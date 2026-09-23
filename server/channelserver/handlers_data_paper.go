package channelserver

import (
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
)

// PaperMissionTimetable represents a daily mission schedule entry.
type PaperMissionTimetable struct {
	Start time.Time
	End   time.Time
}

// PaperMissionData represents daily mission details.
type PaperMissionData struct {
	Unk0            uint8
	Unk1            uint8
	Unk2            int16
	Reward1ID       uint16
	Reward1Quantity uint8
	Reward2ID       uint16
	Reward2Quantity uint8
}

// PaperMission represents a daily mission wrapper.
type PaperMission struct {
	Timetables []PaperMissionTimetable
	Data       []PaperMissionData
}

// Unk1 is the client's mission type, the index into its mission text table
// (G10.1 [pac+0xd94], ZZ [pac+0xde0]): 1 floors reached, 2 TRP earned, 3 chests
// collected, 4 antiques collected, 5 large monsters slain, 6-9 guardians of
// zones 1-4. Unk2 is the goal; for TRP the client multiplies it by 100 before
// displaying and comparing it (G10.1 FUN_11002130, ZZ FUN_113ac8d0).
//
// The original rotating item quantities were not preserved. These twelve
// conservative tasks use the documented categories and rotate
// deterministically at noon JST: one floor, two large monsters, one chest,
// two antiques, 500 TRP, three floors, two floors, three large monsters, two
// chests, 1000 TRP, three antiques and four floors.
var towerDailyMissionPool = []PaperMissionData{
	{1, 1, 1, 0x2B9C, 1, 0, 0},
	{2, 5, 2, 0x2B97, 2, 0, 0},
	{3, 3, 1, 0x2B98, 2, 0, 0},
	{4, 4, 2, 0x2B99, 2, 0, 0},
	{5, 2, 5, 0x2B96, 1, 0, 0},
	{6, 1, 3, 0x2BC9, 1, 0, 0},
	{7, 1, 2, 0x2BA5, 1, 0, 0},
	{8, 5, 3, 0x2C75, 1, 0, 0},
	{9, 3, 2, 0x2C78, 1, 0, 0},
	{10, 2, 10, 0x2B9B, 1, 0, 0},
	{11, 4, 3, 0x2B97, 3, 0, 0},
	{12, 1, 4, 0x2B98, 3, 0, 0},
}

// towerDailyMissionMet reports whether one day's server counters satisfy a
// mission, reading the counter that matches the client's mission type.
func towerDailyMissionMet(c TowerDailyCounters, m PaperMissionData) bool {
	goal := int32(m.Unk2)
	if goal <= 0 {
		return false
	}
	switch m.Unk1 {
	case 1:
		return c.Floors >= goal
	case 2:
		return c.TRP >= goal*100
	case 3:
		return c.Chests >= goal
	case 4:
		return c.Antiques >= goal
	case 5:
		return c.Slays >= goal
	}
	return false
}

func towerDailyStart(now time.Time) time.Time {
	start := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	if now.Before(start) {
		start = start.Add(-24 * time.Hour)
	}
	return start
}

func towerDailyMissions(dayStart time.Time) []PaperMissionData {
	// Six of twelve tasks appear per day. Sliding by one yields a new set each
	// day without inventing a fresh reward table on every server restart.
	first := int(dayStart.Unix()/86400) % len(towerDailyMissionPool)
	out := make([]PaperMissionData, 6)
	for i := range out {
		out[i] = towerDailyMissionPool[(first+i)%len(towerDailyMissionPool)]
		// The client treats this byte as a 1-based timetable index, not
		// the position among the six missions. All six belong to today's
		// only timetable entry.
		out[i].Unk0 = 1
	}
	return out
}

// PaperData represents complete daily paper data.
type PaperData struct {
	Unk0 uint16
	Unk1 int16
	Unk2 int16
	Unk3 int16
	Unk4 int16
	Unk5 int16
	Unk6 int16
}

// PaperGift represents a paper gift reward entry.
type PaperGift struct {
	Unk0 uint16
	Unk1 uint8
	Unk2 uint8
	Unk3 uint16
}

func handleMsgMhfGetPaperData(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetPaperData)
	var data []*byteframe.ByteFrame

	var paperData []PaperData
	var paperMissions PaperMission
	var paperGift []PaperGift

	switch pkt.DataType {
	case 0:
		// Tower daily missions rotate at noon JST, not at midnight.
		dayStart := towerDailyStart(TimeAdjusted())
		paperMissions = PaperMission{
			[]PaperMissionTimetable{{dayStart, dayStart.Add(24 * time.Hour)}},
			towerDailyMissions(dayStart),
		}
	case 5:
		paperData = paperDataTower
	case 6:
		paperData = paperDataTowerScaling
	default:
		if pkt.DataType < 1000 {
			s.logger.Info("PaperData request for unknown type", zap.Uint32("DataType", pkt.DataType))
		}
	}

	if pkt.DataType > 1000 {
		_, ok := paperGiftData[pkt.DataType]
		if ok {
			paperGift = paperGiftData[pkt.DataType]
		} else {
			s.logger.Info("PaperGift request for unknown type", zap.Uint32("DataType", pkt.DataType))
		}
		for _, gift := range paperGift {
			bf := byteframe.NewByteFrame()
			bf.WriteUint16(gift.Unk0)
			bf.WriteUint8(gift.Unk1)
			bf.WriteUint8(gift.Unk2)
			bf.WriteUint16(gift.Unk3)
			data = append(data, bf)
		}
		doAckEarthSucceed(s, pkt.AckHandle, data)
	} else if pkt.DataType == 0 {
		bf := byteframe.NewByteFrame()
		bf.WriteUint16(uint16(len(paperMissions.Timetables)))
		bf.WriteUint16(uint16(len(paperMissions.Data)))
		for _, timetable := range paperMissions.Timetables {
			bf.WriteUint32(uint32(timetable.Start.Unix()))
			bf.WriteUint32(uint32(timetable.End.Unix()))
		}
		for _, mdata := range paperMissions.Data {
			bf.WriteUint8(mdata.Unk0)
			bf.WriteUint8(mdata.Unk1)
			bf.WriteInt16(mdata.Unk2)
			bf.WriteUint16(mdata.Reward1ID)
			bf.WriteUint8(mdata.Reward1Quantity)
			bf.WriteUint16(mdata.Reward2ID)
			bf.WriteUint8(mdata.Reward2Quantity)
		}
		doAckBufSucceed(s, pkt.AckHandle, bf.Data())
	} else {
		for _, pdata := range paperData {
			bf := byteframe.NewByteFrame()
			bf.WriteUint16(pdata.Unk0)
			bf.WriteInt16(pdata.Unk1)
			bf.WriteInt16(pdata.Unk2)
			bf.WriteInt16(pdata.Unk3)
			bf.WriteInt16(pdata.Unk4)
			bf.WriteInt16(pdata.Unk5)
			bf.WriteInt16(pdata.Unk6)
			data = append(data, bf)
		}
		doAckEarthSucceed(s, pkt.AckHandle, data)
	}
}
