package channelserver

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/questdelivery"
	"erupe-ce/network/mhfpacket"
)

func deliveryResultTestData(questID uint16) []byte {
	data := make([]byte, questIDOffset+2)
	binary.LittleEndian.PutUint16(data[questIDOffset:], questID)
	return data
}

func requireDeliveryFailureAck(t *testing.T, s *Session, handle uint32) {
	t.Helper()
	if len(s.sendPackets) != 1 {
		t.Fatalf("response count = %d, want one failure ACK", len(s.sendPackets))
	}
	pkt := <-s.sendPackets
	bf := byteframe.NewByteFrameFromBytes(pkt.data)
	bf.ReadUint16() // opcode
	var ack mhfpacket.MsgSysAck
	if err := ack.Parse(bf, s.clientContext); err != nil {
		t.Fatal(err)
	}
	if ack.AckHandle != handle || ack.ErrorCode != 1 || ack.IsBufferResponse {
		t.Fatalf("unexpected response: %#v", ack)
	}
}

func TestQuestDeliveryResultPendingSurvivesTownAndRejectsBeforeRepositories(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	srv := deliveryDepartureTestServer(t, start, start.Add(time.Hour))
	questStage := NewStage(deliveryDepartureTestStage)
	srv.stages.Store(questStage.id, questStage)
	s := createMockSession(1, srv)
	pinDeliveryDepartureParticipant(t, s, questStage, start)
	town := NewStage("sl1Ns200p0a0u0")
	srv.stages.Store(town.id, town)
	if !doStageTransfer(s, 100, town.id) {
		t.Fatal("return to town failed")
	}
	for len(s.sendPackets) != 0 {
		<-s.sendPackets
	}
	if s.questDeliveryDeparture.Load() != nil || s.questDeliveryReceipt.Load() == nil || s.questDeliveryReceipt.Load().questID != 0 {
		t.Fatal("town transfer forgot the unvalidated departure")
	}
	// These mocks deliberately have no character/guild repositories. The
	// failure must be decided before either handler can read/write them.
	handleMsgMhfSavedata(s, &mhfpacket.MsgMhfSavedata{AckHandle: 101})
	requireDeliveryFailureAck(t, s, 101)
	handleMsgSysRecordLog(s, &mhfpacket.MsgSysRecordLog{AckHandle: 102, Data: deliveryResultTestData(40221)})
	requireDeliveryFailureAck(t, s, 102)
	if s.closed.Load() {
		t.Fatal("pending save/result rejection unexpectedly disconnected the hunter")
	}
}

func TestQuestDeliveryResultValidatedReceiptSurvivesTownAndExpiry(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	srv := deliveryDepartureTestServer(t, start, end)
	stage := NewStage(deliveryDepartureTestStage)
	stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(40221, 0)
	srv.stages.Store(stage.id, stage)
	s := createMockSession(1, srv)
	pinDeliveryDepartureParticipant(t, s, stage, end.Add(-time.Nanosecond))
	town := NewStage("sl1Ns200p0a0u0")
	srv.stages.Store(town.id, town)
	if !doStageTransfer(s, 1, town.id) {
		t.Fatal("return to town failed")
	}
	if s.questDeliveryDeparture.Load() != nil {
		t.Fatal("town retained an active departure")
	}
	receipt := s.questDeliveryReceipt.Load()
	if receipt == nil || receipt.questID != 40221 || !receipt.admittedAt.Equal(end.Add(-time.Nanosecond)) {
		t.Fatal("town lost the validated result receipt")
	}
	if s.allowsQuestDelivery(40221, time.Now()) {
		t.Fatal("test window must have expired")
	}
	if !s.allowsQuestDeliverySave() || !s.allowsQuestDeliveryRecord(deliveryResultTestData(40221)) {
		t.Fatal("expiry or return to town rejected a legitimately admitted result")
	}
}

func TestQuestDeliveryResultLateSetupValidatesPendingReceipt(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	srv := deliveryDepartureTestServer(t, start, start.Add(time.Hour))
	stage := NewStage(deliveryDepartureTestStage)
	srv.stages.Store(stage.id, stage)
	s := createMockSession(1, srv)
	pinDeliveryDepartureParticipant(t, s, stage, start)
	if s.allowsQuestDeliverySave() || s.allowsQuestDeliveryRecord(deliveryResultTestData(40221)) {
		t.Fatal("pending setup authorized a save/result")
	}
	handleMsgSysSetStageBinary(s, &mhfpacket.MsgSysSetStageBinary{
		StageID: stage.id, BinaryType0: 1, BinaryType1: 3,
		RawDataPayload: questRunStagePayload(40221, 0),
	})
	if !s.allowsQuestDeliverySave() || !s.allowsQuestDeliveryRecord(deliveryResultTestData(40221)) {
		t.Fatal("validated late setup did not authorize the admitted run's result")
	}
}

func TestQuestDeliveryResultManagedIDRequiresMatchingReceipt(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	srv := deliveryDepartureTestServer(t, start, start.Add(time.Hour))
	s := createMockSession(1, srv)
	if !s.allowsQuestDeliverySave() || !s.allowsQuestDeliveryRecord(deliveryResultTestData(23303)) {
		t.Fatal("fresh login's unrelated legacy save/result was blocked")
	}
	handleMsgSysRecordLog(s, &mhfpacket.MsgSysRecordLog{AckHandle: 1, Data: deliveryResultTestData(40221)})
	requireDeliveryFailureAck(t, s, 1)
	s.questDeliveryReceipt.Store(&questDeliveryReceipt{admittedAt: start, questID: 23303})
	if s.allowsQuestDeliveryRecord(deliveryResultTestData(40221)) {
		t.Fatal("another quest's receipt authorized a managed result")
	}
	s.questDeliveryReceipt.Store(&questDeliveryReceipt{admittedAt: start.Add(time.Hour), questID: 40221})
	if s.allowsQuestDeliveryRecord(deliveryResultTestData(40221)) {
		t.Fatal("a receipt at the excluded end boundary authorized a result")
	}
}

func TestQuestDeliveryResultFeatureOffPreservesLegacy(t *testing.T) {
	s := createMockSession(1, createMockServer())
	s.questDeliveryReceipt.Store(&questDeliveryReceipt{})
	if !s.allowsQuestDeliverySave() || !s.allowsQuestDeliveryRecord(deliveryResultTestData(40221)) {
		t.Fatal("disabled quest delivery applied a pending/result gate")
	}
}

func TestQuestDeliveryResultTowerAliasesUseTheSamePublicationPolicy(t *testing.T) {
	for _, pair := range [][2]uint16{{21731, 21749}, {21749, 21731}, {21746, 21750}, {21750, 21746}} {
		t.Run(fmt.Sprintf("%d_to_%d", pair[0], pair[1]), func(t *testing.T) {
			catalog, err := questdelivery.Parse([]byte(fmt.Sprintf(`{
				"version":"alias-test",
				"profiles":[{"id":"events","worlds":["events"]}],
				"schedules":[{"id":"always"}],
				"quests":[{"quest_id":%d,"profiles":["events"],"schedule":"always"}]
			}`, pair[1])))
			if err != nil {
				t.Fatal(err)
			}
			srv := createMockServer()
			srv.questDelivery, srv.worldName, srv.channelNumber = catalog, "events", 1
			s := createMockSession(1, srv)
			s.questDeliveryReceipt.Store(&questDeliveryReceipt{admittedAt: time.Now(), questID: pair[0]})
			if !s.allowsQuestDeliveryRecord(deliveryResultTestData(pair[1])) {
				t.Fatal("original/runtime alias rejected the same admitted quest")
			}
			srv.worldName = "other"
			if s.allowsQuestDeliveryRecord(deliveryResultTestData(pair[1])) {
				t.Fatal("alias result bypassed the shared world restriction")
			}
		})
	}
}
