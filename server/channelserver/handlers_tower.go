package channelserver

import (
	cfg "erupe-ce/config"
	"fmt"
	"math"
	"strings"
	"time"

	"go.uber.org/zap"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
	"erupe-ce/network/mhfpacket"
)

// maxTowerFloorReport is the per-block cumulative cap in G10 FUN_107b5520
// (and ZZ FUN_10b1c050), before the separate temporary Guardian milestone cap.
// Both blocks can exceed 1000; 500 is a milestone, not the top of either zone.
const maxTowerFloorReport = 9999

// towerFloorGain saturates cumulative progress without overflowing or lowering
// an existing record. Run/history credit uses the same effective increment.
func towerFloorGain(reached, gain int32) int32 {
	if reached < 0 || reached >= maxTowerFloorReport || gain <= 0 {
		return 0
	}
	return min(gain, maxTowerFloorReport-reached)
}

// Tower zone departures the receptionist lists per character. The prologue
// (21729, map 71, one tutorial floor) is always offered; zone 1 (21732, map 71)
// and zone 2 (21733, map 73) have optional custom floor gates configured in
// TowerZone1UnlockFloor / TowerZone2UnlockFloor (both default 0). The prologue
// awards TRP but no climbed floors, so it cannot unlock a positive floor gate.
// Both zones require positive cumulative TRP by default (TowerZone1/2UnlockTRP=1),
// a requested server policy rather than a verified original G10 requirement.
// Zone 2 requires TR51 by default; its HR5 check is client-side. The client has
// no list-side rule of its own (the G10.1 departure list was "category 0x43 quest records
// the server enumerated"), so the server decides what each hunter sees.
const (
	towerQuestPrologue   = 21729
	towerQuestZone1      = 21732
	towerQuestZone2      = 21733
	towerQuestGuardian1  = 21731 // 緊急調査依頼: 20-minute Guardian arena of block 1 (map 72)
	towerQuestGuardian2  = 21746 // legacy direct arena (map 74)
	towerQuestMilestone1 = 21749 // G10 milestone: exploration floor then Guardian (map 71)
	towerQuestMilestone2 = 21750 // G10 milestone: exploration floor then Guardian (map 73)
)

// towerGuardianFloor reports whether a block floor record sits exactly on a
// Guardian milestone: floor 10, every multiple of 40, and floor 500 (paper rows
// 1104/1105; the client ends a run on these floors). The 天廊遠征録 rule was that
// the 緊急調査依頼 appeared on reaching them and the fight itself was optional,
// so the arena is offered until its poster departs. The G10 departure IT6
// consumes that milestone even on bypass, failure, retirement or disconnect.
// Party guests do not consume their own milestone (G10 host-only rule).
func towerGuardianFloor(record int32) bool {
	return record <= maxTowerFloorReport && (record == 10 || record == 500 || (record >= 40 && record%40 == 0))
}

// A pending urgent replaces the normal investigation of the same block.
// Departure IT6 consumes it even if the poster then requests rescue.
func towerMilestonePending(record, passed int32) bool {
	return towerGuardianFloor(record) && passed < record
}

// towerDataCached reads the tower row once per quest enumeration.
func (s *Session) towerDataCached(cache **TowerData) (*TowerData, bool) {
	if *cache == nil {
		td, err := s.server.towerRepo.GetTowerData(s.charID)
		if err != nil {
			s.logger.Warn("Tower gate: failed to read tower data, hiding tower departures", zap.Error(err))
			return nil, false
		}
		*cache = &td
	}
	return *cache, true
}

// allowsTowerQuest applies the per-character tower gates: the prologue is always
// listed, normal zones wait for configured TRP/rank/floor requirements, and
// Guardian arenas appear only while the matching block record is on a milestone.
func (s *Session) allowsTowerQuest(eq EventQuest, cache **TowerData) bool {
	if eq.QuestType == 55 && s.server.erupeConfig.TowerRotation.Enabled {
		event, err := s.server.towerEvent(TimeAdjusted())
		if err != nil || !event.Active(TimeAdjusted()) {
			return false
		}
	}
	// These IDs also serve Road. Only the Tower category uses Tower unlocks.
	if eq.QuestType == 55 {
		if eq.QuestID == towerQuestMilestone1 {
			eq.QuestID = towerQuestGuardian1
		}
		if eq.QuestID == towerQuestMilestone2 {
			eq.QuestID = towerQuestGuardian2
		}
	}
	switch eq.QuestID {
	case towerQuestZone1:
		needFloor, needTRP := s.server.erupeConfig.TowerZone1UnlockFloor, s.server.erupeConfig.TowerZone1UnlockTRP
		td, ok := s.towerDataCached(cache)
		return ok && (needFloor <= 0 || td.Block1 >= needFloor) &&
			(needTRP <= 0 || td.TRP >= needTRP) &&
			!towerMilestonePending(td.Block1, td.Guardian1)
	case towerQuestZone2:
		// Unlock requirements and pending milestones are independent. Disabling
		// the TRP/rank/floor thresholds must not expose a normal climb at a milestone.
		needFloor, needTR := s.server.erupeConfig.TowerZone2UnlockFloor, s.server.erupeConfig.TowerZone2UnlockTR
		needTRP := s.server.erupeConfig.TowerZone2UnlockTRP
		td, ok := s.towerDataCached(cache)
		return ok && (needFloor <= 0 || td.Block1 >= needFloor) &&
			(needTR <= 0 || td.TR >= needTR) && (needTRP <= 0 || td.TRP >= needTRP) &&
			!towerMilestonePending(td.Block2, td.Guardian2)
	case towerQuestGuardian1, towerQuestGuardian2:
		td, ok := s.towerDataCached(cache)
		if !ok {
			return false
		}
		record, passed := td.Block1, td.Guardian1
		if eq.QuestID == towerQuestGuardian2 {
			record, passed = td.Block2, td.Guardian2
		}
		return towerMilestonePending(record, passed)
	default:
		return true
	}
}

// TowerInfoTRP represents tower RP (points) info.
type TowerInfoTRP struct {
	TR  int32
	TRP int32
}

// TowerInfoSkill represents tower skill info.
type TowerInfoSkill struct {
	TSP    int32
	Skills []int16 // 64
}

// TowerInfoHistory represents tower clear history.
type TowerInfoHistory struct {
	Unk0 []int16 // 5
	Unk1 []int16 // 5
}

// TowerInfoLevel represents tower level info.
type TowerInfoLevel struct {
	Floors int32
	Unk1   int32
	Unk2   int32
	Unk3   int32
}

// EmptyTowerCSV creates an empty CSV string of the given length.
func EmptyTowerCSV(len int) string {
	temp := make([]string, len)
	for i := range temp {
		temp[i] = "0"
	}
	return strings.Join(temp, ",")
}

// towerSurveyRound is Earth value 1001, the current Tower survey round
// (EarthID; 36 when no EarthID is configured). The Tower Status basic info
// page matches the five history rounds against it (G10.1 FUN_104b0d80).
func towerSurveyRound(earthID int32) uint32 {
	if earthID > 0 {
		return uint32(earthID)
	}
	return 36
}

// towerSurveyHistory builds InfoType 4: the current round and the four
// before it (Unk0) with the floors this character cleared in each (Unk1).
func towerSurveyHistory(s *Session) TowerInfoHistory {
	history := TowerInfoHistory{make([]int16, 5), make([]int16, 5)}
	event, err := s.server.towerEvent(TimeAdjusted())
	if err != nil {
		return history
	}
	round := int32(towerSurveyRound(event.ID))
	floors, err := s.server.towerRepo.GetTowerSurveyHistory(event.ID, s.charID)
	if err != nil {
		s.logger.Error("Failed to read tower survey history", zap.Error(err))
	}
	for i := range history.Unk0 {
		history.Unk0[i] = int16(round - int32(i))
		if err == nil {
			history.Unk1[i] = int16(min(floors[i], math.MaxInt16))
		}
	}
	return history
}

func handleMsgMhfGetTowerInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetTowerInfo)
	event, err := s.server.towerEvent(TimeAdjusted())
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	var data []*byteframe.ByteFrame
	type TowerInfo struct {
		TRP     []TowerInfoTRP
		Skill   []TowerInfoSkill
		History []TowerInfoHistory
		Level   []TowerInfoLevel
	}

	towerInfo := TowerInfo{
		TRP:     []TowerInfoTRP{{0, 0}},
		Skill:   []TowerInfoSkill{{0, make([]int16, 64)}},
		History: []TowerInfoHistory{{make([]int16, 5), make([]int16, 5)}},
		Level:   []TowerInfoLevel{{0, 0, 0, 0}, {0, 0, 0, 0}},
	}

	td, err := s.server.towerRepo.GetTowerData(s.charID)
	if err != nil {
		s.logger.Error("Failed to initialize tower data", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	} else {
		towerInfo.TRP[0].TR = td.TR
		towerInfo.TRP[0].TRP = td.TRP
		towerInfo.Skill[0].TSP = td.TSP
		towerInfo.Level[0].Floors = td.Block1
		towerInfo.Level[1].Floors = td.Block2
		towerInfo.Level[0].Unk2 = td.Guardian1
		towerInfo.Level[1].Unk2 = td.Guardian2
	}

	if s.server.erupeConfig.RealClientMode <= cfg.G7 {
		towerInfo.Level = towerInfo.Level[:1]
	}

	roadSkills := ""
	if pkt.InfoType == 2 {
		var roadErr error
		if roadSkills, roadErr = s.server.towerRepo.GetRoadSkills(s.charID); roadErr != nil {
			s.logger.Error("Failed to read road skills", zap.Error(roadErr))
			roadSkills = ""
		}
	}
	for i, skill := range towerMergeSkills(td.Skills, roadSkills) {
		if i >= len(towerInfo.Skill[0].Skills) {
			break
		}
		if skill < math.MinInt16 || skill > math.MaxInt16 {
			continue
		}
		towerInfo.Skill[0].Skills[i] = int16(skill)
	}

	if pkt.InfoType == 4 {
		towerInfo.History[0] = towerSurveyHistory(s)
	}

	switch pkt.InfoType {
	case 1:
		for _, trp := range towerInfo.TRP {
			bf := byteframe.NewByteFrame()
			bf.WriteInt32(trp.TR)
			bf.WriteInt32(trp.TRP)
			data = append(data, bf)
		}
	case 2:
		for _, skills := range towerInfo.Skill {
			bf := byteframe.NewByteFrame()
			bf.WriteInt32(skills.TSP)
			for i := range skills.Skills {
				bf.WriteInt16(skills.Skills[i])
			}
			data = append(data, bf)
		}
	case 4:
		for _, history := range towerInfo.History {
			bf := byteframe.NewByteFrame()
			for i := range history.Unk0 {
				bf.WriteInt16(history.Unk0[i])
			}
			for i := range history.Unk1 {
				bf.WriteInt16(history.Unk1[i])
			}
			data = append(data, bf)
		}
	case 3, 5:
		for _, level := range towerInfo.Level {
			bf := byteframe.NewByteFrame()
			bf.WriteInt32(level.Floors)
			bf.WriteInt32(level.Unk1)
			bf.WriteInt32(level.Unk2)
			bf.WriteInt32(level.Unk3)
			data = append(data, bf)
		}
	}
	doAckTowerSucceed(s, pkt.AckHandle, event.ID, data)
}

func handleMsgMhfPostTowerInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfPostTowerInfo)
	if s.server.erupeConfig.TowerRotation.Enabled {
		if _, err := s.server.towerEvent(TimeAdjusted()); err != nil {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
	}
	resultEvent, hasDeparture := s.towerResultEvent()

	if s.server.erupeConfig.DebugOptions.QuestTools {
		s.logger.Debug(
			p.Opcode().String(),
			zap.Uint32("InfoType", pkt.InfoType),
			zap.Uint32("Unk1", pkt.Unk1),
			zap.Int32("Skill", pkt.Skill),
			zap.Int32("TR", pkt.TR),
			zap.Int32("TRP", pkt.TRP),
			zap.Int32("Cost", pkt.Cost),
			zap.Int32("Unk6", pkt.Unk6),
			zap.Int32("Unk7", pkt.Unk7),
			zap.Int32("Block1", pkt.Block1),
			zap.Int64("Unk9", pkt.Unk9),
		)
	}

	switch pkt.InfoType {
	case 2:
		// Skill learn. ZZ turned the G10 Tower skill screen into its Hunting Road
		// status, so both screens post InfoType 2 with the skill index and its
		// cost. The two sets are stored apart like the originals: a Tower Status
		// learn (vorbis.dll marks it with Unk1 = towerStatusLearnTag) is any of
		// the 22 G10 Tower skills, paid with TSP and kept in tower.skills; a
		// Hunting Road learn is paid with Road SP from the save data and kept in
		// road_skills. The nine common skills therefore have a Road level and a
		// separate Tower level.
		if pkt.Skill < 0 || pkt.Skill >= 64 || pkt.Cost <= 0 {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if _, err := s.server.towerRepo.GetTowerData(s.charID); err != nil {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if err := towerLearnSkill(s, int(pkt.Skill), pkt.Unk1 == towerStatusLearnTag, pkt.Cost); err != nil {
			s.logger.Error("Failed to learn tower skill", zap.Int32("skill", pkt.Skill), zap.Error(err))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
	case 3:
		if pkt.Unk1 == towerStatusLearnTag {
			// Tower Status reset (G10.1 FUN_105d7d90 state 4, one 再覚之古書): the
			// client refunds the learn cost of every Tower skill level and posts the
			// new TSP total as Cost. Refund from the stored levels rather than
			// trusting Cost.
			if err := towerResetSkills(s, pkt.Cost); err != nil {
				s.logger.Error("Failed to reset tower skills", zap.Error(err))
				doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
				return
			}
			break
		}
		// Hunting Road skill reset (a Road reset book). The client clears every
		// level but the Tower-only skills (vorbis.dll keeps those) and refunds
		// Road SP in its save data; Cost is that new Road SP and is not stored
		// here. The Tower-only levels live in tower.skills and stay.
		if err := s.server.towerRepo.UpdateRoadSkills(s.charID, EmptyTowerCSV(64)); err != nil {
			s.logger.Error("Failed to reset road skills", zap.Error(err))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
	case 5:
		// ＴＳＰ変換 (G10.1 FUN_105d7d90 state 6): five 天技之古書 become one TSP
		// and the client posts InfoType 5 with Cost = 1. vorbis.dll tags the Tower
		// Status conversion; the untagged Hunting Road conversion is paid into
		// Road SP in the save data and needs nothing here.
		if pkt.Unk1 != towerStatusLearnTag {
			break
		}
		if pkt.Cost != 1 {
			s.logger.Warn("Tower TSP conversion with unexpected amount", zap.Int32("cost", pkt.Cost))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if _, err := s.server.towerRepo.GetTowerData(s.charID); err != nil {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if err := s.server.towerRepo.AddTSP(s.charID, pkt.Cost); err != nil {
			s.logger.Error("Failed to convert TSP", zap.Error(err))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
	case 6:
		// G10 also sends IT6 at milestone departure, before any fight/result.
		// A consumed milestone is independent of floors climbed and daily credit.
		if handled, err := s.postTowerMilestone(pkt); handled {
			if err != nil {
				s.logger.Warn("Rejected Tower milestone departure", zap.Error(err))
				doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
				return
			}
			break
		}
		if !hasDeparture {
			break
		}
		// Floor record from the ZZ client (cComm_PostTowerInfo, verified against
		// the client on 2026-09-24): the quest-result step sends it only when the
		// run beat the block record it received from GetTowerInfo. Unk6 is the
		// block (1 or 2) and Block1 the new best floor; Unk1 is always 1. Ignoring
		// it left tower.block1/block2 NULL, so every departure restarted at floor 1.
		if pkt.Unk6 < 1 || pkt.Unk6 > 2 || pkt.Block1 <= 0 || pkt.Block1 > maxTowerFloorReport {
			s.logger.Warn("Tower floor report out of range, not recorded", zap.Int32("block", pkt.Unk6), zap.Int32("floors", pkt.Block1))
			break
		}
		if s.server.erupeConfig.TowerRotation.Enabled {
			// Native IT6 reports an absolute record, not a run delta. Defer the
			// write to IT7/the investigation's atomic settlement, otherwise the
			// same floors would be added again by the report-only fallback.
			break
		}
		if _, err := s.server.towerRepo.GetTowerData(s.charID); err != nil {
			s.logger.Error("Failed to initialize tower data for floor report", zap.Error(err))
			break
		}
		if err := s.towerRoundRepo(resultEvent.ID).UpdateBlockFloors(s.charID, uint8(pkt.Unk6), pkt.Block1); err != nil {
			s.logger.Error("Failed to save tower floor report", zap.Error(err))
			break
		}
		s.lifecycleMu.Lock()
		s.towerMissionBlock = uint8(pkt.Unk6)
		s.towerMissionDayStart = towerDailyStart(TimeAdjusted())
		if s.server.erupeConfig.TowerRotation.Enabled {
			s.towerMissionDayStart = towerDailyStart(s.towerDepartureStarted)
		}
		s.lifecycleMu.Unlock()
		s.towerMissionSubmissionReady.Store(true)
	case 1, 7:
		if !hasDeparture {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		// Tower progress from the quest-clear flow. The ZZ client builds InfoType 7
		// in FUN_10b77aa0 (verified 2026-09-26): TR is the new Tower Rank, TRP and
		// Cost (TSP) are what the run added, Unk6 is the tower block (1-4) and
		// Block1 the floors climbed. The floors go to that block's column (blocks 3
		// and 4 have none) and the rank is never lowered.
		block := towerProgressBlock(pkt)
		s.lifecycleMu.Lock()
		v := TowerSettlement{Block: block, TR: pkt.TR, TRP: pkt.TRP, TSP: pkt.Cost, Floors: pkt.Block1}
		if s.questWeaponGeneration == 0 || !v.valid() {
			s.lifecycleMu.Unlock()
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if s.towerSettlementGeneration != s.questWeaponGeneration {
			s.towerSettlementRunID = newTowerGuardianRunID()
			s.towerSettlementGeneration = s.questWeaponGeneration
			s.towerMissionDayStart = towerDailyStart(TimeAdjusted())
			if s.server.erupeConfig.TowerRotation.Enabled {
				s.towerMissionDayStart = towerDailyStart(s.towerDepartureStarted)
			}
		}
		// Create the legacy row if needed; never consume the generation on failure.
		if _, err := s.server.towerRepo.GetTowerData(s.charID); err != nil {
			s.lifecycleMu.Unlock()
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if _, err := s.towerRoundRepo(resultEvent.ID).SettleTowerRun(s.charID, s.towerSettlementRunID, resultEvent.ID, s.towerMissionDayStart, v); err != nil {
			s.lifecycleMu.Unlock()
			s.logger.Error("Failed to commit tower settlement", zap.Error(err))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		fresh := s.towerProgressGeneration != s.questWeaponGeneration
		s.towerProgressGeneration = s.questWeaponGeneration
		s.towerMissionBlock = block

		s.lifecycleMu.Unlock()
		if fresh && pkt.Block1 > 0 {
			s.towerMissionSubmissionReady.Store(true)
		}
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

// towerQuestBlock maps normal investigations to the block they advance.
// The prologue is practice: G10 FUN_107b5520 explicitly adds zero floors.
// Its investigation counters must not become permanent floor progress.
func towerQuestBlock(questID uint16) uint8 {
	switch questID {
	case towerQuestZone1:
		return 1
	case towerQuestZone2:
		return 2
	}
	return 0
}

// towerRecordReportedFloors credits the floors of a Tower run whose quest-clear
// InfoType 7 never arrived. The ZZ quest-end state machine sends Tower quests
// past the Tower result states (no InfoType 7, and no InfoType 6 because the
// client's reached floor is never raised), but it always posts this
// investigation report with the floors climbed. This compatibility fallback is
// only for normal investigations, never the practice prologue. Like InfoType 7,
// it adds this run's floors to the block's reached floor, never lowering it.
func towerRecordReportedFloors(s *Session, stats TowerMissionStats, dayStart time.Time) {
	if stats.Floors == 0 {
		return
	}
	questID := uint16(s.activeQuestID.Load())
	if questID == 0 {
		questID = uint16(s.questRunState.Load())
	}
	block := towerQuestBlock(questID)
	if block == 0 {
		return
	}
	event, valid := s.towerResultEvent()
	if !valid {
		return
	}
	td, err := s.server.towerRepo.GetTowerData(s.charID)
	if err != nil {
		s.logger.Error("Failed to read tower data for reported floors", zap.Error(err))
		return
	}
	if s.server.erupeConfig.TowerRotation.Enabled {
		s.lifecycleMu.Lock()
		if s.towerSettlementGeneration != s.questWeaponGeneration {
			s.towerSettlementGeneration = s.questWeaponGeneration
			s.towerSettlementRunID = newTowerGuardianRunID()
		}
		runID := s.towerSettlementRunID
		s.lifecycleMu.Unlock()
		_, err := s.towerRoundRepo(event.ID).SettleTowerRun(s.charID, runID, event.ID, dayStart,
			TowerSettlement{Block: block, TR: td.TR, Floors: int32(stats.Floors)})
		if err != nil {
			s.logger.Error("Failed to commit reported Tower floors", zap.Error(err))
		}
		return
	}
	reached := td.Block1
	if block == 2 {
		reached = td.Block2
	}
	gain := towerFloorGain(reached, int32(stats.Floors))
	if gain == 0 {
		return
	}
	if err := s.server.towerRepo.UpdateBlockFloors(s.charID, block, reached+gain); err != nil {
		s.logger.Error("Failed to save reported tower floors", zap.Error(err))
		return
	}
	run := TowerMissionStats{Floors: uint16(gain), TRP: stats.TRP}
	if err := s.server.towerRepo.RecordTowerRun(s.server.erupeConfig.EarthID, s.charID, block, dayStart, run); err != nil {
		s.logger.Error("Failed to save reported Tower run", zap.Error(err))
	}
	s.lifecycleMu.Lock()
	s.towerMissionBlock = block
	s.lifecycleMu.Unlock()
	s.logger.Info("Tower floors recorded from the investigation report", zap.Uint32("charID", s.charID),
		zap.Uint16("questID", questID), zap.Uint8("block", block), zap.Int32("reached", reached+gain))
}

// claimTowerMissionSubmission reports whether the current quest departure may
// still record a guild investigation (tenrouirai) submission, and claims it.
// The ZZ client reports floors through MsgMhfPostTowerInfo InfoType 6 only when
// a block record improves, so readiness cannot wait for a progress packet: one
// submission is accepted per quest departure (session generation), plus one
// whenever a progress or floor packet explicitly armed towerMissionSubmissionReady.
func (s *Session) claimTowerMissionSubmission() bool {
	s.lifecycleMu.Lock()
	fresh := s.questWeaponGeneration != 0 && s.towerMissionGeneration != s.questWeaponGeneration
	if fresh {
		s.towerMissionGeneration = s.questWeaponGeneration
	}
	s.lifecycleMu.Unlock()
	armed := s.towerMissionSubmissionReady.Swap(false)
	return armed || fresh
}

// Default missions
var tenrouiraiData = []TenrouiraiData{
	{1, 1, 80, 0, 2, 2, 1, 1, 2, 2},
	{1, 4, 16, 0, 2, 2, 1, 1, 2, 2},
	{1, 6, 50, 0, 2, 2, 1, 0, 2, 2},
	{1, 4, 12, 50, 2, 2, 1, 1, 2, 2},
	{1, 3, 50, 0, 2, 2, 1, 1, 2, 2},
	{2, 5, 40000, 0, 2, 2, 1, 0, 2, 2},
	{1, 5, 50000, 50, 2, 2, 1, 1, 2, 2},
	{2, 1, 60, 0, 2, 2, 1, 1, 2, 2},
	{2, 3, 50, 0, 2, 1, 1, 0, 1, 2},
	{2, 3, 40, 50, 2, 1, 1, 1, 1, 2},
	{2, 4, 12, 0, 2, 1, 1, 1, 1, 2},
	{2, 6, 40, 0, 2, 1, 1, 0, 1, 2},
	{1, 1, 60, 50, 2, 1, 2, 1, 1, 2},
	{1, 5, 50000, 0, 3, 1, 2, 1, 1, 2},
	{1, 6, 50, 0, 3, 1, 2, 0, 1, 2},
	{1, 4, 16, 50, 3, 1, 2, 1, 1, 2},
	{1, 5, 50000, 0, 3, 1, 2, 1, 1, 2},
	{2, 3, 40, 0, 3, 1, 2, 0, 1, 2},
	{1, 3, 50, 50, 3, 1, 2, 1, 1, 2},
	{2, 5, 40000, 0, 3, 1, 2, 1, 1, 1},
	{2, 6, 40, 0, 3, 1, 2, 0, 1, 1},
	{2, 1, 60, 50, 3, 1, 2, 1, 1, 1},
	{2, 6, 50, 0, 3, 1, 2, 1, 1, 1},
	{2, 4, 12, 0, 3, 1, 2, 0, 1, 1},
	{1, 1, 80, 50, 3, 1, 2, 1, 1, 1},
	{1, 5, 40000, 0, 3, 1, 2, 1, 1, 1},
	{1, 3, 50, 0, 3, 1, 2, 0, 1, 1},
	{1, 4, 16, 50, 3, 1, 0, 1, 1, 1},
	{1, 6, 50, 0, 3, 1, 0, 1, 1, 1},
	{2, 3, 40, 0, 3, 1, 0, 1, 1, 1},
	{1, 1, 80, 50, 3, 1, 0, 0, 1, 1},
	{2, 5, 40000, 0, 3, 1, 0, 0, 1, 1},
	{2, 6, 40, 0, 3, 1, 0, 0, 1, 1},
}

// TenrouiraiProgress represents Tenrouirai (sky corridor) progress.
type TenrouiraiProgress struct {
	Page     uint8
	Mission1 uint16
	Mission2 uint16
	Mission3 uint16
}

// TenrouiraiReward represents a Tenrouirai reward.
type TenrouiraiReward struct {
	Index    uint8
	Item     []uint16 // 5
	Quantity []uint8  // 5
}

// TenrouiraiKeyScore represents a Tenrouirai key score.
type TenrouiraiKeyScore struct {
	Unk0 uint8
	Unk1 int32
}

// TenrouiraiData represents Tenrouirai data.
type TenrouiraiData struct {
	Block   uint8
	Mission uint8
	// 1 = Floors climbed
	// 2 = Collect antiques
	// 3 = Open chests
	// 4 = Cats saved
	// 5 = TRP acquisition
	// 6 = Monster slays
	Goal   uint16
	Cost   uint16
	Skill1 uint8 // 80
	Skill2 uint8 // 40
	Skill3 uint8 // 40
	Skill4 uint8 // 20
	Skill5 uint8 // 40
	Skill6 uint8 // 50
}

// TenrouiraiCharScore represents a Tenrouirai per-character score.
type TenrouiraiCharScore struct {
	Score int32
	Name  string
}

// TenrouiraiTicket represents a Tenrouirai ticket entry.
type TenrouiraiTicket struct {
	Unk0 uint8
	RP   uint32
	Unk2 uint32
}

// Tenrouirai represents complete Tenrouirai data.
type Tenrouirai struct {
	Progress  []TenrouiraiProgress
	Reward    []TenrouiraiReward
	KeyScore  []TenrouiraiKeyScore
	Data      []TenrouiraiData
	CharScore []TenrouiraiCharScore
	Ticket    []TenrouiraiTicket
}

func handleMsgMhfGetTenrouirai(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetTenrouirai)
	event, err := s.server.towerEvent(TimeAdjusted())
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	var data []*byteframe.ByteFrame

	tenrouirai := Tenrouirai{
		Progress: []TenrouiraiProgress{{1, 0, 0, 0}},
		Reward:   towerGuildRewards,
		Data:     tenrouiraiData,
		Ticket:   []TenrouiraiTicket{{0, 0, 0}},
	}

	switch pkt.DataType {
	case 1:
		for _, tdata := range tenrouirai.Data {
			bf := byteframe.NewByteFrame()
			bf.WriteUint8(tdata.Block)
			bf.WriteUint8(tdata.Mission)
			bf.WriteUint16(tdata.Goal)
			bf.WriteUint16(tdata.Cost)
			bf.WriteUint8(tdata.Skill1)
			bf.WriteUint8(tdata.Skill2)
			bf.WriteUint8(tdata.Skill3)
			bf.WriteUint8(tdata.Skill4)
			bf.WriteUint8(tdata.Skill5)
			bf.WriteUint8(tdata.Skill6)
			data = append(data, bf)
		}
	case 2:
		for _, reward := range tenrouirai.Reward {
			bf := byteframe.NewByteFrame()
			bf.WriteUint8(reward.Index)
			for i := 0; i < 5; i++ {
				if i < len(reward.Item) {
					bf.WriteUint16(reward.Item[i])
				} else {
					bf.WriteUint16(0)
				}
			}
			for i := 0; i < 5; i++ {
				if i < len(reward.Quantity) {
					bf.WriteUint8(reward.Quantity[i])
				} else {
					bf.WriteUint8(0)
				}
			}
			data = append(data, bf)
		}
	case 4:
		progress, err := s.server.towerService.GetTenrouiraiProgressCapped(pkt.GuildID)
		if err != nil {
			s.logger.Error("Failed to read tower mission page", zap.Error(err))
		} else {
			tenrouirai.Progress[0].Page = progress.Page
			tenrouirai.Progress[0].Mission1 = progress.Mission1
			tenrouirai.Progress[0].Mission2 = progress.Mission2
			tenrouirai.Progress[0].Mission3 = progress.Mission3
		}

		for _, progress := range tenrouirai.Progress {
			bf := byteframe.NewByteFrame()
			bf.WriteUint8(progress.Page)
			bf.WriteUint16(progress.Mission1)
			bf.WriteUint16(progress.Mission2)
			bf.WriteUint16(progress.Mission3)
			data = append(data, bf)
		}
	case 5:
		scores, err := s.server.towerRepo.GetTenrouiraiMissionScores(pkt.GuildID, pkt.MissionIndex)
		if err != nil {
			s.logger.Error("Failed to query tower mission scores", zap.Error(err))
		}
		for _, charScore := range scores {
			bf := byteframe.NewByteFrame()
			bf.WriteInt32(charScore.Score)
			bf.WriteBytes(stringsupport.PaddedString(charScore.Name, 14, true))
			data = append(data, bf)
		}
	case 6:
		rp, _ := s.server.towerRepo.GetGuildTowerRP(pkt.GuildID)
		tenrouirai.Ticket[0].RP = rp
		for _, ticket := range tenrouirai.Ticket {
			bf := byteframe.NewByteFrame()
			bf.WriteUint8(ticket.Unk0)
			bf.WriteUint32(ticket.RP)
			bf.WriteUint32(ticket.Unk2)
			data = append(data, bf)
		}
	}

	doAckTowerSucceed(s, pkt.AckHandle, event.ID, data)
}

func handleMsgMhfPostTenrouirai(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfPostTenrouirai)
	event, eventErr := s.server.towerEvent(TimeAdjusted())
	if eventErr != nil {
		if pkt.Op == 1 {
			doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
		} else {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		}
		return
	}

	if s.server.erupeConfig.DebugOptions.QuestTools {
		s.logger.Debug(
			p.Opcode().String(),
			zap.Uint8("Unk0", pkt.Unk0),
			zap.Uint8("Op", pkt.Op),
			zap.Uint32("GuildID", pkt.GuildID),
			zap.Uint8("Unk1", pkt.Unk1),
			zap.Uint16("Floors", pkt.Floors),
			zap.Uint16("Antiques", pkt.Antiques),
			zap.Uint16("Chests", pkt.Chests),
			zap.Uint16("Cats", pkt.Cats),
			zap.Uint16("TRP", pkt.TRP),
			zap.Uint16("Slays", pkt.Slays),
		)
	}

	if pkt.Op == 1 {
		resultEvent, valid := s.towerResultEvent()
		if !valid {
			doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		event = resultEvent
		// The client posts this from its quest-end state machine (state 0x9b) and
		// only proceeds on a success ack: a failure ack leaves it polling forever
		// on a black screen (observed 2026-09-24 after abandoning a tower quest).
		// The official server never failed this message, so every rejection below
		// answers success and simply does not record progress. One submission is
		// accepted per quest departure; see claimTowerMissionSubmission.
		if !s.claimTowerMissionSubmission() {
			s.logger.Debug("Tower investigation submission ignored: already recorded for this departure")
			doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		stats := TowerMissionStats{Floors: pkt.Floors, Antiques: pkt.Antiques, Chests: pkt.Chests, Cats: pkt.Cats, TRP: pkt.TRP, Slays: pkt.Slays}
		if !stats.Valid() {
			s.logger.Warn("Tower investigation counters out of range, not recorded")
			doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		// The quest-clear flow normally posts TR, TRP and TSP itself (InfoType 7,
		// verified 2026-09-26). Only when that did not reach us for this departure
		// is the Tower Rank kept from this report's TRP, so a run never counts twice.
		s.lifecycleMu.Lock()
		progressPosted := s.questWeaponGeneration != 0 && s.towerProgressGeneration == s.questWeaponGeneration
		progressAttempted := s.questWeaponGeneration != 0 && s.towerSettlementGeneration == s.questWeaponGeneration
		s.lifecycleMu.Unlock()
		if progressAttempted && !progressPosted {
			// The report-only compatibility path must not partially save a failed
			// atomic result, then add those floors again on a later IT7 retry.
			s.logger.Warn("Tower investigation ignored after an uncommitted result")
			doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if perRank := s.server.erupeConfig.TowerRankTRPPerRank; perRank > 0 && stats.TRP > 0 && !progressPosted {
			if _, err := s.server.towerRepo.AddTowerRankPoints(s.charID, int32(stats.TRP), perRank, s.server.erupeConfig.TowerTSPPerRank); err != nil {
				s.logger.Error("Failed to credit tower rank points", zap.Error(err))
			}
		}
		// claimTowerMissionSubmission above already consumed the armed flag (one
		// submission per departure). A run that did not post a floor record has no
		// recorded day yet; it ended just now, so count it against today.
		s.lifecycleMu.Lock()
		dayStart := s.towerMissionDayStart
		s.lifecycleMu.Unlock()
		if dayStart.IsZero() {
			dayStart = towerDailyStart(TimeAdjusted())
			if s.server.erupeConfig.TowerRotation.Enabled {
				s.lifecycleMu.Lock()
				dayStart = towerDailyStart(s.towerDepartureStarted)
				s.lifecycleMu.Unlock()
			}
		}
		var dailyErr error
		if r, ok := s.server.towerRepo.(TowerDailyExtrasRepository); ok && s.server.erupeConfig.TowerRotation.Enabled {
			dailyErr = r.RecordTowerDailyExtrasWithTRP(event.ID, s.charID, dayStart, stats, !progressPosted)
		} else {
			dailyErr = s.server.towerRepo.RecordTowerDailyExtras(event.ID, s.charID, dayStart, stats)
		}
		if dailyErr != nil {
			s.logger.Error("Failed to save Tower daily bonus counters", zap.Error(dailyErr))
		}
		if !progressPosted {
			towerRecordReportedFloors(s, stats, dayStart)
		}
		guildID, reason, err := resolveGuildMemberAccess(s, pkt.GuildID)
		if err != nil || reason != "" {
			s.logger.Debug("Tower guild investigation not recorded", zap.Error(err), zap.String("reason", reason))
			doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if err := s.towerRoundRepo(event.ID).SubmitTenrouiraiProgress(guildID, s.charID, stats); err != nil {
			s.logger.Error("Failed to save tower investigation progress", zap.Error(err))
			doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
	} else if pkt.Op == 2 {
		if s.server.erupeConfig.TowerRotation.Enabled && !event.Active(TimeAdjusted()) {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		bf := byteframe.NewByteFrame()
		if pkt.DonatedRP == 0 {
			bf.WriteUint32(0)
			doAckSimpleFail(s, pkt.AckHandle, bf.Data())
			return
		}
		guildID, reason, lookupErr := resolveGuildMemberAccess(s, pkt.GuildID)
		if lookupErr != nil {
			s.logger.Error("Failed to establish guild membership for tower donation", zap.Error(lookupErr))
			bf.WriteUint32(0)
			doAckSimpleFail(s, pkt.AckHandle, bf.Data())
			return
		}
		if reason != "" {
			s.recordSecurityAudit("unauthorized_guild_point_spend", "warning", "rejected", map[string]interface{}{
				"point_kind": "rp", "requested_guild_id": pkt.GuildID, "reason": reason,
			})
			bf.WriteUint32(0)
			doAckSimpleFail(s, pkt.AckHandle, bf.Data())
			return
		}

		sd, err := GetCharacterSaveData(s, s.charID)
		if err != nil || sd == nil || sd.RP < pkt.DonatedRP {
			bf.WriteUint32(0)
			doAckSimpleFail(s, pkt.AckHandle, bf.Data())
			return
		}
		service := s.server.towerService
		if s.server.erupeConfig.TowerRotation.Enabled {
			service = NewTowerService(s.towerRoundRepo(event.ID), s.logger)
		}
		result, err := service.DonateGuildTowerRP(guildID, pkt.DonatedRP)
		if err != nil {
			s.logger.Error("Failed to process tower RP donation", zap.Error(err))
			bf.WriteUint32(0)
			doAckSimpleFail(s, pkt.AckHandle, bf.Data())
			return
		}
		// A page may need fewer points than requested. Never deduct the
		// entire request or allow uint16 underflow in the character save.
		sd.RP -= result.ActualDonated
		if err := sd.Save(s); err != nil {
			s.logger.Error("Failed to save RP after tower donation", zap.Error(err))
			bf.WriteUint32(0)
			doAckSimpleFail(s, pkt.AckHandle, bf.Data())
			return
		}
		bf.WriteUint32(uint32(result.ActualDonated))

		doAckSimpleSucceed(s, pkt.AckHandle, bf.Data())
	} else {
		doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
	}
}

func handleMsgMhfPresentBox(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfPresentBox)
	handleTowerPresentBox(s, pkt)
}

// GemInfo represents gem (decoration) info.
type GemInfo struct {
	Gem      uint16
	Quantity uint16
}

// GemHistory represents gem usage history.
type GemHistory struct {
	ID        int64
	Gem       uint16
	Message   uint16
	Timestamp time.Time
	Sender    string
}

func handleMsgMhfGetGemInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetGemInfo)
	event, err := s.server.towerEvent(TimeAdjusted())
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	var data []*byteframe.ByteFrame
	gemInfo := []GemInfo{}
	gemHistory := []GemHistory{}

	tempGems, err := s.server.towerRepo.GetGems(s.charID)
	if err != nil && pkt.QueryType == 1 {
		s.logger.Error("Failed to read ancient treasures", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	for i, v := range stringsupport.CSVElems(tempGems) {
		if i >= 30 {
			break
		}
		if v < 0 || v > math.MaxUint16 {
			continue
		}
		gemInfo = append(gemInfo, GemInfo{towerGemID(i), uint16(v)})
	}

	switch pkt.QueryType {
	case 1:
		for _, info := range gemInfo {
			bf := byteframe.NewByteFrame()
			bf.WriteUint16(info.Gem)
			bf.WriteUint16(info.Quantity)
			data = append(data, bf)
		}
	case 2:
		gemHistory, err = s.server.towerRepo.GetGemHistory(s.charID)
		if err == nil {
			for _, h := range gemHistory {
				if h.ID > s.towerGemNoticeThrough {
					s.towerGemNoticeThrough = h.ID
				}
			}
		}
		if err != nil {
			s.logger.Error("Failed to read ancient treasure gift history", zap.Error(err))
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		for _, history := range gemHistory {
			bf := byteframe.NewByteFrame()
			bf.WriteUint16(history.Gem)
			bf.WriteUint16(history.Message)
			bf.WriteUint32(uint32(history.Timestamp.Unix()))
			bf.WriteBytes(stringsupport.PaddedString(history.Sender, 14, true))
			data = append(data, bf)
		}
	}
	doAckTowerSucceed(s, pkt.AckHandle, event.ID, data)
}

func handleMsgMhfPostGemInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfPostGemInfo)
	event, eventErr := s.server.towerEvent(TimeAdjusted())
	if eventErr != nil {
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	repo := s.towerRoundRepo(event.ID)

	if s.server.erupeConfig.DebugOptions.QuestTools {
		s.logger.Debug(
			p.Opcode().String(),
			zap.Uint32("Op", pkt.Op),
			zap.Uint32("Unk1", pkt.Unk1),
			zap.Int32("Gem", pkt.Gem),
			zap.Int32("Quantity", pkt.Quantity),
			zap.Int32("CID", pkt.CID),
			zap.Int32("Message", pkt.Message),
			zap.Int32("Unk6", pkt.Unk6),
		)
	}

	switch pkt.Op {
	case 1: // Add gem
		// Active msx types are 1,2,3,4,8; the last CSV slot is the candlestick.
		i, valid := towerGemIndex(pkt.Gem)
		if !valid || pkt.Quantity <= 0 {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		var err error
		if s.server.erupeConfig.TowerRotation.Enabled {
			resultEvent, valid := s.towerResultEvent()
			if !valid {
				doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
				return
			}
			repo = s.towerRoundRepo(resultEvent.ID)
		}
		if pkt.Unk1 == 0xd463 {
			s.lifecycleMu.Lock()
			if s.towerSettlementRunID == "" || s.towerProgressGeneration != s.questWeaponGeneration || s.questWeaponGeneration == 0 {
				s.lifecycleMu.Unlock()
				doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
				return
			}
			err = repo.DepositTowerGem(s.charID, s.towerSettlementRunID, pkt.Unk6, pkt.Gem, pkt.Quantity)
			s.lifecycleMu.Unlock()
		} else {
			service := s.server.towerService
			if s.server.erupeConfig.TowerRotation.Enabled {
				service = NewTowerService(repo, s.logger)
			}
			err = service.AddGem(s.charID, i, int(pkt.Quantity))
		}
		if err != nil {
			s.logger.Error("Failed to update tower gems", zap.Error(err))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
	case 2: // Transfer gem
		if pkt.CID <= 0 || pkt.Quantity != 1 || pkt.Message < 0 || pkt.Message > math.MaxUint16 ||
			pkt.Gem < 0 || pkt.Gem > math.MaxUint16 {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if err := repo.TransferGem(s.charID, uint32(pkt.CID), uint16(pkt.Gem), uint16(pkt.Message)); err != nil {
			s.logger.Warn("Rejected ancient treasure gift", zap.Error(err), zap.Uint32("sender", s.charID), zap.Int32("receiver", pkt.CID))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
	default:
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

// G10 antiques use notice key (1,0,1); unrelated notice keys retain their empty reply.
func handleMsgMhfGetNotice(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetNotice)
	bf := byteframe.NewByteFrame()
	value := uint32(0)
	if pkt.Unk0 == 1 && pkt.Unk1 == 0 && pkt.Unk2 == 1 {
		latest, seen, err := s.server.towerRepo.GetGemNotice(s.charID)
		if err != nil {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if latest > seen {
			value = 1
		}
	}
	bf.WriteUint32(value)
	doAckSimpleSucceed(s, pkt.AckHandle, bf.Data())
}
func handleMsgMhfPostNotice(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfPostNotice)
	if pkt.Unk0 == 1 && pkt.Unk1 == 0 && pkt.Unk2 == 1 {
		// Clear only history actually returned to this session. A concurrent gift remains unread.
		if pkt.Unk3 != 0 || s.server.towerRepo.ReadGemNotice(s.charID, s.towerGemNoticeThrough) != nil {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

// towerProgressBlock returns the tower block (1-4) a progress post belongs to.
// The ZZ client names it in Unk6; without one, InfoType 7 meant block 2.
func towerProgressBlock(pkt *mhfpacket.MsgMhfPostTowerInfo) uint8 {
	if pkt.Unk6 >= 1 && pkt.Unk6 <= 4 {
		return uint8(pkt.Unk6)
	}
	if pkt.InfoType == 7 {
		return 2
	}
	return 1
}

// towerStatusLearnTag is the Unk1 value vorbis.dll puts on an InfoType 2 skill
// learn made from the Tower Status screen, the only learn paid with TSP.
const towerStatusLearnTag = 0x5453

// isTowerOnlySkill reports whether id is one of the G10 Tower skills ZZ dropped
// from its Hunting Road skill table. Only the Tower Status learns them, so a
// Hunting Road reset leaves them alone.
func isTowerOnlySkill(id int) bool {
	switch id {
	case 3, 4, 6, 7, 8, 9, 10, 11, 12, 13, 16, 17, 21:
		return true
	}
	return false
}

// towerTSPMax is the client's TSP ceiling (G10.1 FUN_105d7630 clamps to it).
const towerTSPMax = 99999999

// towerSkillLearnCosts is the TSP cost of each level of the 22 G10 Tower
// skills (G10.1 mhfdat skill table at 0x98ee78, record +0xC), indexed by skill
// id then level-1.
var towerSkillLearnCosts = map[int][]int32{
	1: {1, 1, 2, 3, 5}, 2: {1, 1, 2, 3, 5}, 3: {1}, 4: {3, 7}, 5: {3, 7},
	6: {1, 2, 4, 5, 7}, 7: {1, 2, 4, 5, 7}, 8: {1, 2, 4, 5, 7}, 9: {1, 2, 4, 5, 7},
	10: {1, 3}, 11: {1, 3, 5}, 12: {1, 2, 5, 6, 8}, 13: {1, 3, 5},
	14: {2, 5, 10}, 15: {1, 3, 5}, 16: {2, 4, 7}, 17: {3, 5, 8},
	18: {2, 5, 10}, 19: {2, 3, 4, 5, 8}, 20: {2, 3, 4, 5, 8}, 21: {5}, 22: {2, 5, 10},
}

// towerCommonSkills are the nine skills both the G10 Tower and the ZZ Hunting
// Road sets contain. Their Tower levels travel in the InfoType 2 reply at
// towerCommonSlotBase+k, slots no ZZ skill uses; vorbis.dll moves them out of
// the client's level array before anything reads it.
var towerCommonSkills = [...]int{1, 2, 5, 14, 15, 18, 19, 20, 22}

const towerCommonSlotBase = 40

// isTowerStatusSkill reports whether id is one of the 22 G10 Tower skills.
func isTowerStatusSkill(id int) bool {
	return id >= 1 && id <= 22
}

// towerSkillRefund is what a Tower Status reset returns: the learn cost of every
// Tower skill level held (G10.1 FUN_105d79d0).
func towerSkillRefund(skills string) int32 {
	var refund int32
	for id, level := range stringsupport.CSVElems(skills) {
		costs := towerSkillLearnCosts[id]
		for l := 0; l < level && l < len(costs); l++ {
			refund += costs[l]
		}
	}
	return refund
}

// towerResetSkills clears the Tower-only skill levels and refunds their TSP.
func towerResetSkills(s *Session, clientTSP int32) error {
	td, err := s.server.towerRepo.GetTowerData(s.charID)
	if err != nil {
		return err
	}
	skills, err := s.server.towerRepo.GetSkills(s.charID)
	if err != nil {
		return err
	}
	refund := towerSkillRefund(skills)
	if clientTSP != td.TSP+refund {
		s.logger.Warn("Tower skill reset TSP differs from the client",
			zap.Int32("client", clientTSP), zap.Int32("server", td.TSP+refund))
	}
	return s.server.towerRepo.ResetTowerSkills(s.charID, refund)
}

// towerMergeSkills builds the client's single 64-entry skill-level array from
// the two stores: Tower-only skills from tower.skills, the Road levels of every
// other skill from road_skills, and the Tower levels of the common skills in
// the spare slots towerCommonSlotBase+k.
func towerMergeSkills(tower, road string) []int {
	t := stringsupport.CSVElems(tower)
	r := stringsupport.CSVElems(road)
	out := make([]int, 64)
	for i := 0; i < towerCommonSlotBase; i++ {
		if isTowerOnlySkill(i) {
			if i < len(t) {
				out[i] = t[i]
			}
		} else if i < len(r) {
			out[i] = r[i]
		}
	}
	for k, id := range towerCommonSkills {
		if id < len(t) {
			out[towerCommonSlotBase+k] = t[id]
		}
	}
	return out
}

// towerLearnSkill raises one skill level in its own store. A Tower Status
// learn pays cost TSP and goes to tower.skills (any of the 22 G10 Tower
// skills, the common ones included); a Hunting Road learn goes to road_skills
// and costs nothing here. The Road screen has no Tower-only skills, so an
// untagged Tower-only learn only keeps the level.
func towerLearnSkill(s *Session, id int, towerStatus bool, cost int32) error {
	skills, err := s.server.towerRepo.GetSkills(s.charID)
	if err != nil {
		return err
	}
	if len(stringsupport.CSVElems(skills)) != 64 {
		return fmt.Errorf("tower skills have %d entries", len(stringsupport.CSVElems(skills)))
	}
	if towerStatus || isTowerOnlySkill(id) {
		if towerStatus && !isTowerStatusSkill(id) {
			return fmt.Errorf("skill %d is not a Tower skill", id)
		}
		if !towerStatus {
			cost = 0
		}
		return s.server.towerRepo.UpdateSkills(s.charID,
			stringsupport.CSVSetIndex(skills, id, stringsupport.CSVGetIndex(skills, id)+1), cost)
	}
	road, err := s.server.towerRepo.GetRoadSkills(s.charID)
	if err != nil {
		return err
	}
	if len(stringsupport.CSVElems(road)) != 64 {
		return fmt.Errorf("road skills have %d entries", len(stringsupport.CSVElems(road)))
	}
	return s.server.towerRepo.UpdateRoadSkills(s.charID,
		stringsupport.CSVSetIndex(road, id, stringsupport.CSVGetIndex(road, id)+1))
}
