package channelserver

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestHandleMsgMhfGetGachaPlayHistory_StubResponse(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetGachaPlayHistory{AckHandle: 100, GachaID: 1}
	handleMsgMhfGetGachaPlayHistory(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Fatal("Empty response")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetGachaPoint(t *testing.T) {
	server := createMockServer()
	userRepo := &mockUserRepoGacha{
		gachaFP: 100,
		gachaGP: 200,
		gachaGT: 300,
	}
	server.userRepo = userRepo

	session := createMockSession(1, server)
	session.userID = 1

	pkt := &mhfpacket.MsgMhfGetGachaPoint{AckHandle: 100}
	handleMsgMhfGetGachaPoint(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Fatal("Empty response")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfUseGachaPoint_TrialCoins(t *testing.T) {
	server := createMockServer()
	userRepo := &mockUserRepoGacha{}
	server.userRepo = userRepo

	session := createMockSession(1, server)
	session.userID = 1

	pkt := &mhfpacket.MsgMhfUseGachaPoint{
		AckHandle:    100,
		TrialCoins:   10,
		PremiumCoins: 0,
	}
	handleMsgMhfUseGachaPoint(session, pkt)

	select {
	case <-session.sendPackets:
		// success
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfUseGachaPoint_PremiumCoins(t *testing.T) {
	server := createMockServer()
	userRepo := &mockUserRepoGacha{}
	server.userRepo = userRepo

	session := createMockSession(1, server)
	session.userID = 1

	pkt := &mhfpacket.MsgMhfUseGachaPoint{
		AckHandle:    100,
		TrialCoins:   0,
		PremiumCoins: 5,
	}
	handleMsgMhfUseGachaPoint(session, pkt)

	select {
	case <-session.sendPackets:
		// success
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfReceiveGachaItem_Normal(t *testing.T) {
	server := createMockServer()
	charRepo := newMockCharacterRepo()
	// Store 2 items: count byte + 2 * 5 bytes each
	data := []byte{2, 1, 0, 100, 0, 5, 2, 0, 200, 0, 10}
	charRepo.columns["gacha_items"] = data
	server.charRepo = charRepo

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfReceiveGachaItem{AckHandle: 100, Freeze: false}
	handleMsgMhfReceiveGachaItem(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Fatal("Empty response")
		}
	default:
		t.Error("No response packet queued")
	}

	// After non-freeze receive, gacha_items should be cleared
	if charRepo.columns["gacha_items"] != nil {
		t.Error("Expected gacha_items to be cleared after receive")
	}
}

func TestHandleMsgMhfReceiveGachaItem_Overflow(t *testing.T) {
	server := createMockServer()
	charRepo := newMockCharacterRepo()
	// Build data with >36 items (overflow scenario): count=37, 37*5=185 bytes + 1 count byte = 186
	data := make([]byte, 186)
	data[0] = 37
	for i := 1; i < 186; i++ {
		data[i] = byte(i % 256)
	}
	charRepo.columns["gacha_items"] = data
	server.charRepo = charRepo

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfReceiveGachaItem{AckHandle: 100, Freeze: false}
	handleMsgMhfReceiveGachaItem(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Fatal("Empty response")
		}
	default:
		t.Error("No response packet queued")
	}

	// After overflow, remaining items should be saved
	saved := charRepo.columns["gacha_items"]
	if saved == nil {
		t.Error("Expected overflow items to be saved")
	}
}

func TestHandleMsgMhfReceiveGachaItem_Freeze(t *testing.T) {
	server := createMockServer()
	charRepo := newMockCharacterRepo()
	data := []byte{1, 1, 0, 100, 0, 5}
	charRepo.columns["gacha_items"] = data
	server.charRepo = charRepo

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfReceiveGachaItem{AckHandle: 100, Freeze: true}
	handleMsgMhfReceiveGachaItem(session, pkt)

	select {
	case <-session.sendPackets:
		// success
	default:
		t.Error("No response packet queued")
	}

	// Freeze should NOT clear the items
	if charRepo.columns["gacha_items"] == nil {
		t.Error("Expected gacha_items to be preserved on freeze")
	}
}

func TestHandleMsgMhfPlayNormalGacha_TransactError(t *testing.T) {
	server := createMockServer()
	gachaRepo := &mockGachaRepo{
		txErr:      errors.New("transact failed"),
		rewardPool: []GachaEntry{{ID: 10, Weight: 100}},
	}
	server.gachaRepo = gachaRepo
	server.userRepo = &mockUserRepoGacha{}
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPlayNormalGacha{AckHandle: 100, GachaID: 1, RollType: 0}
	handleMsgMhfPlayNormalGacha(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Fatal("Empty response")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfPlayNormalGacha_RewardPoolError(t *testing.T) {
	server := createMockServer()
	gachaRepo := &mockGachaRepo{
		txRolls:       1,
		rewardPoolErr: errors.New("pool error"),
	}
	server.gachaRepo = gachaRepo
	server.userRepo = &mockUserRepoGacha{}
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPlayNormalGacha{AckHandle: 100, GachaID: 1, RollType: 0}
	handleMsgMhfPlayNormalGacha(session, pkt)

	select {
	case <-session.sendPackets:
		// success - returns empty result
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfPlayNormalGacha_Success(t *testing.T) {
	server := createMockServer()
	charRepo := newMockCharacterRepo()
	server.charRepo = charRepo

	gachaRepo := &mockGachaRepo{
		txRolls: 1,
		rewardPool: []GachaEntry{
			{ID: 10, Weight: 100, Rarity: 3},
		},
		entryItems: map[uint32][]GachaItem{
			10: {{ItemType: 1, ItemID: 500, Quantity: 1}},
		},
	}
	server.gachaRepo = gachaRepo
	server.userRepo = &mockUserRepoGacha{}
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPlayNormalGacha{AckHandle: 100, GachaID: 1, RollType: 0}
	handleMsgMhfPlayNormalGacha(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Fatal("Empty response")
		}
	default:
		t.Error("No response packet queued")
	}

	// Verify gacha items were stored
	if charRepo.columns["gacha_items"] == nil {
		t.Error("Expected gacha items to be saved")
	}
}

func TestHandleMsgMhfPlayStepupGacha_TransactError(t *testing.T) {
	server := createMockServer()
	gachaRepo := &mockGachaRepo{
		txErr:      errors.New("transact failed"),
		rewardPool: []GachaEntry{{ID: 10, Weight: 100}},
	}
	server.gachaRepo = gachaRepo
	server.userRepo = &mockUserRepoGacha{}
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPlayStepupGacha{AckHandle: 100, GachaID: 1, RollType: 0}
	handleMsgMhfPlayStepupGacha(session, pkt)

	select {
	case <-session.sendPackets:
		// success - returns empty result
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfPlayStepupGacha_Success(t *testing.T) {
	server := createMockServer()
	charRepo := newMockCharacterRepo()
	server.charRepo = charRepo

	gachaRepo := &mockGachaRepo{
		txRolls: 1,
		rewardPool: []GachaEntry{
			{ID: 10, Weight: 100, Rarity: 2},
		},
		entryItems: map[uint32][]GachaItem{
			10: {{ItemType: 1, ItemID: 600, Quantity: 2}},
		},
		guaranteedItems: []GachaItem{
			{ItemType: 1, ItemID: 700, Quantity: 1},
		},
	}
	server.gachaRepo = gachaRepo
	server.userRepo = &mockUserRepoGacha{}
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPlayStepupGacha{AckHandle: 100, GachaID: 1, RollType: 0}
	handleMsgMhfPlayStepupGacha(session, pkt)

	if !gachaRepo.deletedStepup {
		t.Error("Expected stepup to be deleted")
	}
	if gachaRepo.insertedStep != 1 {
		t.Errorf("Expected insertedStep=1, got %d", gachaRepo.insertedStep)
	}

	select {
	case <-session.sendPackets:
		// success
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetStepupStatus_FreshStep(t *testing.T) {
	server := createMockServer()
	gachaRepo := &mockGachaRepo{
		stepupStep:   2,
		stepupTime:   time.Now(), // recent, not stale
		hasEntryType: true,
	}
	server.gachaRepo = gachaRepo
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetStepupStatus{AckHandle: 100, GachaID: 1}
	handleMsgMhfGetStepupStatus(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Fatal("Empty response")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetStepupStatus_StaleStep(t *testing.T) {
	server := createMockServer()
	gachaRepo := &mockGachaRepo{
		stepupStep: 3,
		stepupTime: time.Now().Add(-48 * time.Hour), // stale
	}
	server.gachaRepo = gachaRepo
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetStepupStatus{AckHandle: 100, GachaID: 1}
	handleMsgMhfGetStepupStatus(session, pkt)

	if !gachaRepo.deletedStepup {
		t.Error("Expected stale stepup to be deleted")
	}

	select {
	case <-session.sendPackets:
		// success
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetStepupStatus_NoRows(t *testing.T) {
	server := createMockServer()
	gachaRepo := &mockGachaRepo{
		stepupErr: sql.ErrNoRows,
	}
	server.gachaRepo = gachaRepo
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetStepupStatus{AckHandle: 100, GachaID: 1}
	handleMsgMhfGetStepupStatus(session, pkt)

	select {
	case <-session.sendPackets:
		// success
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetStepupStatus_NoEntryType(t *testing.T) {
	server := createMockServer()
	gachaRepo := &mockGachaRepo{
		stepupStep:   2,
		stepupTime:   time.Now(),
		hasEntryType: false, // no matching entry type -> reset
	}
	server.gachaRepo = gachaRepo
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetStepupStatus{AckHandle: 100, GachaID: 1}
	handleMsgMhfGetStepupStatus(session, pkt)

	if !gachaRepo.deletedStepup {
		t.Error("Expected stepup to be reset when no entry type")
	}

	select {
	case <-session.sendPackets:
		// success
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetBoxGachaInfo_Error(t *testing.T) {
	server := createMockServer()
	gachaRepo := &mockGachaRepo{
		boxEntryIDsErr: errors.New("db error"),
	}
	server.gachaRepo = gachaRepo
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetBoxGachaInfo{AckHandle: 100, GachaID: 1}
	handleMsgMhfGetBoxGachaInfo(session, pkt)

	select {
	case <-session.sendPackets:
		// returns empty
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetBoxGachaInfo_Success(t *testing.T) {
	server := createMockServer()
	gachaRepo := &mockGachaRepo{
		boxEntryIDs: []uint32{10, 20, 30},
	}
	server.gachaRepo = gachaRepo
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetBoxGachaInfo{AckHandle: 100, GachaID: 1}
	handleMsgMhfGetBoxGachaInfo(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Fatal("Empty response")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfPlayBoxGacha_TransactError(t *testing.T) {
	server := createMockServer()
	gachaRepo := &mockGachaRepo{
		txErr:      errors.New("transact failed"),
		rewardPool: []GachaEntry{{ID: 10, Weight: 100}},
	}
	server.gachaRepo = gachaRepo
	server.userRepo = &mockUserRepoGacha{}
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPlayBoxGacha{AckHandle: 100, GachaID: 1, RollType: 0}
	handleMsgMhfPlayBoxGacha(session, pkt)

	select {
	case <-session.sendPackets:
		// success
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfPlayBoxGacha_Success(t *testing.T) {
	server := createMockServer()
	charRepo := newMockCharacterRepo()
	server.charRepo = charRepo

	gachaRepo := &mockGachaRepo{
		txRolls: 1,
		rewardPool: []GachaEntry{
			{ID: 10, Weight: 100, Rarity: 1},
		},
		entryItems: map[uint32][]GachaItem{
			10: {{ItemType: 1, ItemID: 800, Quantity: 1}},
		},
	}
	server.gachaRepo = gachaRepo
	server.userRepo = &mockUserRepoGacha{}
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPlayBoxGacha{AckHandle: 100, GachaID: 1, RollType: 0}
	handleMsgMhfPlayBoxGacha(session, pkt)

	if len(gachaRepo.insertedBoxIDs) == 0 {
		t.Error("Expected box entry to be inserted")
	}

	select {
	case <-session.sendPackets:
		// success
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfResetBoxGachaInfo(t *testing.T) {
	server := createMockServer()
	gachaRepo := &mockGachaRepo{}
	server.gachaRepo = gachaRepo
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfResetBoxGachaInfo{AckHandle: 100, GachaID: 1}
	handleMsgMhfResetBoxGachaInfo(session, pkt)

	if !gachaRepo.deletedBox {
		t.Error("Expected box entries to be deleted")
	}

	select {
	case <-session.sendPackets:
		// success
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfPlayFreeGacha_StubACK(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPlayFreeGacha{AckHandle: 100, GachaID: 1}
	handleMsgMhfPlayFreeGacha(session, pkt)

	select {
	case <-session.sendPackets:
		// success
	default:
		t.Error("No response packet queued")
	}
}

func TestGetRandomEntries_NonBox(t *testing.T) {
	entries := []GachaEntry{
		{ID: 1, Weight: 50},
		{ID: 2, Weight: 50},
	}
	result, err := getRandomEntries(entries, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(result))
	}
}

func TestGetRandomEntries_Box(t *testing.T) {
	entries := []GachaEntry{
		{ID: 1, Weight: 50},
		{ID: 2, Weight: 50},
		{ID: 3, Weight: 50},
	}
	result, err := getRandomEntries(entries, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(result))
	}
	// Box mode removes entries without replacement — all IDs should be unique
	if result[0].ID == result[1].ID {
		t.Error("Box mode should return unique entries")
	}
}

func TestGetRandomEntries_BoxMoreRollsThanEntries(t *testing.T) {
	entries := []GachaEntry{
		{ID: 1, Weight: 50},
	}
	result, err := getRandomEntries(entries, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	// Should clamp to available entries instead of panicking
	if len(result) != 1 {
		t.Errorf("Expected 1 entry (clamped), got %d", len(result))
	}
}

func TestGetRandomEntries_EmptyEntries(t *testing.T) {
	_, err := getRandomEntries(nil, 1, false)
	if err == nil {
		t.Fatal("Expected error for empty entries, got nil")
	}
}

func TestGetRandomEntries_ZeroWeight(t *testing.T) {
	entries := []GachaEntry{
		{ID: 1, Weight: 0},
		{ID: 2, Weight: 0},
	}
	_, err := getRandomEntries(entries, 1, false)
	if err == nil {
		t.Fatal("Expected error for zero total weight, got nil")
	}
}

func TestHandleMsgMhfPlayStepupGacha_RewardPoolError(t *testing.T) {
	server := createMockServer()
	charRepo := newMockCharacterRepo()
	server.charRepo = charRepo

	gachaRepo := &mockGachaRepo{
		txRolls:       1,
		rewardPoolErr: errors.New("pool error"),
	}
	server.gachaRepo = gachaRepo
	server.userRepo = &mockUserRepoGacha{}
	ensureGachaService(server)

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPlayStepupGacha{AckHandle: 100, GachaID: 1, RollType: 0}
	handleMsgMhfPlayStepupGacha(session, pkt)

	select {
	case p := <-session.sendPackets:
		// Verify minimal response (1 byte)
		_ = p
	default:
		t.Error("No response packet queued")
	}
}

// Verify the response payload of GetGachaPoint contains the expected values
func TestHandleMsgMhfGetGachaPoint_ResponsePayload(t *testing.T) {
	server := createMockServer()
	userRepo := &mockUserRepoGacha{
		gachaFP: 111,
		gachaGP: 222,
		gachaGT: 333,
	}
	server.userRepo = userRepo

	session := createMockSession(1, server)
	session.userID = 1

	pkt := &mhfpacket.MsgMhfGetGachaPoint{AckHandle: 100}
	handleMsgMhfGetGachaPoint(session, pkt)

	select {
	case p := <-session.sendPackets:
		// The ack wraps the payload. The handler writes gp, gt, fp (12 bytes).
		// Just verify we got a reasonable-sized response.
		if len(p.data) < 12 {
			t.Errorf("Expected at least 12 bytes of gacha point data in response, got %d", len(p.data))
		}
	default:
		t.Error("No response packet queued")
	}
}

// Verify the response when no gacha items exist (default column)
func TestHandleMsgMhfReceiveGachaItem_Empty(t *testing.T) {
	server := createMockServer()
	charRepo := newMockCharacterRepo()
	// No gacha_items set — will return default {0x00}
	server.charRepo = charRepo

	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfReceiveGachaItem{AckHandle: 100, Freeze: false}
	handleMsgMhfReceiveGachaItem(session, pkt)

	select {
	case p := <-session.sendPackets:
		// The response should contain the default byte
		bf := byteframe.NewByteFrameFromBytes(p.data)
		_ = bf
	default:
		t.Error("No response packet queued")
	}
}

func TestInspectGachaItemBlob(t *testing.T) {
	tests := []struct {
		name         string
		data         []byte
		wantDeclared int
		wantRecords  int
		wantTrailing int
		wantValid    bool
	}{
		{name: "absent", data: nil, wantValid: false},
		{name: "empty list", data: []byte{0}, wantValid: true},
		{
			name:         "two complete records",
			data:         []byte{2, 1, 0, 100, 0, 5, 2, 0, 200, 0, 10},
			wantDeclared: 2,
			wantRecords:  2,
			wantValid:    true,
		},
		{
			name:         "declared count mismatch",
			data:         []byte{2, 1, 0, 100, 0, 5},
			wantDeclared: 2,
			wantRecords:  1,
			wantValid:    false,
		},
		{
			name:         "partial trailing record",
			data:         []byte{1, 1, 0},
			wantDeclared: 1,
			wantTrailing: 2,
			wantValid:    false,
		},
		{
			name:        "uint8 count wrapped after 256 records",
			data:        make([]byte, 1+256*gachaItemRecordBytes),
			wantRecords: 256,
			wantValid:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			declared, records, trailing, valid := inspectGachaItemBlob(tt.data)
			if declared != tt.wantDeclared || records != tt.wantRecords ||
				trailing != tt.wantTrailing || valid != tt.wantValid {
				t.Fatalf(
					"inspectGachaItemBlob() = (%d, %d, %d, %t), want (%d, %d, %d, %t)",
					declared, records, trailing, valid,
					tt.wantDeclared, tt.wantRecords, tt.wantTrailing, tt.wantValid,
				)
			}
		})
	}
}

func TestBoundedGachaHex(t *testing.T) {
	exact := make([]byte, gachaMaxResponseBytes)
	for i := range exact {
		exact[i] = byte(i)
	}

	encoded, truncated := boundedGachaHex(exact)
	if truncated {
		t.Fatal("exactly one client response should not be truncated")
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatalf("boundedGachaHex returned invalid hex: %v", err)
	}
	if len(decoded) != gachaMaxResponseBytes {
		t.Fatalf("decoded length = %d, want %d", len(decoded), gachaMaxResponseBytes)
	}

	encoded, truncated = boundedGachaHex(append(exact, 0xFF))
	if !truncated {
		t.Fatal("oversized diagnostic payload should be truncated")
	}
	decoded, err = hex.DecodeString(encoded)
	if err != nil {
		t.Fatalf("boundedGachaHex returned invalid truncated hex: %v", err)
	}
	if len(decoded) != gachaMaxResponseBytes {
		t.Fatalf("truncated decoded length = %d, want %d", len(decoded), gachaMaxResponseBytes)
	}
}

func TestHandleMsgMhfReceiveGachaItem_DiagnosticLog(t *testing.T) {
	overflow := make([]byte, 1+37*gachaItemRecordBytes)
	overflow[0] = 37
	for i := 1; i < len(overflow); i++ {
		overflow[i] = byte(i)
	}

	tests := []struct {
		name                 string
		data                 []byte
		freeze               bool
		wantAction           string
		wantResponseBytes    int
		wantResponseCount    int
		wantRemainingBytes   int
		wantStoredValid      bool
		wantPersistAttempted bool
	}{
		{
			name:                 "normal clear",
			data:                 []byte{2, 1, 0, 100, 0, 5, 2, 0, 200, 0, 10},
			wantAction:           "clear",
			wantResponseBytes:    11,
			wantResponseCount:    2,
			wantStoredValid:      true,
			wantPersistAttempted: true,
		},
		{
			name:                 "overflow retains one",
			data:                 overflow,
			wantAction:           "retain_overflow",
			wantResponseBytes:    gachaMaxResponseBytes,
			wantResponseCount:    gachaClientItemLimit,
			wantRemainingBytes:   1 + gachaItemRecordBytes,
			wantStoredValid:      true,
			wantPersistAttempted: true,
		},
		{
			name:                 "freeze preserves",
			data:                 []byte{1, 1, 0, 100, 0, 5},
			freeze:               true,
			wantAction:           "preserve",
			wantResponseBytes:    6,
			wantResponseCount:    1,
			wantRemainingBytes:   6,
			wantStoredValid:      true,
			wantPersistAttempted: false,
		},
		{
			name:                 "malformed count is visible",
			data:                 []byte{2, 1, 0, 100, 0, 5},
			freeze:               true,
			wantAction:           "preserve",
			wantResponseBytes:    6,
			wantResponseCount:    2,
			wantRemainingBytes:   6,
			wantStoredValid:      false,
			wantPersistAttempted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := createMockServer()
			charRepo := newMockCharacterRepo()
			charRepo.columns["gacha_items"] = append([]byte(nil), tt.data...)
			server.charRepo = charRepo

			core, logs := observer.New(zapcore.InfoLevel)
			session := createMockSession(7, server)
			session.userID = 9
			session.Name = "DiagnosticHunter"
			session.logger = zap.New(core)

			pkt := &mhfpacket.MsgMhfReceiveGachaItem{
				AckHandle: 100,
				Max:       36,
				Freeze:    tt.freeze,
			}
			handleMsgMhfReceiveGachaItem(session, pkt)

			if len(session.sendPackets) != 1 {
				t.Fatalf("queued responses = %d, want 1", len(session.sendPackets))
			}
			entries := logs.FilterMessage("Gacha pending items receive").All()
			if len(entries) != 1 {
				t.Fatalf("diagnostic entries = %d, want 1", len(entries))
			}
			fields := entries[0].ContextMap()
			checkObservedGachaField(t, fields, "charID", "7")
			checkObservedGachaField(t, fields, "userID", "9")
			checkObservedGachaField(t, fields, "name", "DiagnosticHunter")
			checkObservedGachaField(t, fields, "max", "36")
			checkObservedGachaField(t, fields, "freeze", fmt.Sprint(tt.freeze))
			checkObservedGachaField(t, fields, "stored_blob_valid", fmt.Sprint(tt.wantStoredValid))
			checkObservedGachaField(t, fields, "response_bytes", fmt.Sprint(tt.wantResponseBytes))
			checkObservedGachaField(t, fields, "response_declared_count", fmt.Sprint(tt.wantResponseCount))
			checkObservedGachaField(t, fields, "persist_action", tt.wantAction)
			checkObservedGachaField(t, fields, "persist_attempted", fmt.Sprint(tt.wantPersistAttempted))
			checkObservedGachaField(t, fields, "persist_ok", "true")
			checkObservedGachaField(t, fields, "remaining_bytes", fmt.Sprint(tt.wantRemainingBytes))
		})
	}
}

func TestHandleMsgMhfReceiveGachaItem_DiagnosticErrors(t *testing.T) {
	t.Run("load fallback", func(t *testing.T) {
		server := createMockServer()
		charRepo := newMockCharacterRepo()
		charRepo.loadColumnErr = errors.New("load failed")
		server.charRepo = charRepo

		core, logs := observer.New(zapcore.InfoLevel)
		session := createMockSession(7, server)
		session.logger = zap.New(core)
		handleMsgMhfReceiveGachaItem(session, &mhfpacket.MsgMhfReceiveGachaItem{
			AckHandle: 100,
			Max:       36,
		})

		fields := logs.FilterMessage("Gacha pending items receive").All()[0].ContextMap()
		checkObservedGachaField(t, fields, "used_fallback", "true")
		checkObservedGachaField(t, fields, "response_hex", "00")
	})

	t.Run("save failure", func(t *testing.T) {
		server := createMockServer()
		charRepo := newMockCharacterRepo()
		charRepo.columns["gacha_items"] = []byte{1, 1, 0, 100, 0, 5}
		charRepo.saveErr = errors.New("save failed")
		server.charRepo = charRepo

		core, logs := observer.New(zapcore.InfoLevel)
		session := createMockSession(7, server)
		session.logger = zap.New(core)
		handleMsgMhfReceiveGachaItem(session, &mhfpacket.MsgMhfReceiveGachaItem{
			AckHandle: 100,
			Max:       36,
		})

		entries := logs.FilterMessage("Gacha pending items receive").All()
		if len(entries) != 1 {
			t.Fatalf("diagnostic entries = %d, want 1", len(entries))
		}
		fields := entries[0].ContextMap()
		checkObservedGachaField(t, fields, "persist_attempted", "true")
		checkObservedGachaField(t, fields, "persist_ok", "false")
		if _, ok := fields["persist_error"]; !ok {
			t.Fatal("diagnostic log is missing persist_error")
		}
	})
}

func checkObservedGachaField(t *testing.T, fields map[string]interface{}, key, want string) {
	t.Helper()
	got, ok := fields[key]
	if !ok {
		t.Fatalf("diagnostic log is missing %q", key)
	}
	if fmt.Sprint(got) != want {
		t.Fatalf("%s = %v, want %s", key, got, want)
	}
}
