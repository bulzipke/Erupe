package channelserver

import (
	cfg "erupe-ce/config"
	"fmt"
	"sort"
	"strconv"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

func handleMsgMhfGetUdTacticsPoint(s *Session, p mhfpacket.MHFPacket) {
	// Diva defense interception points
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsPoint)
	if s.server.erupeConfig.RealClientMode == cfg.ZZ {
		handleDivaTacticsPoint(s, pkt)
		return
	}

	pointsMap, err := s.server.divaRepo.GetCharacterInterceptionPoints(s.charID)
	if err != nil {
		s.logger.Warn("Failed to get interception points", zap.Uint32("charID", s.charID), zap.Error(err))
		pointsMap = map[string]int{}
	}

	// Build per-quest list and compute total.
	type questEntry struct {
		questFileID uint32
		points      int
	}
	var entries []questEntry
	var total int
	for k, pts := range pointsMap {
		// ParseUint with bitSize 32 rejects keys that don't fit in a
		// uint32 instead of silently truncating them (unlike Atoi followed
		// by a uint32 conversion), since these are written to the wire as
		// uint32 below.
		qid, err := strconv.ParseUint(k, 10, 32)
		if err != nil {
			continue
		}
		entries = append(entries, questEntry{uint32(qid), pts})
		total += pts
	}

	bf := byteframe.NewByteFrame()
	bf.WriteUint32(uint32(total))
	bf.WriteUint32(uint32(len(entries)))
	for _, e := range entries {
		bf.WriteUint32(e.questFileID)
		bf.WriteUint32(uint32(e.points))
	}

	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

// udTacticsQuestMin/Max bound the interception (Diva Defense) quest file IDs.
// Every ripped 58xxx quest_id in EventQuests.sql (quest_type 46/47/48, see
// isDivaDefenseQuestType in constants_quest.go) falls in 58043-58128; the
// previous 58079-58083 bound only covered one event batch out of 65 rows.
// This range isn't gap-free (a handful of unused IDs in between are also
// accepted), but that's harmless since no real quest ever sends them here.
const (
	udTacticsQuestMin = 58043
	udTacticsQuestMax = 58128
)

func handleMsgMhfAddUdTacticsPoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfAddUdTacticsPoint)
	if s.server.erupeConfig.RealClientMode == cfg.ZZ {
		handleDivaTacticsAdd(s, pkt)
		return
	}
	questFileID := int(pkt.QuestID)
	points := int(pkt.TacticsPoints)

	if questFileID < udTacticsQuestMin || questFileID > udTacticsQuestMax {
		s.logger.Warn("AddUdTacticsPoint: quest file ID out of range",
			zap.Int("questFileID", questFileID),
			zap.String("range", fmt.Sprintf("%d-%d", udTacticsQuestMin, udTacticsQuestMax)))
		doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x00})
		return
	}

	if points > 0 {
		if err := s.server.divaRepo.AddInterceptionPoints(s.charID, questFileID, points); err != nil {
			s.logger.Warn("Failed to add interception points",
				zap.Uint32("charID", s.charID),
				zap.Int("questFileID", questFileID),
				zap.Int("points", points),
				zap.Error(err))
		}
	}

	doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x00})
}

// divaPrizeListMax is the client's per-list table capacity. The retail parser
// fills fixed 512-entry tables (stride 0x10) and performs NO bounds check on
// the count it reads, so an oversized count is a direct global buffer overflow
// inside the client. The server must clamp.
const divaPrizeListMax = 512

// writeDivaPrizeList emits one prize list in the layout the retail client
// expects: u16 count, then 11 bytes per entry.
//
// The count is u16 and ItemType is u8. Writing them as u32/u16 (as this did
// previously) both inflated the count the client reads — 13 rows were read as
// 3328, overflowing the 512-entry table by 45KB — and made the entry stride 12
// instead of 11, desyncing every field after it.
func writeDivaPrizeList(bf *byteframe.ByteFrame, prizes []DivaPrize) {
	if len(prizes) > divaPrizeListMax {
		prizes = prizes[:divaPrizeListMax]
	}
	bf.WriteUint16(uint16(len(prizes)))
	for _, p := range prizes {
		bf.WriteUint32(uint32(p.PointsReq))
		bf.WriteUint8(uint8(p.ItemType))
		bf.WriteUint16(uint16(p.ItemID))
		bf.WriteUint16(uint16(p.Quantity))
		if p.GR {
			bf.WriteUint8(1)
		} else {
			bf.WriteUint8(0)
		}
		if p.Repeatable {
			bf.WriteUint8(1)
		} else {
			bf.WriteUint8(0)
		}
	}
}

func handleMsgMhfGetUdTacticsRewardList(s *Session, p mhfpacket.MHFPacket) {
	// Diva defense interception reward list
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsRewardList)

	personal, err := s.server.divaRepo.GetPersonalPrizes()
	if err != nil {
		s.logger.Warn("Failed to get personal prizes", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	var hrGuild []DivaPrize
	if s.server.erupeConfig.RealClientMode == cfg.ZZ {
		ranks, ok := s.server.divaRepo.(DivaRewardRankRepository)
		if !ok {
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		hr, gr, err := ranks.GetDivaRewardRanks(s.charID)
		if err != nil {
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		personal = append(append([]DivaPrize(nil), personal...), divaHRInterceptionDisplay(hr, gr)...)
		personal = append(personal, divaMelodyDisplay()...)
		sort.SliceStable(personal, func(i, j int) bool { return personal[i].PointsReq < personal[j].PointsReq })
		hrGuild = divaHRGuildDisplay(hr, gr)
	}
	guild, err := s.server.divaRepo.GetGuildPrizes()
	if err != nil {
		s.logger.Warn("Failed to get guild prizes", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	if len(hrGuild) > 0 {
		guild = append(append([]DivaPrize(nil), guild...), hrGuild...)
		sort.SliceStable(guild, func(i, j int) bool { return guild[i].PointsReq < guild[j].PointsReq })
	}
	if s.server.erupeConfig.RealClientMode == cfg.ZZ {
		// Display the already-implemented automatic one-area hall entitlement.
		// Do not issue an artificial inventory item or a second unlock receipt.
		guild = append(append([]DivaPrize(nil), guild...), DivaPrize{Type: "guild", PointsReq: 1, ItemType: 28, Quantity: 1}, DivaPrize{Type: "guild", PointsReq: 1, ItemType: 28, Quantity: 1, GR: true})
		sort.SliceStable(guild, func(i, j int) bool { return guild[i].PointsReq < guild[j].PointsReq })
	}

	bf := byteframe.NewByteFrame()
	// Leading status byte: the client skips parsing entirely when this is
	// non-zero. It was missing, so the client consumed the first byte of the
	// personal count as the status and read every subsequent field shifted.
	bf.WriteUint8(0)
	writeDivaPrizeList(bf, personal)
	writeDivaPrizeList(bf, guild)
	// Retain the native third slot. No separate interception ranking prize
	// catalog is verified in the final manual/round-40 notice. A zero count is
	// mandatory, not evidence that a known original reward is unimplemented.
	bf.WriteUint16(0)

	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdTacticsFollower(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsFollower)
	getDivaTacticsFollower(s, pkt.AckHandle)
}

func handleMsgMhfGetUdTacticsBonusQuest(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsBonusQuest)
	handleDivaTacticsBonusQuest(s, pkt)
}

// udTacticsFirstQuestBonuses are the static first-quest bonus point values,
// matching the retail capture (1500/2000/2500/3000/4500).
var udTacticsFirstQuestBonuses = []uint32{1500, 2000, 2500, 3000, 4500}

func handleMsgMhfGetUdTacticsFirstQuestBonus(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsFirstQuestBonus)
	bf := byteframe.NewByteFrame()
	// Wire layout is u8 count, then {u8 index, u32 value} per entry. Writing a
	// u32 count made the client (which reads a u8) see 0 and skip the whole
	// table, and the index/value order was inverted.
	bf.WriteUint8(uint8(len(udTacticsFirstQuestBonuses)))
	for i, bonus := range udTacticsFirstQuestBonuses {
		bf.WriteUint8(uint8(i))
		bf.WriteUint32(bonus)
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdTacticsRemainingPoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsRemainingPoint)
	if s.server.erupeConfig.RealClientMode == cfg.ZZ {
		handleDivaRemainingAreas(s, pkt)
		return
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint32(0) // Points until Special Guild Hall earned
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdTacticsRanking(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsRanking)
	handleDivaAreaRanking(s, pkt)
}

func handleMsgMhfSetUdTacticsFollower(s *Session, p mhfpacket.MHFPacket) {
	setDivaTacticsFollower(s, p.(*mhfpacket.MsgMhfSetUdTacticsFollower))
}

func handleMsgMhfGetUdTacticsLog(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsLog)
	handleDivaTacticsLog(s, pkt.AckHandle)
}
