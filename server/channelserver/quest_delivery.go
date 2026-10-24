package channelserver

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"go.uber.org/zap"
)

type questDeliveryListSnapshot struct {
	entries              [][]byte
	capacity             int
	contractVersion      int
	evaluatedAt          time.Time
	collabTuneValues     []tuneValue
	collabCatalogVersion string
	collabSourceHash     string
}

// appendPage shares the production page boundary with offline rehearsal. The
// trailing entry may cross 60,000 bytes, matching the native legacy behavior;
// the count is bounded by the snapshot's validated u16 capacity. Production
// snapshots are constructed by encodeQuestDeliverySnapshot before this cast.
func (snapshot *questDeliveryListSnapshot) appendPage(bf *byteframe.ByteFrame, offset uint16) (total, returned uint16) {
	total = uint16(len(snapshot.entries))
	for i, data := range snapshot.entries {
		if i >= int(offset) && len(bf.Data()) < 60000 {
			returned++
			bf.WriteBytes(data)
		}
	}
	return total, returned
}

// allowsQuestDelivery adds location/time restrictions only. It never grants
// campaign rights, tickets, progression, courses, or dedicated event eligibility.
func (s *Session) allowsQuestDelivery(questID int, now time.Time) bool {
	if _, known := commonCollabDefinition(questID); known && s.server != nil && (s.server.questDelivery.CommonCollabDeclared() || s.server.commonCollab != nil) {
		if s.server.validateCommonCollabRuntime() != nil {
			return false
		}
		return s.server.questDelivery.AllowsCommonCollab(questID, s.server.worldName, s.server.channelNumber, now)
	}
	managed, allowed := s.questDeliveryDecision(questID, now)
	return !managed || allowed
}

func (s *Session) questDeliveryDecision(questID int, now time.Time) (bool, bool) {
	if s.server == nil || s.server.questDelivery == nil {
		return false, false
	}
	ids := []int{questID}
	// ZZ lists these legacy Tower rows using their replacement file IDs.
	// A policy on either name must also constrain cached file/departure requests.
	switch questID {
	case towerQuestGuardian1:
		ids = append(ids, towerQuestMilestone1)
	case towerQuestMilestone1:
		ids = append(ids, towerQuestGuardian1)
	case towerQuestGuardian2:
		ids = append(ids, towerQuestMilestone2)
	case towerQuestMilestone2:
		ids = append(ids, towerQuestGuardian2)
	}
	managed, allowed := false, true
	for _, id := range ids {
		m, a := s.server.questDelivery.Allows(id, s.server.worldName, s.server.channelNumber, now)
		if m {
			managed = true
			allowed = allowed && a
		}
	}
	return managed, managed && allowed
}

// legacyQuestActiveAt evaluates legacy rows without changing their start_time.
// Managed rows instead use the manifest's explicit immutable schedule.
func legacyQuestActiveAt(eq EventQuest, now time.Time) bool {
	if eq.ActiveDays <= 0 {
		return true
	}
	if eq.InactiveDays < 0 || eq.StartTime.IsZero() || now.Before(eq.StartTime) {
		return false
	}
	const secondsPerDay = int64(24 * 60 * 60)
	a, b := int64(eq.ActiveDays), int64(eq.InactiveDays)
	if a > math.MaxInt64/secondsPerDay || b > math.MaxInt64/secondsPerDay-a {
		return false
	}
	cycle := (a + b) * secondsPerDay
	elapsed := now.Unix() - eq.StartTime.Unix()
	if now.Nanosecond() < eq.StartTime.Nanosecond() {
		elapsed--
	}
	return elapsed%cycle < a*secondsPerDay
}

func deliveryQuestIDFromFilename(filename string) (int, bool) {
	name := strings.ToLower(filepath.Base(filename))
	if len(name) < 7 || (name[5] != 'd' && name[5] != 'n') {
		return 0, false
	}
	for i := range name {
		if i == 5 {
			continue
		}
		if name[i] < '0' || name[i] > '9' {
			return 0, false
		}
	}
	id, err := strconv.Atoi(name[:5])
	return id, err == nil && id > 0 && id <= math.MaxUint16
}

// questDeliveryPreparation owns an immutable cached page or one new epoch.
// All repository/encoding work occurs outside gameplay/list state locks.
type questDeliveryPreparation struct {
	epoch  uint64
	cached *questDeliveryListSnapshot
}

func (s *Session) beginQuestDeliveryPreparation(offset uint16) questDeliveryPreparation {
	s.questDeliveryListMu.Lock()
	defer s.questDeliveryListMu.Unlock()
	if offset != 0 && s.questDeliveryList != nil {
		return questDeliveryPreparation{cached: s.questDeliveryList}
	}
	s.questDeliveryListEpoch++
	s.questClientCache = nil
	return questDeliveryPreparation{epoch: s.questDeliveryListEpoch}
}

func (s *Session) abortQuestDeliveryPreparation(preparation questDeliveryPreparation) {
	if preparation.epoch == 0 {
		return
	}
	s.questDeliveryListMu.Lock()
	defer s.questDeliveryListMu.Unlock()
	if s.questDeliveryListEpoch == preparation.epoch {
		s.questDeliveryList = nil
		s.questDeliveryListEpoch++
	}
}

// Invalid immutable capacity/config is detected synchronously before request
// preparation; invalidate every older in-flight token as well as cached pages.
func (s *Session) invalidateQuestDeliveryConfig() {
	s.questDeliveryListMu.Lock()
	s.questDeliveryList = nil
	s.questClientCache = nil
	s.questDeliveryListEpoch++
	s.questDeliveryListMu.Unlock()
}

// Offset zero begins a new enumeration; nonzero cached pages never re-query.
// File/departure checks still use current publication and original authority.
func (s *Session) questDeliverySnapshot(offset uint16, now time.Time, access ...*QuestAccessSnapshot) (*questDeliveryListSnapshot, error) {
	return s.questDeliverySnapshotPrepared(s.beginQuestDeliveryPreparation(offset), now, access...)
}

func (s *Session) questDeliverySnapshotPrepared(preparation questDeliveryPreparation, now time.Time, access ...*QuestAccessSnapshot) (*questDeliveryListSnapshot, error) {
	if preparation.cached != nil {
		return preparation.cached, nil
	}
	if preparation.epoch == 0 {
		return nil, fmt.Errorf("invalid quest delivery preparation")
	}
	returnAllowed := s.canUseReturnWorldQuests()
	quests, err := s.server.eventRepo.GetEventQuests()
	pinned := s.questAccessSnapshot.Load()
	if len(access) != 0 {
		pinned = access[0]
	}
	var result *questDeliveryListSnapshot
	if err == nil {
		result, err = s.encodeQuestDeliverySnapshotWithReturn(quests, now, returnAllowed, func(eq EventQuest) ([]byte, error) {
			return makeEventQuestWithAccess(s, eq, pinned, now)
		})
	}
	s.questDeliveryListMu.Lock()
	defer s.questDeliveryListMu.Unlock()
	if s.questDeliveryListEpoch != preparation.epoch {
		return nil, fmt.Errorf("quest delivery preparation was superseded")
	}
	s.questDeliveryListEpoch++ // Token is single-use even after failure.
	if err != nil {
		s.questDeliveryList = nil
		return nil, err
	}
	s.questDeliveryList = result
	return result, nil
}

func (s *Session) encodeQuestDeliverySnapshot(quests []EventQuest, now time.Time, encode func(EventQuest) ([]byte, error)) (*questDeliveryListSnapshot, error) {
	return s.encodeQuestDeliverySnapshotWithReturn(quests, now, s.canUseReturnWorldQuests(), encode)
}

func (s *Session) encodeQuestDeliverySnapshotWithReturn(quests []EventQuest, now time.Time, returnAllowed bool, encode func(EventQuest) ([]byte, error)) (*questDeliveryListSnapshot, error) {
	capacity, err := s.eventQuestCapacity()
	if err != nil {
		return nil, err
	}
	commonView, err := s.commonCollabRuntimeViewAt(now)
	if err != nil {
		return nil, err
	}
	if commonView != nil {
		quests, err = s.server.commonCollab.merge(quests, commonView)
		if err != nil {
			return nil, err
		}
	} else {
		quests = s.appendBuiltInCollabQuests(quests)
	}
	quests = s.withReturnWorldQuestsAllowed(quests, returnAllowed)
	quests = orderTowerQuests(quests)
	result := &questDeliveryListSnapshot{capacity: capacity, contractVersion: questEnumerationContractVersion, evaluatedAt: now}
	if commonView != nil {
		result.collabTuneValues = append([]tuneValue(nil), commonView.tunes...)
		result.collabCatalogVersion, result.collabSourceHash = commonView.version, commonView.sourceHash
	} else {
		result.collabTuneValues = s.appendCollabTuneValues(nil)
	}
	seen := make(map[int]bool, len(quests))
	validCount := 0
	var towerData *TowerData
	for _, eq := range quests {
		// Keep DB rows through provider merging so hidden Return quests are
		// not re-injected. This flag only hides extra ordinary list records;
		// native receptions, file access and progress retain their row IDs.
		if s.server.erupeConfig.QuestDelivery.Enabled && s.server.erupeConfig.RealClientMode == cfg.ZZ && eq.ListHidden && (eq.QuestType == 18 || eq.QuestType == 28 || eq.QuestType == 54) {
			continue
		}
		if _, known := commonCollabDefinition(eq.QuestID); known && commonView != nil {
			if !commonView.activeIDs[eq.QuestID] {
				continue
			}
		} else if !s.allowsQuestDelivery(eq.QuestID, now) || !s.allowsCollabQuest(eq) {
			continue
		}
		if !s.allowsTowerQuest(eq, &towerData) {
			continue
		}
		managed, _ := s.questDeliveryDecision(eq.QuestID, now)
		if !managed && !legacyQuestActiveAt(eq, now) {
			continue
		}
		listedID := towerMilestoneListing(eq, s.server.erupeConfig.RealClientMode).QuestID
		if seen[listedID] {
			continue
		}
		data, err := encode(eq)
		if err != nil || len(data) < questDataMinLen || len(data) > questDataMaxLen {
			if managed {
				if err != nil {
					return nil, fmt.Errorf("managed quest %d cannot be listed in world %q channel %d: %w", eq.QuestID, s.server.worldName, s.server.channelNumber, err)
				}
				return nil, fmt.Errorf("managed quest %d has invalid list data length %d (expected %d..%d) in world %q channel %d", eq.QuestID, len(data), questDataMinLen, questDataMaxLen, s.server.worldName, s.server.channelNumber)
			}
			if s.logger != nil {
				s.logger.Warn("Quest omitted from delivery snapshot: missing or invalid list data",
					zap.Int("questID", eq.QuestID), zap.Int("bytes", len(data)), zap.Error(err))
			}
			continue
		}
		seen[listedID] = true
		validCount++
		if validCount <= capacity {
			result.entries = append(result.entries, data)
		}
	}
	if validCount > capacity {
		return nil, fmt.Errorf("quest delivery has %d valid quests, exceeding client capacity %d by %d in world %q channel %d", validCount, capacity, validCount-capacity, s.server.worldName, s.server.channelNumber)
	}
	return result, nil
}

func (s *Session) deliveryFileAllowed(filename string, now time.Time) bool {
	// A normal quest filename is decimal ID plus d/n and a variant number.
	// Scenario and other file formats retain their existing dedicated handlers.
	qid, ok := deliveryQuestIDFromFilename(strings.TrimSpace(filename))
	return !ok || s.allowsQuestDelivery(qid, now)
}
