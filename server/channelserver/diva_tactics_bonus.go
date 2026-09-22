package channelserver

import (
	"fmt"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

const divaTacticsBonusMax = 32

type divaTacticsBonusQuest struct {
	QuestID    uint16
	Start, End uint32 // The native time check includes both endpoints.
	Percent    uint16 // Total percentage: 600 is x6, not 600 flat points.
}

// This is the quest order and multiplier from Erupe's existing November
// 13-20, 2019 payload, NOT a verified round-40 catalog. Retain the existing
// operating table while replacing its expired dates. The trailing SQL text
// in that old payload is not part of this protocol and must not be sent.
var divaTacticsLegacyBonusQuests = [...]struct {
	QuestID uint16
	Percent uint16
}{
	{58101, 1000}, {58053, 600}, {58062, 633}, {58119, 1050},
	{58097, 600}, {58052, 600}, {58101, 1000}, {58050, 600},
	{58062, 633}, {58119, 1050}, {58062, 633}, {58099, 650},
	{58051, 600}, {58096, 600}, {58062, 633}, {58101, 1000},
	{58098, 750}, {58058, 600}, {58119, 1050}, {58101, 1000},
}

// Rebase the existing 22:00 / 06:00 / 14:00 two-hour slots onto the UTC+9
// calendar date on which interception starts. Forced phases need not begin
// at the retail maintenance time, so intersect every slot with the actual
// phase. The anchor never depends on request time, reconnects or midnight.
func divaTacticsBonusSchedule(event DivaEvent) ([]divaTacticsBonusQuest, error) {
	if event.ID == 0 || int64(event.StartTime)+divaPhaseDuration+divaWeekDuration > 0xffffffff {
		return nil, fmt.Errorf("invalid Diva event or overflowing interception bonus timestamps")
	}
	phaseStart, phaseEnd := divaInterceptionWindow(event)
	local := phaseStart.In(divaLocation)
	first := time.Date(local.Year(), local.Month(), local.Day(), 22, 0, 0, 0, divaLocation)
	rows := make([]divaTacticsBonusQuest, 0, len(divaTacticsLegacyBonusQuests))
	for i, rule := range divaTacticsLegacyBonusQuests {
		start := first.Add(time.Duration(i) * 8 * time.Hour)
		end := start.Add(2 * time.Hour)
		if start.Before(phaseStart) {
			start = phaseStart
		}
		if end.After(phaseEnd) {
			end = phaseEnd
		}
		if !start.Before(end) {
			continue
		}
		rows = append(rows, divaTacticsBonusQuest{
			QuestID: rule.QuestID, Start: uint32(start.Unix()),
			End: uint32(end.Unix() - 1), Percent: rule.Percent,
		})
	}
	return rows, nil
}

// ZZ FUN_11537320 reads a u8 count and 12-byte records. It does not clear the
// unused tail, and FUN_103a24b0 / FUN_107a6e70 scan ALL 32 slots regardless of
// the last count. Explicit zero records prevent bonuses from the preceding
// round surviving an empty or shorter response. 385 bytes fit the 513-byte
// request buffer allocated by FUN_115373c0.
func divaTacticsBonusPayload(rows []divaTacticsBonusQuest) []byte {
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(divaTacticsBonusMax)
	for i := 0; i < divaTacticsBonusMax; i++ {
		var row divaTacticsBonusQuest
		if i < len(rows) {
			row = rows[i]
		}
		bf.WriteUint16(row.QuestID)
		bf.WriteUint32(row.Start)
		bf.WriteUint32(row.End)
		bf.WriteUint16(row.Percent)
	}
	return bf.Data()
}

func handleDivaTacticsBonusQuest(s *Session, pkt *mhfpacket.MsgMhfGetUdTacticsBonusQuest) {
	options := s.server.erupeConfig
	if options.RealClientMode != cfg.ZZ {
		// Dynamic dates and the fixed-array clearing behavior are verified only
		// for ZZ. Do not send an unverified layout to older clients.
		doAckBufSucceed(s, pkt.AckHandle, []byte{0})
		return
	}
	phase := options.DebugOptions.DivaOverride
	if phase == 0 || phase == 1 || phase == 3 {
		doAckBufSucceed(s, pkt.AckHandle, divaTacticsBonusPayload(nil))
		return
	}
	if s.server.divaRepo == nil || options.DebugOptions.InGameTimeOverrideHour != nil {
		// A shifted client clock cannot safely consume absolute event dates.
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	event, err := s.divaEvent()
	if err != nil {
		s.logger.Warn("Failed to resolve Diva interception bonus event", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	now := TimeAdjusted()
	start, end := divaInterceptionWindow(event)
	if event.ID == 0 || now.Before(start) || !now.Before(end) {
		doAckBufSucceed(s, pkt.AckHandle, divaTacticsBonusPayload(nil))
		return
	}
	rows, err := divaTacticsBonusSchedule(event)
	if err != nil {
		s.logger.Warn("Rejected Diva interception bonus schedule", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	// Native FUN_103a24b0 computes the bonus from this percentage and includes
	// it in the submitted interception points. Never multiply the saved
	// TacticsPoints again on the server.
	s.logger.Debug("Diva interception bonus schedule sent",
		zap.Uint32("eventID", event.ID), zap.Int("quests", len(rows)),
		zap.String("basis", "legacy-2019-11-rebased"))
	doAckBufSucceed(s, pkt.AckHandle, divaTacticsBonusPayload(rows))
}
