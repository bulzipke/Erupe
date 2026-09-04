package channelserver

import (
	"encoding/binary"
	"time"

	"erupe-ce/common/mhfquest"

	"go.uber.org/zap"
)

type questRunMode uint8

const (
	questRunModeUnknown questRunMode = iota
	questRunModeNormal
	questRunModeHardcore
)

const (
	questRunStageBinaryType0         = uint8(1)
	questRunStageBinaryType1         = uint8(3)
	questRunStageBinaryPayloadSize   = 144
	questRunStageQuestIDOffset       = 0x24
	questRunStageHardcoreFlagsOffset = 0x54
	questRunStageConquestLevelOffset = 0x62
	questRunStageQuestIDMirrorOffset = 0x68
	questRunStageHardcoreFlag        = uint8(0x08)
	questRunConquestLevelMax         = uint16(9999)

	questRunRecordPayloadSize        = 1196
	questRunRecordHardcoreModeOffset = 0x3c4

	questRunStateModeShift = 16
)

func (m questRunMode) String() string {
	switch m {
	case questRunModeNormal:
		return "normal"
	case questRunModeHardcore:
		return "hardcore"
	default:
		return "unknown"
	}
}

// decodeQuestRunModeFromStageBinary decodes the ZZ quest setup structure that
// was isolated by comparing normal and HC runs of the same optional-HC quest.
// Requiring the exact structure size and matching duplicated quest IDs keeps
// unrelated stage binary payloads from becoming ranking state.
func decodeQuestRunModeFromStageBinary(stageID string, binaryType0, binaryType1 uint8, payload []byte) (uint16, questRunMode, bool) {
	questID, mode, _, ok := decodeQuestRunSetupFromStageBinary(stageID, binaryType0, binaryType1, payload)
	return questID, mode, ok
}

// decodeQuestRunSetupFromStageBinary also captures the runtime Conquest level
// selected by the host. The level is meaningful only for a quest later
// classified as Conquest; all other hunt records must store zero.
func decodeQuestRunSetupFromStageBinary(stageID string, binaryType0, binaryType1 uint8, payload []byte) (uint16, questRunMode, uint16, bool) {
	if stageKind(stageID) != "Qs" ||
		binaryType0 != questRunStageBinaryType0 ||
		binaryType1 != questRunStageBinaryType1 ||
		len(payload) != questRunStageBinaryPayloadSize {
		return 0, questRunModeUnknown, 0, false
	}

	questID := binary.LittleEndian.Uint16(payload[questRunStageQuestIDOffset : questRunStageQuestIDOffset+2])
	mirroredQuestID := binary.LittleEndian.Uint16(payload[questRunStageQuestIDMirrorOffset : questRunStageQuestIDMirrorOffset+2])
	if questID == 0 || mirroredQuestID != questID {
		return 0, questRunModeUnknown, 0, false
	}
	conquestLevel := binary.LittleEndian.Uint16(payload[questRunStageConquestLevelOffset : questRunStageConquestLevelOffset+2])
	if conquestLevel == 0 || conquestLevel > questRunConquestLevelMax {
		conquestLevel = 0
	}

	if payload[questRunStageHardcoreFlagsOffset]&questRunStageHardcoreFlag != 0 {
		return questID, questRunModeHardcore, conquestLevel, true
	}
	return questID, questRunModeNormal, conquestLevel, true
}

// decodeQuestRunModeFromRecordLog is a corroborating signal from the ZZ quest
// result. Unknown sizes and values are deliberately rejected, and this value
// is never used by itself to classify an optional-HC hunt.
func decodeQuestRunModeFromRecordLog(data []byte) questRunMode {
	if len(data) != questRunRecordPayloadSize {
		return questRunModeUnknown
	}
	switch data[questRunRecordHardcoreModeOffset] {
	case 0:
		return questRunModeNormal
	case 1:
		return questRunModeHardcore
	default:
		return questRunModeUnknown
	}
}

func (s *Session) storeQuestRunMode(questID uint16, mode questRunMode) {
	if questID == 0 || (mode != questRunModeNormal && mode != questRunModeHardcore) {
		return
	}
	s.questRunState.Store(uint32(questID) | uint32(mode)<<questRunStateModeShift)
}

func (s *Session) peekQuestRunMode(questID uint16) questRunMode {
	state := s.questRunState.Load()
	if uint16(state) != questID {
		return questRunModeUnknown
	}
	mode := questRunMode(state >> questRunStateModeShift)
	if mode != questRunModeNormal && mode != questRunModeHardcore {
		return questRunModeUnknown
	}
	return mode
}

func (s *Session) clearQuestRunMode(questID uint16) {
	for {
		state := s.questRunState.Load()
		if state == 0 || (questID != 0 && uint16(state) != questID) {
			return
		}
		if s.questRunState.CompareAndSwap(state, 0) {
			return
		}
	}
}

func (s *Session) storeQuestConquestLevel(questID, level uint16) {
	if questID == 0 {
		return
	}
	if level > questRunConquestLevelMax {
		level = 0
	}
	s.questConquestLevelState.Store(uint32(questID)<<16 | uint32(level))
}

func (s *Session) peekQuestConquestLevel(questID uint16) uint16 {
	state := s.questConquestLevelState.Load()
	if uint16(state>>16) != questID {
		return 0
	}
	level := uint16(state)
	if level == 0 || level > questRunConquestLevelMax {
		return 0
	}
	return level
}

func (s *Session) clearQuestConquestLevel(questID uint16) {
	for {
		state := s.questConquestLevelState.Load()
		if state == 0 || (questID != 0 && uint16(state>>16) != questID) {
			return
		}
		if s.questConquestLevelState.CompareAndSwap(state, 0) {
			return
		}
	}
}

// resolveQuestRunVariant only replaces the optional-HC sentinel. Exact monster
// identities, fixed HC, Zenith, challenge, and every other existing category
// retain their static classification.
func resolveQuestRunVariant(base mhfquest.HuntVariant, stageMode, recordMode questRunMode) (mhfquest.HuntVariant, bool) {
	if base != mhfquest.HuntVariantHardcoreOptional {
		return base, true
	}

	// The setup packet is the authoritative signal. The result byte has only
	// been observed as a matching 0/1 pair, so use it to reject disagreement
	// but not as a fallback when setup state is unavailable.
	if stageMode == questRunModeUnknown {
		return mhfquest.HuntVariantHardcoreOptional, false
	}
	if recordMode != questRunModeUnknown && stageMode != recordMode {
		return mhfquest.HuntVariantHardcoreOptional, false
	}

	switch stageMode {
	case questRunModeNormal:
		return mhfquest.HuntVariantNormal, true
	case questRunModeHardcore:
		return mhfquest.HuntVariantHardcore, true
	default:
		return mhfquest.HuntVariantHardcoreOptional, false
	}
}

// beginQuestRun publishes the quest this session just entered, so the dashboard
// can show what is being hunted and for how long. The title is resolved once
// here because it reads the quest file, which is far too costly to repeat on
// every dashboard poll.
func (s *Session) beginQuestRun() {
	questID := uint16(s.questRunState.Load())
	if questID == 0 {
		// The quest was selected without a decodable run mode, so the ID is not
		// known. Leave the previous run cleared rather than showing a stale one.
		s.endQuestRun()
		return
	}
	s.activeQuestID.Store(uint32(questID))
	s.activeQuestStart.Store(TimeAdjusted().Unix())
	s.activeQuestName.Store(questTitleForRecord(s, questID))
}

// endQuestRun clears the published quest run.
func (s *Session) endQuestRun() {
	s.activeQuestID.Store(0)
	s.activeQuestStart.Store(0)
	s.activeQuestName.Store("")
}

// recordQuestWeaponDeparture counts one authenticated hunter entering a quest
// and snapshots the weapon class for a later personal-best result.  Companion
// NPCs never own a Session and therefore cannot reach this path.
func (s *Session) recordQuestWeaponDeparture(questID uint16, generation uint64) {
	if s.server == nil || s.server.weaponUsageRepo == nil {
		return
	}
	s.armQuestWeaponDeparture(questID, generation)
	s.recordArmedQuestWeaponDeparture(questID, generation)
}

// armQuestWeaponDeparture marks a validated attempt before any database work.
// Results may arrive on another session's handler while the host is processing
// a late stage binary, so every participant must be armed first.
func (s *Session) armQuestWeaponDeparture(questID uint16, generation uint64) bool {
	if questID == 0 {
		return false
	}
	marker := uint32(questID) << 8
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.questWeaponGeneration != generation {
		return false
	}
	s.questWeaponState.Store(marker)
	return true
}

// recordArmedQuestWeaponDeparture performs the aggregate increment after the
// attempt marker has been published. It still counts a real older departure if
// a newer generation supersedes it while the database work is queued.
func (s *Session) recordArmedQuestWeaponDeparture(questID uint16, generation uint64) {
	marker := uint32(questID) << 8
	clearMarker := func() {
		if questID == 0 {
			return
		}
		s.lifecycleMu.Lock()
		if s.questWeaponGeneration == generation && s.questWeaponState.Load() == marker {
			s.questWeaponState.Store(0)
		}
		s.lifecycleMu.Unlock()
	}
	if s.server == nil || s.server.weaponUsageRepo == nil {
		clearMarker()
		return
	}
	weaponType, ok, err := s.server.weaponUsageRepo.RecordQuestDeparture(s.charID)
	if err != nil {
		clearMarker()
		s.logger.Warn("Failed to record quest-departure weapon usage",
			zap.Uint32("charID", s.charID),
			zap.Uint16("questID", questID),
			zap.Error(err))
		return
	}
	if !ok {
		clearMarker()
		s.logger.Warn("Quest departure has no valid persisted weapon type",
			zap.Uint32("charID", s.charID),
			zap.Uint16("questID", questID))
		return
	}
	// Personal hunt records and validated departure setup decoding are ZZ-only,
	// so an aggregate call without a quest ID must not retain a snapshot.
	if questID == 0 {
		return
	}
	// Serialize the final write with new departures. The database operation may
	// have taken long enough for this session to enter another quest or for the
	// matching result packet to consume the in-flight marker.
	s.lifecycleMu.Lock()
	if s.questWeaponGeneration == generation && s.questWeaponState.Load() == marker {
		// Adding one makes weapon type 0 distinguishable from an unknown state.
		s.questWeaponState.Store(marker | uint32(weaponType+1))
	}
	s.lifecycleMu.Unlock()
}

// armPendingQuestWeaponDeparture consumes and arms a pending first entry only
// for the stage whose validated setup just arrived. lifecycleMu makes the two
// state changes atomic with transfers and result processing.
func (s *Session) armPendingQuestWeaponDeparture(stageID string, questID uint16) (uint64, bool) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed.Load() || s.questWeaponPendingStage != stageID {
		return 0, false
	}
	generation := s.questWeaponPendingGeneration
	s.questWeaponPendingStage = ""
	s.questWeaponPendingGeneration = 0
	if questID != 0 && s.questWeaponGeneration == generation {
		s.questWeaponState.Store(uint32(questID) << 8)
	}
	return generation, true
}

// questWeaponForResult returns the weapon captured for this exact quest. A
// delayed result from an older quest must not borrow a newer attempt's weapon.
func (s *Session) questWeaponForResult(questID uint16) (uint8, bool) {
	state := s.questWeaponState.Load()
	if uint16(state>>8) != questID {
		return 0, false
	}
	encodedType := uint8(state)
	if encodedType == 0 || encodedType > 14 {
		return 0, false
	}
	return encodedType - 1, true
}

// clearQuestWeaponForResult clears only the matching attempt, preserving a
// newer departure if an older result packet arrives late.
func (s *Session) clearQuestWeaponForResult(questID uint16) {
	for {
		state := s.questWeaponState.Load()
		if state == 0 || uint16(state>>8) != questID {
			return
		}
		if s.questWeaponState.CompareAndSwap(state, 0) {
			return
		}
	}
}

// activeQuestRun returns the quest this session is currently in. ok is false
// when the session is not in a quest.
func (s *Session) activeQuestRun() (questID uint16, name string, startedAt time.Time, ok bool) {
	id := uint16(s.activeQuestID.Load())
	if id == 0 {
		return 0, "", time.Time{}, false
	}
	started := s.activeQuestStart.Load()
	if started == 0 {
		return 0, "", time.Time{}, false
	}
	title, _ := s.activeQuestName.Load().(string)
	return id, title, time.Unix(started, 0), true
}

// recordQuestResult stores one finished quest attempt.
//
// The ZZ client sends a record log whenever a quest ends, with the outcome in a
// header byte, so nothing has to be inferred from session state: retires are
// simply not recorded, and an attempt that never reports a result (a disconnect
// mid-quest) is not counted either way.
func (s *Session) recordQuestResult(questID uint16, questName string, cleared bool) {
	if questID == 0 || s.server == nil || s.server.questStatsRepo == nil {
		return
	}
	if err := s.server.questStatsRepo.RecordResult(questID, questName, cleared); err != nil {
		s.logger.Warn("Failed to record quest result",
			zap.Uint16("questID", questID),
			zap.Bool("cleared", cleared),
			zap.Error(err))
	}
}
