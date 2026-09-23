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

// maxTowerFloorReport bounds the floor a client may report through
// MsgMhfPostTowerInfo InfoType 6. The tower never came close to this many
// floors, so anything larger is treated as a corrupt packet and ignored.
const maxTowerFloorReport = 1000

// Tower zone departures the receptionist lists per character. The prologue
// (21729, map 71, one tutorial floor) is always offered; zone 1 (21732, map 71)
// and zone 2 (21733, map 73) wait for the block-1 floor record configured in
// TowerZone1UnlockFloor / TowerZone2UnlockFloor. The client has no list-side
// rule of its own (the G10.1 departure list was "category 0x43 quest records
// the server enumerated"), so the server decides what each hunter sees.
const (
	towerQuestZone1     = 21732
	towerQuestZone2     = 21733
	towerQuestGuardian1 = 21731 // 緊急調査依頼: 20-minute Guardian arena of block 1 (map 72)
	towerQuestGuardian2 = 21746 // 緊急調査依頼: 20-minute Guardian arena of block 2 (map 74)
)

// towerGuardianFloor reports whether a block floor record sits exactly on a
// Guardian milestone: floor 10, every multiple of 40, and floor 500 (paper rows
// 1104/1105; the client ends a run on these floors). The 天廊遠征録 rule was that
// the 緊急調査依頼 appeared on reaching them and the fight itself was optional,
// so the arena is offered while the hunter stands on the milestone and vanishes
// again once they climb on. Killing it does not need tracking here: the record
// only moves when the hunter continues the climb.
func towerGuardianFloor(record int32) bool {
	return record == 10 || record == 500 || (record >= 40 && record%40 == 0)
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
// listed, the zone climbs wait for the configured block-1 record, and the
// Guardian arenas appear only while the matching block record is on a milestone.
func (s *Session) allowsTowerQuest(eq EventQuest, cache **TowerData) bool {
	switch eq.QuestID {
	case towerQuestZone1:
		need := s.server.erupeConfig.TowerZone1UnlockFloor
		if need <= 0 {
			return true
		}
		td, ok := s.towerDataCached(cache)
		return ok && td.Block1 >= need
	case towerQuestZone2:
		// Official rule: 凄腕 rank and Tower Rank 51. The floor threshold stays as
		// a second, optional knob.
		needFloor, needTR := s.server.erupeConfig.TowerZone2UnlockFloor, s.server.erupeConfig.TowerZone2UnlockTR
		if needFloor <= 0 && needTR <= 0 {
			return true
		}
		td, ok := s.towerDataCached(cache)
		if !ok {
			return false
		}
		return td.Block1 >= needFloor && td.TR >= needTR
	case towerQuestGuardian1, towerQuestGuardian2:
		td, ok := s.towerDataCached(cache)
		if !ok {
			return false
		}
		record := td.Block1
		if eq.QuestID == towerQuestGuardian2 {
			record = td.Block2
		}
		return towerGuardianFloor(record)
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
	round := int32(towerSurveyRound(s.server.erupeConfig.EarthID))
	floors, err := s.server.towerRepo.GetTowerSurveyHistory(s.server.erupeConfig.EarthID, s.charID)
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
	} else {
		towerInfo.TRP[0].TR = td.TR
		towerInfo.TRP[0].TRP = td.TRP
		towerInfo.Skill[0].TSP = td.TSP
		towerInfo.Level[0].Floors = td.Block1
		towerInfo.Level[1].Floors = td.Block2
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
	doAckEarthSucceed(s, pkt.AckHandle, data)
}

func handleMsgMhfPostTowerInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfPostTowerInfo)

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
		// cost. The two skill sets are stored apart: the thirteen Tower-only
		// skills in tower.skills and every other skill in road_skills. Only a
		// Tower Status learn is paid with TSP (vorbis.dll marks it with Unk1 =
		// towerStatusLearnTag); a Hunting Road learn is paid with Road SP from
		// the save data, so it only raises the level.
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
		// Floor record from the ZZ client (cComm_PostTowerInfo, verified against
		// the client on 2026-09-24): the quest-result step sends it only when the
		// run beat the block record it received from GetTowerInfo. Unk6 is the
		// block (1 or 2) and Block1 the new best floor; Unk1 is always 1. Ignoring
		// it left tower.block1/block2 NULL, so every departure restarted at floor 1.
		if pkt.Unk6 < 1 || pkt.Unk6 > 2 || pkt.Block1 <= 0 || pkt.Block1 > maxTowerFloorReport {
			s.logger.Warn("Tower floor report out of range, not recorded", zap.Int32("block", pkt.Unk6), zap.Int32("floors", pkt.Block1))
			break
		}
		if _, err := s.server.towerRepo.GetTowerData(s.charID); err != nil {
			s.logger.Error("Failed to initialize tower data for floor report", zap.Error(err))
			break
		}
		if err := s.server.towerRepo.UpdateBlockFloors(s.charID, uint8(pkt.Unk6), pkt.Block1); err != nil {
			s.logger.Error("Failed to save tower floor report", zap.Error(err))
			break
		}
		s.lifecycleMu.Lock()
		s.towerMissionBlock = uint8(pkt.Unk6)
		s.towerMissionDayStart = towerDailyStart(TimeAdjusted())
		s.lifecycleMu.Unlock()
		s.towerMissionSubmissionReady.Store(true)
	case 1, 7:
		// Tower progress from the quest-clear flow. The ZZ client builds InfoType 7
		// in FUN_10b77aa0 (verified 2026-09-26): TR is the new Tower Rank, TRP and
		// Cost (TSP) are what the run added, Unk6 is the tower block (1-4) and
		// Block1 the floors climbed. The floors go to that block's column (blocks 3
		// and 4 have none) and the rank is never lowered.
		block := towerProgressBlock(pkt)
		s.lifecycleMu.Lock()
		if s.questWeaponGeneration == 0 || s.towerProgressGeneration == s.questWeaponGeneration ||
			pkt.Block1 < 0 || pkt.Block1 > 4 || pkt.TRP < 0 || pkt.TRP > 50000 {
			s.lifecycleMu.Unlock()
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		td, err := s.server.towerRepo.GetTowerData(s.charID)
		if err != nil {
			s.lifecycleMu.Unlock()
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		tr := pkt.TR
		if tr < td.TR {
			tr = td.TR
		}
		if err := s.server.towerRepo.UpdateProgress(s.charID, tr, pkt.TRP, pkt.Cost, 0); err != nil {
			s.lifecycleMu.Unlock()
			s.logger.Error("Failed to update tower progress", zap.Error(err))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if pkt.Block1 > 0 && (block == 1 || block == 2) {
			reached := td.Block1
			if block == 2 {
				reached = td.Block2
			}
			if err := s.server.towerRepo.UpdateBlockFloors(s.charID, block, reached+pkt.Block1); err != nil {
				s.logger.Error("Failed to save tower floors", zap.Error(err))
			}
		}
		s.towerProgressGeneration = s.questWeaponGeneration
		s.towerMissionBlock = block
		s.towerMissionDayStart = towerDailyStart(TimeAdjusted())
		if pkt.Block1 > 0 && (block == 1 || block == 2) {
			stats := TowerMissionStats{Floors: uint16(pkt.Block1), TRP: uint16(pkt.TRP)}
			if err := s.server.towerRepo.RecordTowerRun(s.server.erupeConfig.EarthID, s.charID,
				block, s.towerMissionDayStart, stats); err != nil {
				s.logger.Error("Failed to save Tower floor and daily progress", zap.Error(err))
			}
		}
		s.lifecycleMu.Unlock()
		if pkt.Block1 > 0 {
			s.towerMissionSubmissionReady.Store(true)
		}
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
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

	doAckEarthSucceed(s, pkt.AckHandle, data)
}

func handleMsgMhfPostTenrouirai(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfPostTenrouirai)

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
		s.lifecycleMu.Unlock()
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
		}
		if err := s.server.towerRepo.RecordTowerDailyExtras(s.server.erupeConfig.EarthID, s.charID, dayStart, stats); err != nil {
			s.logger.Error("Failed to save Tower daily bonus counters", zap.Error(err))
		}
		guildID, reason, err := resolveGuildMemberAccess(s, pkt.GuildID)
		if err != nil || reason != "" {
			s.logger.Debug("Tower guild investigation not recorded", zap.Error(err), zap.String("reason", reason))
			doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if err := s.server.towerRepo.SubmitTenrouiraiProgress(guildID, s.charID, stats); err != nil {
			s.logger.Error("Failed to save tower investigation progress", zap.Error(err))
			doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
	} else if pkt.Op == 2 {
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
		result, err := s.server.towerService.DonateGuildTowerRP(guildID, pkt.DonatedRP)
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
	Gem       uint16
	Message   uint16
	Timestamp time.Time
	Sender    string
}

func handleMsgMhfGetGemInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetGemInfo)
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
		gemInfo = append(gemInfo, GemInfo{uint16((i / 5 << 8) + (i%5 + 1)), uint16(v)})
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
	doAckEarthSucceed(s, pkt.AckHandle, data)
}

func handleMsgMhfPostGemInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfPostGemInfo)

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
		// The protocol encodes six groups of five as (group << 8) | (slot+1).
		group, slot := pkt.Gem>>8, pkt.Gem&0xFF
		if group < 0 || group >= 6 || slot < 1 || slot > 5 || pkt.Quantity <= 0 {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		i := int(group*5 + slot - 1)
		if err := s.server.towerService.AddGem(s.charID, i, int(pkt.Quantity)); err != nil {
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
		if err := s.server.towerRepo.TransferGem(s.charID, uint32(pkt.CID), uint16(pkt.Gem), uint16(pkt.Message)); err != nil {
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

func handleMsgMhfGetNotice(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetNotice)
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgMhfPostNotice(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfPostNotice)
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

// towerSkillLearnCosts is the TSP cost of each level of the Tower-only skills
// (G10.1 mhfdat skill table, record +0xC), indexed by skill id then level-1.
var towerSkillLearnCosts = map[int][]int32{
	3: {1}, 4: {3, 7},
	6: {1, 2, 4, 5, 7}, 7: {1, 2, 4, 5, 7}, 8: {1, 2, 4, 5, 7}, 9: {1, 2, 4, 5, 7},
	10: {1, 3}, 11: {1, 3, 5}, 12: {1, 2, 5, 6, 8}, 13: {1, 3, 5},
	16: {2, 4, 7}, 17: {3, 5, 8}, 21: {5},
}

// towerSkillRefund is what a Tower Status reset returns: the learn cost of every
// Tower-only skill level held (G10.1 FUN_105d79d0).
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

// towerMergeSkills rebuilds the client's single 64-entry skill-level array from
// the two stores: Tower-only skills from tower.skills, every other skill from
// road_skills.
func towerMergeSkills(tower, road string) []int {
	t := stringsupport.CSVElems(tower)
	r := stringsupport.CSVElems(road)
	out := make([]int, 64)
	for i := range out {
		if isTowerOnlySkill(i) {
			if i < len(t) {
				out[i] = t[i]
			}
		} else if i < len(r) {
			out[i] = r[i]
		}
	}
	return out
}

// towerLearnSkill raises one skill level in its own store and, for a Tower
// Status learn, pays cost TSP. A tagged learn of a Road skill (older
// vorbis.dll builds listed the common skills on the Tower Status) is still
// paid with TSP but its level goes to road_skills.
func towerLearnSkill(s *Session, id int, towerStatus bool, cost int32) error {
	if !towerStatus {
		cost = 0
	}
	skills, err := s.server.towerRepo.GetSkills(s.charID)
	if err != nil {
		return err
	}
	if len(stringsupport.CSVElems(skills)) != 64 {
		return fmt.Errorf("tower skills have %d entries", len(stringsupport.CSVElems(skills)))
	}
	if isTowerOnlySkill(id) {
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
	if cost > 0 {
		if err := s.server.towerRepo.UpdateSkills(s.charID, skills, cost); err != nil {
			return err
		}
	}
	return s.server.towerRepo.UpdateRoadSkills(s.charID,
		stringsupport.CSVSetIndex(road, id, stringsupport.CSVGetIndex(road, id)+1))
}
