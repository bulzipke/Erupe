package channelserver

import (
	"encoding/binary"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

// questDeliveryDeparture is immutable after publication. Stage admission pins
// real wall time, independently of the game's debug-adjusted clock. A zero ID
// means that admission preceded the host's validated quest setup.
type questDeliveryDeparture struct {
	returnEligibility *returnQuestEligibilitySnapshot
	stage             *Stage
	admittedAt        time.Time
	questID           uint16
	receipt           *questDeliveryReceipt
	accessSnapshot    *QuestAccessSnapshot
}

// The result receipt deliberately has no Stage pointer: retaining the last
// result across town must not keep an empty stage and its binary/object maps
// alive for the rest of a long login session.
type questDeliveryReceipt struct {
	nativeIntrusionResultID uint16
	returnEligibility       *returnQuestEligibilitySnapshot
	admittedAt              time.Time
	questID                 uint16
	caravanCourseID         uint16
	accessSnapshot          *QuestAccessSnapshot
}

func (s *Session) nativeCaravanCourse(setup []byte) uint16 {
	// The validated ZZ setup carries category 20 at 0x2a and the native
	// course number at 0x56. Ordinary quest setup uses other categories.
	const categoryOffset, courseOffset = 0x2a, 0x56
	if len(setup) <= courseOffset+1 || setup[categoryOffset] != 20 || s.server.erupeConfig.RealClientMode != cfg.ZZ {
		return 0
	}
	return binary.LittleEndian.Uint16(setup[courseOffset:])
}

func (s *Session) allowsQuestDeparture(questID, caravanCourseID uint16, now time.Time) bool {
	return s.allowsQuestDelivery(int(questID), now) || s.server.questDelivery.AllowsCaravan(int(questID), questID, caravanCourseID)
}

type questDeliveryDepartureRejection struct {
	session   *Session
	departure *questDeliveryDeparture
}

// admitQuestDeliveryLocked runs before adding a client to the stage, while the
// caller holds Session.lifecycleMu followed by Stage's write lock. The atomic
// stamp lets late host setup inspect other participants without reversing that
// lock order. It grants no entry ticket, campaign entitlement, or progression.
func (s *Session) admitQuestDeliveryLocked(stage *Stage, alreadyClient bool, now time.Time, preparedAccess ...*QuestAccessSnapshot) bool {
	return s.admitQuestDeliveryWithReturnLocked(stage, alreadyClient, now, nil, preparedAccess...)
}

func (s *Session) admitQuestDeliveryWithReturnLocked(stage *Stage, alreadyClient bool, now time.Time, preparedReturn *returnQuestEligibilitySnapshot, preparedAccess ...*QuestAccessSnapshot) bool {
	policies := s.questEntryPoliciesEnabled()
	if stageKind(stage.id) != "Qs" {
		s.questDeliveryDeparture.Store(nil)
		return true
	}
	if old := s.questDeliveryDeparture.Load(); alreadyClient && old != nil && old.stage == stage {
		// Duplicate transfer packets for the same live run are not new entry.
		return true
	}
	access := s.questAccessSnapshot.Load()
	if policies && s.server.questAccess != nil && len(preparedAccess) != 0 {
		access = preparedAccess[0]
		if access == nil {
			// A skipped duplicate preflight cannot authorize a new entry after
			// another transfer changed membership before this lock was taken.
			return false
		}
	}
	var questID uint16
	var nativeIntrusionResultID uint16
	var caravanCourseID uint16
	if setup, exists := stage.rawBinaryData[stageBinaryKey{1, 3}]; exists {
		var decoded bool
		questID, _, _, decoded = decodeQuestRunSetupFromStageBinary(stage.id, 1, 3, setup)
		nativeIntrusionResultID = s.nativeIntrusionResultFromSetup(stage.id, setup)
		if decoded {
			caravanCourseID = s.nativeCaravanCourse(setup)
		}
		if policies && (!decoded || !s.allowsQuestDeparture(questID, caravanCourseID, now) || !s.allowsQuestAccessSnapshot(access, int(questID), now)) || decoded && !s.allowsReturnQuestSnapshot(preparedReturn, int(questID), now) {
			return false
		}
	}
	if !policies && preparedReturn == nil {
		// No added restriction for an ordinary/unknown legacy run when the
		// optional reader is unavailable. A known Return ID was denied above;
		// late Return setup still requires its own same-stage pinned evidence.
		s.questDeliveryDeparture.Store(nil)
		return true
	}
	receipt := &questDeliveryReceipt{admittedAt: now, questID: questID, accessSnapshot: access, returnEligibility: preparedReturn, nativeIntrusionResultID: nativeIntrusionResultID, caravanCourseID: caravanCourseID}
	departure := &questDeliveryDeparture{stage: stage, admittedAt: now, questID: questID, receipt: receipt, accessSnapshot: access, returnEligibility: preparedReturn}
	s.questDeliveryDeparture.Store(departure)
	s.questDeliveryReceipt.Store(receipt)
	return true
}

// validateQuestDeliverySetupLocked runs under Stage's write lock immediately
// before storing the setup. It never takes a participant's lifecycle lock.
// Pending participants are checked at their own admission time, so a slow setup
// cannot interrupt a run admitted before the publication window ended.
func (s *Session) validateQuestDeliverySetupLocked(stage *Stage, pkt *mhfpacket.MsgSysSetStageBinary, questID uint16, decoded bool) (bool, []questDeliveryDepartureRejection) {
	if stageKind(stage.id) != "Qs" || pkt.BinaryType0 != 1 || pkt.BinaryType1 != 3 {
		return true, nil
	}
	// This extra predicate never adds a connection-close rejection. An invalid
	// Return setup is rejected atomically before changing any participant/state.
	if !s.returnQuestSetupAllowedLocked(stage, questID, decoded) {
		return false, nil
	}
	if !s.questEntryPoliciesEnabled() && (!decoded || !isReturnWorldQuest(int(questID))) {
		return true, nil
	}
	var caravanCourseID uint16
	if decoded {
		caravanCourseID = s.nativeCaravanCourse(pkt.RawDataPayload)
		if existing, ok := stage.rawBinaryData[stageBinaryKey{1, 3}]; ok && len(stage.clients) != 0 {
			previousID, _, _, valid := decodeQuestRunSetupFromStageBinary(stage.id, 1, 3, existing)
			if !valid || previousID != questID || s.nativeCaravanCourse(existing) != caravanCourseID {
				// Replacing a running quest cannot borrow its earlier admission.
				return false, nil
			}
		}
		for participant := range stage.clients {
			old := participant.questDeliveryDeparture.Load()
			if old != nil && old.stage == stage && old.questID != 0 && old.questID != questID {
				return false, nil
			}
		}
	}
	var rejected []questDeliveryDepartureRejection
	for participant := range stage.clients {
		old := participant.questDeliveryDeparture.Load()
		if old != nil && old.stage != stage {
			// The participant is concurrently leaving this stage. Its new stamp
			// belongs to another transfer and must not be overwritten or closed.
			continue
		}
		if !decoded && old != nil && old.questID != 0 {
			// Keep an already validated run intact; discard the bad update.
			continue
		}
		if !decoded || old == nil || old.admittedAt.IsZero() || !participant.allowsQuestDeparture(questID, caravanCourseID, old.admittedAt) || !participant.allowsQuestAccessSnapshot(old.accessSnapshot, int(questID), old.admittedAt) {
			rejected = append(rejected, questDeliveryDepartureRejection{participant, old})
			delete(stage.clients, participant)
			continue
		}
		if old.questID == 0 {
			receipt := &questDeliveryReceipt{admittedAt: old.admittedAt, questID: questID, accessSnapshot: old.accessSnapshot, returnEligibility: old.returnEligibility, nativeIntrusionResultID: participant.nativeIntrusionResultFromSetup(stage.id, pkt.RawDataPayload), caravanCourseID: caravanCourseID}
			validated := &questDeliveryDeparture{
				stage: stage, admittedAt: old.admittedAt, questID: questID,
				receipt: receipt, accessSnapshot: old.accessSnapshot, returnEligibility: old.returnEligibility,
			}
			if participant.questDeliveryDeparture.CompareAndSwap(old, validated) {
				participant.questDeliveryReceipt.CompareAndSwap(old.receipt, receipt)
			}
		}
	}
	return decoded, rejected
}

// Receipt state survives a return to town because ordinary quest results and
// savedata can arrive after that transition. A pending departure cannot become
// an unguarded save merely by leaving its stage before sending the setup.
// This is intentionally limited to departures observed by this connection;
// it does not try to authenticate every save from a fresh login.
func (s *Session) allowsQuestDeliverySave() bool {
	receipt := s.questDeliveryReceipt.Load()
	if receipt != nil && !s.allowsReturnQuestResult(receipt, receipt.questID) {
		return false
	}
	if !s.questEntryPoliciesEnabled() {
		return true
	}
	return receipt == nil || (receipt.questID != 0 && s.allowsQuestAccessResult(receipt, receipt.questID))
}

func (s *Session) allowsQuestDeliveryRecord(data []byte) bool {
	pinned := s.questDeliveryReceipt.Load()
	if len(data) >= questIDOffset+2 {
		resultID := binary.LittleEndian.Uint16(data[questIDOffset : questIDOffset+2])
		if !s.allowsReturnQuestResult(pinned, resultID) {
			return false
		}
	} else if pinned != nil && !s.allowsReturnQuestResult(pinned, pinned.questID) {
		return false
	}
	if !s.questEntryPoliciesEnabled() {
		return true
	}
	receipt := s.questDeliveryReceipt.Load()
	if receipt != nil && receipt.questID == 0 {
		return false
	}
	if len(data) < questIDOffset+2 {
		// Legacy validation handles records with no usable quest ID.
		return true
	}
	questID := binary.LittleEndian.Uint16(data[questIDOffset : questIDOffset+2])
	if !s.allowsQuestAccessResult(receipt, questID) {
		return false
	}
	managed, _ := s.questDeliveryDecision(int(questID), time.Now())
	if !managed {
		return true
	}
	if receipt != nil && s.server.questDelivery.AllowsCaravan(int(questID), receipt.questID, receipt.caravanCourseID) {
		return true
	}
	return receipt != nil && (sameQuestDeliveryResultID(receipt.questID, questID) || receipt.nativeIntrusionResultID != 0 && receipt.nativeIntrusionResultID == questID) && s.allowsQuestDelivery(int(questID), receipt.admittedAt)
}

func sameQuestDeliveryResultID(receiptID, resultID uint16) bool {
	if receiptID == resultID {
		return true
	}
	switch receiptID {
	case towerQuestGuardian1:
		return resultID == towerQuestMilestone1
	case towerQuestMilestone1:
		return resultID == towerQuestGuardian1
	case towerQuestGuardian2:
		return resultID == towerQuestMilestone2
	case towerQuestMilestone2:
		return resultID == towerQuestGuardian2
	}
	return false
}

// SET_STAGE_BINARY has no ACK handle. Denied pending admissions therefore close
// their connection instead of leaving a client in an acknowledged but unusable
// quest stage. Cleanup runs after releasing Stage, preserving lifecycle->Stage
// lock order; the pointer identity prevents closing a later successful transfer.
func rejectQuestDeliveryDepartures(stage *Stage, rejected []questDeliveryDepartureRejection) {
	for _, rejection := range rejected {
		s := rejection.session
		s.lifecycleMu.Lock()
		if s.questDeliveryDeparture.Load() != rejection.departure {
			s.lifecycleMu.Unlock()
			continue
		}
		s.Lock()
		current := s.stage
		s.Unlock()
		if current != stage {
			s.lifecycleMu.Unlock()
			continue
		}
		s.markClosed()
		s.questDeliveryDeparture.Store(nil)
		s.clearQuestRunMode(0)
		s.clearQuestConquestLevel(0)
		s.endQuestRun()
		s.questWeaponState.Store(0)
		s.questWeaponGeneration++
		s.questWeaponPendingStage = ""
		s.questWeaponPendingGeneration = 0
		removeSessionFromStage(s)
		s.Lock()
		s.stage = nil
		s.Unlock()
		s.lifecycleMu.Unlock()
		s.closeConnection()
	}
}
