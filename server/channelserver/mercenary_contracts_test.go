package channelserver

import (
	"bytes"
	"encoding/binary"
	"net"
	"reflect"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/mhfmon"
	cfg "erupe-ce/config"
	"erupe-ce/network"
	"erupe-ce/network/binpacket"
	"erupe-ce/network/mhfpacket"
)

func TestMercenaryContractAndMaterialFlow(t *testing.T) {
	repo, db, lenderID, _ := setupMercenaryRepo(t)
	borrowerID := CreateTestCharacter(t, db, CreateTestUser(t, db, "mercenary_borrower"), "Borrower")
	if _, err := db.Exec(`UPDATE characters SET rasta_id = CASE WHEN id=$1 THEN 50 ELSE 51 END
		WHERE id IN ($1,$2)`, lenderID, borrowerID); err != nil {
		t.Fatal(err)
	}

	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.charRepo = NewCharacterRepository(db)
	server.mercenaryRepo = repo
	lender := createMockSession(lenderID, server)
	borrower := createMockSession(borrowerID, server)
	bar := NewStage("sl1Ns211p0a0u0")
	lender.stage, borrower.stage = bar, bar
	server.sessions = map[net.Conn]*Session{&mockConn{}: lender, &mockConn{}: borrower}

	contract := &mhfpacket.MsgMhfContractMercenary{AckHandle: 1, PactMercID: 50, CID: borrowerID}
	handleMsgMhfContractMercenary(lender, contract)
	assertMercenaryAck(t, lender, 1)

	// Native subtype 0x1e, command 3, eight-byte message, status zero.
	consent := []byte{0, 2, 0x1e, 0, 0, 0, 0, 0, 8, 0, 0, 0, 3, 8, 0, 0, 0, 0, 0, 0}
	bf := byteframe.NewByteFrame()
	targeted := binpacket.MsgBinTargeted{TargetCount: 1, TargetCharIDs: []uint32{lenderID}, RawDataPayload: consent}
	if err := targeted.Build(bf); err != nil {
		t.Fatal(err)
	}
	handleMsgSysCastBinary(borrower, &mhfpacket.MsgSysCastBinary{
		BroadcastType: BroadcastTypeTargeted, MessageType: BinaryMessageTypeData, RawDataPayload: bf.Data(),
	})
	forwarded := <-lender.sendPackets
	if !forwarded.mercenaryExchange || !bytes.Equal(forwarded.data[10:], consent) {
		t.Fatalf("wrong mercenary reply frame: %x", forwarded.data)
	}
	handleMsgMhfContractMercenary(lender, contract)
	assertMercenaryAck(t, lender, 0)
	handleMsgMhfContractMercenary(lender, contract)
	assertMercenaryAck(t, lender, 0)

	pact, enabled, err := repo.GetMercenaryPact(borrowerID)
	if err != nil || pact.CharID != lenderID || pact.PactID != 50 || !pact.Since.Valid || !enabled {
		t.Fatalf("borrower's pact = %+v, enabled=%t, err=%v", pact, enabled, err)
	}
	logs, err := repo.GetMercenaryContractLogs(borrowerID)
	if err != nil || len(logs) != 1 || logs[0].Event != 0 || logs[0].Initiated {
		t.Fatalf("borrower logs = %+v, err=%v", logs, err)
	}
	if err := server.charRepo.UpdateGCPAndPact(borrowerID, 100, 0); err != nil {
		t.Fatal(err)
	}
	pact, enabled, err = repo.GetMercenaryPact(borrowerID)
	if err != nil || pact.PactID != 50 || enabled {
		t.Fatalf("escort off changed the pact: %+v, enabled=%t, err=%v", pact, enabled, err)
	}

	start := TimeAdjusted().Add(time.Second)
	if err := repo.ChangeMercenaryPact(lenderID, borrowerID, borrowerID, 50, 1, start); err == nil {
		t.Fatal("new contract was cancelled before the end of its lock")
	}
	stage := NewStage("sl2Qs200p0a0u0")
	server.stages.Store(stage.id, stage)
	data := make([]byte, nativeQuestRecordBytes)
	data[questResultCodeOffset] = questResultCodeCleared
	const monster = 6
	if !mhfmon.Monsters[monster].Large {
		t.Fatal("invalid large monster fixture")
	}
	data[killLogHeaderSize+monster] = 1

	// An empty companion slot leaves no material reward, even with a kill.
	handleMsgSysSetStageBinary(borrower, &mhfpacket.MsgSysSetStageBinary{
		StageID: stage.id, BinaryType0: 1, BinaryType1: 23, RawDataPayload: make([]byte, 0x114),
	})
	borrower.stage = stage
	borrower.beginMercenaryDeparture(stage, start)
	borrower.towerGuardianRunID, borrower.towerGuardianQuestID = stage.towerGuardianRunID, 123
	if err := borrower.recordMercenaryResult(123, data, true, start); err != nil {
		t.Fatal(err)
	}
	preview, err := repo.PreviewMercenaryHunts(lenderID, start)
	if err != nil || len(preview) != 0 {
		t.Fatalf("unexpected reward for empty slot: %v, %v", preview, err)
	}

	companion := make([]byte, 0x114)
	companion[16] = 1
	binary.LittleEndian.PutUint32(companion[8:12], 50)
	borrower.stage = bar
	borrower.mercenaryDeparture = mercenaryDeparture{}
	handleMsgSysSetStageBinary(borrower, &mhfpacket.MsgSysSetStageBinary{
		StageID: stage.id, BinaryType0: 1, BinaryType1: 23, RawDataPayload: companion,
	})
	borrower.beginMercenaryDeparture(stage, start)
	// The native result is sent after the town transfer.
	if err := borrower.recordMercenaryResult(123, data, true, start); err != nil {
		t.Fatal(err)
	}
	// Reward remains available after the contract ends.
	if err := repo.ChangeMercenaryPact(lenderID, borrowerID, borrowerID, 50, 1, start.Add(mercenaryContractLock)); err != nil {
		t.Fatal(err)
	}
	handleMsgMhfMercenaryHuntdata(lender, &mhfpacket.MsgMhfMercenaryHuntdata{AckHandle: 2, RequestType: 1})
	ack := assertMercenaryAck(t, lender, 0)
	if len(ack.AckData) != 9 || ack.AckData[0] != 1 || binary.BigEndian.Uint32(ack.AckData[5:9]) != monster {
		t.Fatalf("bad hunt response %x", ack.AckData)
	}
	handleMsgMhfMercenaryHuntdata(lender, &mhfpacket.MsgMhfMercenaryHuntdata{AckHandle: 2, RequestType: 1})
	if again := assertMercenaryAck(t, lender, 0); !bytes.Equal(again.AckData, ack.AckData) {
		t.Fatal("preview changed before the claim")
	}
	handleMsgMhfMercenaryHuntdata(lender, &mhfpacket.MsgMhfMercenaryHuntdata{AckHandle: 3})
	assertMercenaryAck(t, lender, 0)
	handleMsgMhfMercenaryHuntdata(lender, &mhfpacket.MsgMhfMercenaryHuntdata{AckHandle: 4})
	assertMercenaryAck(t, lender, 1)
}

func TestMercenaryExchangeFrame(t *testing.T) {
	conn := &MockCryptConn{}
	session := createTestSession(conn)
	first := []byte{0, 0x1b, 1}
	exchange := []byte{0, 0x1b, 2}
	separate := []byte{0, 0x1b, 3}
	sent := make(chan error, 1)
	session.sendPackets <- packet{data: first}
	session.sendPackets <- packet{data: exchange, mercenaryExchange: true}
	session.sendPackets <- packet{data: separate, sent: sent}
	done := make(chan struct{})
	go func() {
		defer close(done)
		session.sendLoop()
	}()
	defer func() {
		session.closed.Store(true)
		<-done
	}()
	select {
	case err := <-sent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("packet write did not complete")
	}
	want := [][]byte{{0, 0x1b, 1, 0, 0x1b, 2, 0, 0x10}, {0, 0x1b, 3, 0, 0x10}}
	if got := conn.GetSentPackets(); !reflect.DeepEqual(got, want) {
		t.Fatalf("frames = %x, want %x", got, want)
	}
}

func assertMercenaryAck(t *testing.T, s *Session, wantError uint8) *mhfpacket.MsgSysAck {
	t.Helper()
	select {
	case queued := <-s.sendPackets:
		bf := byteframe.NewByteFrameFromBytes(queued.data)
		if got := bf.ReadUint16(); got != uint16(network.MSG_SYS_ACK) {
			t.Fatalf("expected ACK, got %04x", got)
		}
		ack := &mhfpacket.MsgSysAck{}
		if err := ack.Parse(bf, s.clientContext); err != nil {
			t.Fatal(err)
		}
		if bf.Err() != nil || ack.ErrorCode != wantError {
			t.Fatalf("bad ACK %+v, want error code %d", ack, wantError)
		}
		return ack
	default:
		t.Fatal("no ACK queued")
		return nil
	}
}
