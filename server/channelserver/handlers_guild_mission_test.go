package channelserver

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"erupe-ce/network/mhfpacket"
)

func guildMissionHandlerSession(repo GuildMissionRepo) (*Server, *Session) {
	server := createMockServer()
	server.guildMissionRepo = repo
	ensureGuildMissionService(server)
	return server, createMockSession(1, server)
}

func TestHandleMsgMhfAddGuildMissionCount(t *testing.T) {
	repo := &mockGuildMissionRepo{
		progressResult: GuildMissionProgressResult{Completed: true},
	}
	_, session := guildMissionHandlerSession(repo)

	handleMsgMhfAddGuildMissionCount(session, &mhfpacket.MsgMhfAddGuildMissionCount{
		AckHandle: 9,
		MissionID: 431201,
		Count:     35,
	})

	ack := readAck(t, session)
	if ack.ErrorCode != 0 || ack.IsBufferResponse {
		t.Fatalf("unexpected ACK: %+v", ack)
	}
	if got := binary.BigEndian.Uint32(ack.Payload); got != guildMissionAddCompleted {
		t.Fatalf("completion status = %d, want %d", got, guildMissionAddCompleted)
	}
	if repo.progressID != 431201 || repo.progressCount != 35 {
		t.Fatalf("repository call = id %d/count %d", repo.progressID, repo.progressCount)
	}
}

func TestHandleMsgMhfAddGuildMissionCountFailure(t *testing.T) {
	repo := &mockGuildMissionRepo{progressErr: errors.New("db failed")}
	_, session := guildMissionHandlerSession(repo)

	handleMsgMhfAddGuildMissionCount(session, &mhfpacket.MsgMhfAddGuildMissionCount{
		AckHandle: 1,
		MissionID: 431201,
		Count:     1,
	})
	ack := readAck(t, session)
	if ack.ErrorCode != 0 {
		t.Fatalf("transport error code = %d, want 0", ack.ErrorCode)
	}
	if got := binary.BigEndian.Uint32(ack.Payload); got != guildMissionAddFailed {
		t.Fatalf("ADD failure status = %d, want %d", got, guildMissionAddFailed)
	}
}

func TestHandleMsgMhfSetGuildMissionTarget(t *testing.T) {
	repo := &mockGuildMissionRepo{}
	_, session := guildMissionHandlerSession(repo)

	handleMsgMhfSetGuildMissionTarget(session, &mhfpacket.MsgMhfSetGuildMissionTarget{
		AckHandle: 2,
		MissionID: 431202,
	})

	ack := readAck(t, session)
	if ack.ErrorCode != 0 || ack.IsBufferResponse {
		t.Fatalf("unexpected ACK: %+v", ack)
	}
	if got := binary.BigEndian.Uint32(ack.Payload); got != 0 {
		t.Fatalf("SET status = %d, want 0", got)
	}
	if repo.startedDef.ID != 431202 {
		t.Fatalf("started mission = %d, want 431202", repo.startedDef.ID)
	}
}

func TestHandleMsgMhfSetGuildMissionTargetRejectsUnknownID(t *testing.T) {
	repo := &mockGuildMissionRepo{}
	_, session := guildMissionHandlerSession(repo)

	handleMsgMhfSetGuildMissionTarget(session, &mhfpacket.MsgMhfSetGuildMissionTarget{
		AckHandle: 2,
		MissionID: 999999,
	})

	ack := readAck(t, session)
	if ack.ErrorCode != 1 {
		t.Fatalf("error code = %d, want 1", ack.ErrorCode)
	}
	if repo.startedCharID != 0 {
		t.Fatal("unknown mission reached repository")
	}
}

func TestHandleMsgMhfCancelGuildMissionTarget(t *testing.T) {
	repo := &mockGuildMissionRepo{}
	_, session := guildMissionHandlerSession(repo)

	handleMsgMhfCancelGuildMissionTarget(session, &mhfpacket.MsgMhfCancelGuildMissionTarget{
		AckHandle: 3,
		MissionID: 431203,
	})

	ack := readAck(t, session)
	if ack.ErrorCode != 0 || ack.IsBufferResponse {
		t.Fatalf("unexpected ACK: %+v", ack)
	}
	if got := binary.BigEndian.Uint32(ack.Payload); got != guildMissionCancelSucceeded {
		t.Fatalf("CANCEL status = %d, want %d", got, guildMissionCancelSucceeded)
	}
	if repo.cancelID != 431203 {
		t.Fatalf("cancelled mission = %d, want 431203", repo.cancelID)
	}
}

func TestHandleMsgMhfCancelGuildMissionTargetFailureRollsBackClientTickets(t *testing.T) {
	repo := &mockGuildMissionRepo{cancelErr: errors.New("db failed")}
	_, session := guildMissionHandlerSession(repo)

	handleMsgMhfCancelGuildMissionTarget(session, &mhfpacket.MsgMhfCancelGuildMissionTarget{
		AckHandle: 3,
		MissionID: 431203,
	})

	ack := readAck(t, session)
	if ack.ErrorCode != 0 || ack.IsBufferResponse {
		t.Fatalf("unexpected ACK: %+v", ack)
	}
	if got := binary.BigEndian.Uint32(ack.Payload); got != guildMissionCancelFailed {
		t.Fatalf("CANCEL failure status = %d, want %d", got, guildMissionCancelFailed)
	}
}

func TestHandleMsgMhfGetGuildMissionRecord(t *testing.T) {
	activeSetAt := time.Unix(1_800_000_000, 0)
	effectCompletedAt := time.Unix(1_800_100_000, 0)
	repo := &mockGuildMissionRepo{
		snapshot: GuildMissionSnapshot{
			Active: &GuildMissionRun{
				MissionID:           431201,
				TargetType:          1,
				TargetID:            4761,
				RequiredCount:       35,
				Progress:            12,
				SkipTickets:         1,
				ProgressPerExchange: 1,
				CancelTicketCost:    0,
				GR:                  false,
				RewardType:          2,
				RewardLevel:         1,
				SetAt:               activeSetAt,
			},
			Effects: []GuildMissionRun{{
				MissionID:           431202,
				TargetType:          0,
				TargetID:            95,
				RequiredCount:       12,
				Progress:            12,
				SkipTickets:         2,
				ProgressPerExchange: 1,
				CancelTicketCost:    0,
				GR:                  true,
				RewardType:          3,
				RewardLevel:         2,
				CompletedAt:         &effectCompletedAt,
			}},
		},
	}
	_, session := guildMissionHandlerSession(repo)

	handleMsgMhfGetGuildMissionRecord(session, &mhfpacket.MsgMhfGetGuildMissionRecord{
		AckHandle: 4,
	})

	ack := readAck(t, session)
	if ack.ErrorCode != 0 || !ack.IsBufferResponse {
		t.Fatalf("unexpected ACK: %+v", ack)
	}
	if len(ack.Payload) != guildMissionRecordSize {
		t.Fatalf("payload size = %d, want %d", len(ack.Payload), guildMissionRecordSize)
	}
	if got := binary.BigEndian.Uint32(ack.Payload[0:4]); got != 2 {
		t.Fatalf("record count = %d, want 2", got)
	}

	first := ack.Payload[4 : 4+guildMissionRecordWireSize]
	if got := binary.BigEndian.Uint32(first[0:4]); got != 431201 {
		t.Fatalf("active mission ID = %d", got)
	}
	if got := binary.BigEndian.Uint16(first[8:10]); got != 35 {
		t.Fatalf("active required = %d", got)
	}
	if got := binary.BigEndian.Uint16(first[10:12]); got != 12 {
		t.Fatalf("active progress = %d", got)
	}
	if got := binary.BigEndian.Uint16(first[23:25]); got != guildMissionStateActive {
		t.Fatalf("active state = %d", got)
	}
	if got := binary.BigEndian.Uint16(first[14:16]); got != 1 {
		t.Fatalf("active progress per exchange = %d", got)
	}
	if got := binary.BigEndian.Uint16(first[16:18]); got != 0 {
		t.Fatalf("active cancel ticket cost = %d", got)
	}
	if got := binary.BigEndian.Uint32(first[25:29]); got != uint32(activeSetAt.Unix()) {
		t.Fatalf("active set time = %d", got)
	}

	secondStart := 4 + guildMissionRecordWireSize
	second := ack.Payload[secondStart : secondStart+guildMissionRecordWireSize]
	if got := binary.BigEndian.Uint32(second[0:4]); got != 431202 {
		t.Fatalf("effect mission ID = %d", got)
	}
	if second[18] != 1 {
		t.Fatalf("effect GR flag = %d, want 1", second[18])
	}
	if got := binary.BigEndian.Uint16(second[23:25]); got != guildMissionStateEffect {
		t.Fatalf("effect state = %d", got)
	}
	if got := binary.BigEndian.Uint32(second[25:29]); got != uint32(effectCompletedAt.Unix()) {
		t.Fatalf("effect completion time = %d", got)
	}
	for i, value := range ack.Payload[secondStart+guildMissionRecordWireSize:] {
		if value != 0 {
			t.Fatalf("padding byte %d = %#x, want zero", i, value)
		}
	}
}

func TestHandleMsgMhfGetGuildMissionRecordWithoutMembership(t *testing.T) {
	repo := &mockGuildMissionRepo{snapshotErr: ErrGuildMissionNotMember}
	_, session := guildMissionHandlerSession(repo)

	handleMsgMhfGetGuildMissionRecord(session, &mhfpacket.MsgMhfGetGuildMissionRecord{
		AckHandle: 5,
	})
	ack := readAck(t, session)
	if ack.ErrorCode != 0 || len(ack.Payload) != guildMissionRecordSize {
		t.Fatalf("unexpected ACK: %+v", ack)
	}
	for i, value := range ack.Payload {
		if value != 0 {
			t.Fatalf("empty record byte %d = %#x", i, value)
		}
	}
}

func TestEncodeGuildMissionRecordCapsClientArray(t *testing.T) {
	effects := make([]GuildMissionRun, 20)
	completedAt := time.Unix(1_800_000_000, 0)
	for i := range effects {
		effects[i] = GuildMissionRun{
			MissionID:     uint32(431201 + i),
			RequiredCount: 1,
			Progress:      1,
			CompletedAt:   &completedAt,
		}
	}
	active := &GuildMissionRun{MissionID: 431200, SetAt: completedAt}
	payload := encodeGuildMissionRecord(GuildMissionSnapshot{Active: active, Effects: effects})
	if got := binary.BigEndian.Uint32(payload[:4]); got != guildMissionRecordMaxCount {
		t.Fatalf("record count = %d, want %d", got, guildMissionRecordMaxCount)
	}
	if len(payload) != guildMissionRecordSize {
		t.Fatalf("payload size = %d, want %d", len(payload), guildMissionRecordSize)
	}

	payload = encodeGuildMissionRecord(GuildMissionSnapshot{Effects: effects})
	if got := binary.BigEndian.Uint32(payload[:4]); got != guildMissionEffectMaxCount {
		t.Fatalf("effect-only record count = %d, want %d", got, guildMissionEffectMaxCount)
	}
}

func TestHandleMsgMhfGetGuildMissionList(t *testing.T) {
	repo := &mockGuildMissionRepo{}
	_, session := guildMissionHandlerSession(repo)

	handleMsgMhfGetGuildMissionList(session, &mhfpacket.MsgMhfGetGuildMissionList{
		AckHandle: 6,
	})

	ack := readAck(t, session)
	if ack.ErrorCode != 0 || !ack.IsBufferResponse {
		t.Fatalf("unexpected ACK: %+v", ack)
	}
	const expectedSize = 15*25 + guildMissionListTrailerSize
	if len(ack.Payload) != expectedSize {
		t.Fatalf("list payload size = %d, want %d", len(ack.Payload), expectedSize)
	}
	if got := binary.BigEndian.Uint32(ack.Payload[0:4]); got != 431201 {
		t.Fatalf("first mission ID = %d", got)
	}
	trailer := ack.Payload[len(ack.Payload)-guildMissionListTrailerSize:]
	for i, value := range trailer {
		if value != 0 {
			t.Fatalf("trailer byte %d = %#x, want zero", i, value)
		}
	}
}
