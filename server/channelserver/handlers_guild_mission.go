package channelserver

import (
	"errors"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
)

const (
	guildMissionListTrailerSize = 45
	guildMissionRecordSize      = 0x190
	guildMissionRecordWireSize  = 0x1D
	guildMissionRecordMaxCount  = 11
	guildMissionEffectMaxCount  = 10
	guildMissionStateActive     = 0
	guildMissionStateEffect     = 1
	guildMissionAddFailed       = 1
	guildMissionAddCompleted    = 2
	guildMissionCancelFailed    = 0
	guildMissionCancelSucceeded = 1
)

func handleMsgMhfGetGuildMissionList(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetGuildMissionList)
	bf := byteframe.NewByteFrame()
	timestamp := uint32(TimeAdjusted().Unix())
	for _, mission := range guildMissionDefinitions {
		bf.WriteUint32(mission.ID)
		bf.WriteUint32(mission.Unk)
		bf.WriteUint16(mission.Type)
		bf.WriteUint16(mission.Goal)
		bf.WriteUint16(mission.Quantity)
		bf.WriteUint16(mission.SkipTickets)
		bf.WriteBool(mission.GR)
		bf.WriteUint16(mission.RewardType)
		bf.WriteUint16(mission.RewardLevel)
		bf.WriteUint32(timestamp)
	}
	// The official response is 0x1A4 bytes: 15 records followed by 45
	// reserved zero bytes. The ZZ client consumes the 15 records and ignores
	// this trailer, but preserving it avoids a protocol regression.
	bf.WriteBytes(make([]byte, guildMissionListTrailerSize))
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetGuildMissionRecord(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetGuildMissionRecord)

	snapshot, err := s.server.guildMissionService.GetSnapshot(s.charID)
	if err != nil {
		if errors.Is(err, ErrGuildMissionNotMember) {
			doAckBufSucceed(s, pkt.AckHandle, make([]byte, guildMissionRecordSize))
			return
		}
		s.logger.Error("Failed to load guild mission record", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, make([]byte, guildMissionRecordSize))
		return
	}
	doAckBufSucceed(s, pkt.AckHandle, encodeGuildMissionRecord(snapshot))
}

func handleMsgMhfAddGuildMissionCount(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfAddGuildMissionCount)
	result, err := s.server.guildMissionService.AddProgress(s.charID, pkt.MissionID, pkt.Count)
	if err != nil {
		s.logger.Warn("Failed to add guild mission progress",
			zap.Uint32("mission_id", pkt.MissionID),
			zap.Uint32("requested", pkt.Count),
			zap.Error(err),
		)
		// The client may have already removed guild tickets/items. A normal
		// ACK with result 1 enters its rollback path; a transport-level failure
		// leaves that rollback behavior undefined.
		doAckSimpleSucceed(s, pkt.AckHandle, guildMissionSimpleResult(guildMissionAddFailed))
		return
	}
	status := uint32(0)
	if result.Completed {
		status = guildMissionAddCompleted
	}
	doAckSimpleSucceed(s, pkt.AckHandle, guildMissionSimpleResult(status))
}

func handleMsgMhfSetGuildMissionTarget(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfSetGuildMissionTarget)
	if _, err := s.server.guildMissionService.Start(s.charID, pkt.MissionID); err != nil {
		s.logger.Warn("Failed to set guild mission target",
			zap.Uint32("mission_id", pkt.MissionID),
			zap.Error(err),
		)
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	doAckSimpleSucceed(s, pkt.AckHandle, guildMissionSimpleResult(0))
}

func handleMsgMhfCancelGuildMissionTarget(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfCancelGuildMissionTarget)
	if err := s.server.guildMissionService.Cancel(s.charID, pkt.MissionID); err != nil {
		s.logger.Warn("Failed to cancel guild mission target",
			zap.Uint32("mission_id", pkt.MissionID),
			zap.Error(err),
		)
		// Result 0 is the protocol-level failure code and makes the client
		// restore the cancellation tickets it removed before this request.
		doAckSimpleSucceed(s, pkt.AckHandle, guildMissionSimpleResult(guildMissionCancelFailed))
		return
	}
	doAckSimpleSucceed(s, pkt.AckHandle, guildMissionSimpleResult(guildMissionCancelSucceeded))
}

func guildMissionSimpleResult(value uint32) []byte {
	bf := byteframe.NewByteFrame()
	bf.WriteUint32(value)
	return bf.Data()
}

func encodeGuildMissionRecord(snapshot GuildMissionSnapshot) []byte {
	type record struct {
		run   GuildMissionRun
		state uint16
	}
	records := make([]record, 0, guildMissionRecordMaxCount)
	if snapshot.Active != nil {
		records = append(records, record{run: *snapshot.Active, state: guildMissionStateActive})
	}
	for index, effect := range snapshot.Effects {
		if index >= guildMissionEffectMaxCount {
			break
		}
		if len(records) >= guildMissionRecordMaxCount {
			break
		}
		records = append(records, record{run: effect, state: guildMissionStateEffect})
	}

	bf := byteframe.NewByteFrame()
	bf.WriteUint32(uint32(len(records)))
	for _, entry := range records {
		run := entry.run
		bf.WriteUint32(run.MissionID)
		bf.WriteUint16(run.TargetType)
		bf.WriteUint16(uint16(run.TargetID))
		bf.WriteUint16(uint16(run.RequiredCount))
		bf.WriteUint16(uint16(run.Progress))
		bf.WriteUint16(run.SkipTickets)
		bf.WriteUint16(run.ProgressPerExchange)
		bf.WriteUint16(run.CancelTicketCost)
		bf.WriteBool(run.GR)
		bf.WriteUint16(run.RewardType)
		bf.WriteUint16(run.RewardLevel)
		bf.WriteUint16(entry.state)

		var startedAt int64
		if entry.state == guildMissionStateActive {
			startedAt = run.SetAt.Unix()
		} else if run.CompletedAt != nil {
			startedAt = run.CompletedAt.Unix()
		}
		bf.WriteUint32(uint32(startedAt))
	}

	data := bf.Data()
	if len(data) > guildMissionRecordSize {
		// The count cap above makes this unreachable, but retain a hard bound
		// because the client parser itself performs no destination bounds check.
		data = data[:guildMissionRecordSize]
	}
	if len(data) < guildMissionRecordSize {
		data = append(data, make([]byte, guildMissionRecordSize-len(data))...)
	}
	return data
}
