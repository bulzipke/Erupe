package channelserver

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

func newTestGachaService(gr GachaRepo, ur UserRepo, cr CharacterRepo) *GachaService {
	logger, _ := zap.NewDevelopment()
	return NewGachaService(gr, ur, cr, logger, 100000)
}

func TestGachaServiceSaveItemsPreservesWrappedPendingRecords(t *testing.T) {
	tests := []struct {
		name          string
		existingCount int
		wantCountByte byte
		wantActual    int
	}{
		{
			name:          "crosses 255 record boundary",
			existingCount: 255,
			wantCountByte: 0,
			wantActual:    256,
		},
		{
			name:          "appends after wrapped header",
			existingCount: 256,
			wantCountByte: 1,
			wantActual:    257,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			charRepo := newMockCharacterRepo()
			existing := makeSequentialGachaBlob(tt.existingCount)
			charRepo.columns["gacha_items"] = append([]byte(nil), existing...)
			svc := newTestGachaService(nil, nil, charRepo)
			added := GachaItem{ItemType: 9, ItemID: 65000, Quantity: 2}

			svc.saveGachaItems(7, []GachaItem{added})

			saved := charRepo.columns["gacha_items"]
			if len(saved) != 1+tt.wantActual*gachaItemRecordBytes {
				t.Fatalf("saved bytes = %d, want %d",
					len(saved), 1+tt.wantActual*gachaItemRecordBytes)
			}
			if saved[0] != tt.wantCountByte {
				t.Fatalf("count byte = %d, want %d", saved[0], tt.wantCountByte)
			}
			_, actual, trailing, valid := inspectGachaItemBlob(saved)
			if actual != tt.wantActual || trailing != 0 || !valid {
				t.Fatalf("saved blob actual=%d trailing=%d valid=%t, want actual=%d valid",
					actual, trailing, valid, tt.wantActual)
			}

			wantAdded := make([]byte, gachaItemRecordBytes)
			wantAdded[0] = added.ItemType
			binary.BigEndian.PutUint16(wantAdded[1:3], added.ItemID)
			binary.BigEndian.PutUint16(wantAdded[3:5], added.Quantity)
			if !bytes.Equal(saved[1:1+gachaItemRecordBytes], wantAdded) {
				t.Fatalf("prepended item = %x, want %x", saved[1:6], wantAdded)
			}
			if !bytes.Equal(saved[1+gachaItemRecordBytes:], existing[1:]) {
				t.Fatal("existing pending records were changed while appending")
			}
		})
	}
}

func TestGachaServiceSaveItemsStillStoresNewRewardsAfterLoadFailure(t *testing.T) {
	charRepo := newMockCharacterRepo()
	charRepo.loadColumnErr = errors.New("load failed")
	svc := newTestGachaService(nil, nil, charRepo)
	added := GachaItem{ItemType: 9, ItemID: 65000, Quantity: 2}

	svc.saveGachaItems(7, []GachaItem{added})

	saved := charRepo.columns["gacha_items"]
	if len(saved) != 1+gachaItemRecordBytes || saved[0] != 1 {
		t.Fatalf("saved blob = %x, want one newly rolled reward", saved)
	}
	if saved[1] != added.ItemType ||
		binary.BigEndian.Uint16(saved[2:4]) != added.ItemID ||
		binary.BigEndian.Uint16(saved[4:6]) != added.Quantity {
		t.Fatalf("saved reward = %x, want %+v", saved[1:], added)
	}
}

func TestGachaService_PlayNormalGacha(t *testing.T) {
	tests := []struct {
		name      string
		txErr     error
		poolErr   error
		txRolls   int
		pool      []GachaEntry
		items     map[uint32][]GachaItem
		wantErr   bool
		wantCount int
	}{
		{
			name:    "transact error",
			pool:    []GachaEntry{{ID: 10, Weight: 100}},
			txErr:   errors.New("tx fail"),
			wantErr: true,
		},
		{
			name:    "reward pool error",
			txRolls: 1,
			poolErr: errors.New("pool fail"),
			wantErr: true,
		},
		{
			name:    "empty reward pool",
			wantErr: true,
		},
		{
			name:    "success single roll",
			txRolls: 1,
			pool:    []GachaEntry{{ID: 10, Weight: 100, Rarity: 3}},
			items: map[uint32][]GachaItem{
				10: {{ItemType: 1, ItemID: 500, Quantity: 1}},
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gr := &mockGachaRepo{
				txRolls:       tt.txRolls,
				txErr:         tt.txErr,
				rewardPool:    tt.pool,
				rewardPoolErr: tt.poolErr,
				entryItems:    tt.items,
			}
			cr := newMockCharacterRepo()
			svc := newTestGachaService(gr, &mockUserRepoGacha{}, cr)

			result, err := svc.PlayNormalGacha(1, 1, 1, 0)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if len(result.Rewards) != tt.wantCount {
				t.Errorf("Rewards count = %d, want %d", len(result.Rewards), tt.wantCount)
			}
			// Verify items were saved
			if tt.wantCount > 0 && cr.columns["gacha_items"] == nil {
				t.Error("Expected gacha items to be saved")
			}
		})
	}
}

func TestGachaService_PlayStepupGacha(t *testing.T) {
	tests := []struct {
		name               string
		txErr              error
		poolErr            error
		txRolls            int
		pool               []GachaEntry
		items              map[uint32][]GachaItem
		guaranteed         []GachaItem
		wantErr            bool
		wantRandomCount    int
		wantGuaranteeCount int
	}{
		{
			name:    "transact error",
			pool:    []GachaEntry{{ID: 10, Weight: 100}},
			txErr:   errors.New("tx fail"),
			wantErr: true,
		},
		{
			name:    "reward pool error",
			txRolls: 1,
			poolErr: errors.New("pool fail"),
			wantErr: true,
		},
		{
			name:    "empty reward pool",
			wantErr: true,
		},
		{
			name:    "success with guaranteed",
			txRolls: 1,
			pool:    []GachaEntry{{ID: 10, Weight: 100, Rarity: 2}},
			items: map[uint32][]GachaItem{
				10: {{ItemType: 1, ItemID: 600, Quantity: 2}},
			},
			guaranteed:         []GachaItem{{ItemType: 1, ItemID: 700, Quantity: 1}},
			wantRandomCount:    1,
			wantGuaranteeCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gr := &mockGachaRepo{
				txRolls:         tt.txRolls,
				txErr:           tt.txErr,
				rewardPool:      tt.pool,
				rewardPoolErr:   tt.poolErr,
				entryItems:      tt.items,
				guaranteedItems: tt.guaranteed,
			}
			cr := newMockCharacterRepo()
			svc := newTestGachaService(gr, &mockUserRepoGacha{}, cr)

			result, err := svc.PlayStepupGacha(1, 1, 1, 0)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if len(result.RandomRewards) != tt.wantRandomCount {
				t.Errorf("RandomRewards count = %d, want %d", len(result.RandomRewards), tt.wantRandomCount)
			}
			if len(result.GuaranteedRewards) != tt.wantGuaranteeCount {
				t.Errorf("GuaranteedRewards count = %d, want %d", len(result.GuaranteedRewards), tt.wantGuaranteeCount)
			}
			if !gr.deletedStepup {
				t.Error("Expected stepup to be deleted")
			}
			if gr.insertedStep != 1 {
				t.Errorf("Expected insertedStep=1, got %d", gr.insertedStep)
			}
		})
	}
}

func TestGachaService_PlayBoxGacha(t *testing.T) {
	gr := &mockGachaRepo{
		txRolls: 1,
		rewardPool: []GachaEntry{
			{ID: 10, Weight: 100, Rarity: 1},
		},
		entryItems: map[uint32][]GachaItem{
			10: {{ItemType: 1, ItemID: 800, Quantity: 1}},
		},
	}
	cr := newMockCharacterRepo()
	svc := newTestGachaService(gr, &mockUserRepoGacha{}, cr)

	result, err := svc.PlayBoxGacha(1, 1, 1, 0)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(result.Rewards) != 1 {
		t.Errorf("Rewards count = %d, want 1", len(result.Rewards))
	}
	if len(gr.insertedBoxIDs) == 0 {
		t.Error("Expected box entry to be inserted")
	}
}

func TestGachaService_GetStepupStatus(t *testing.T) {
	now := time.Date(2025, 6, 15, 15, 0, 0, 0, time.UTC) // 3 PM
	midday := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		step        uint8
		createdAt   time.Time
		stepupErr   error
		hasEntry    bool
		wantStep    uint8
		wantDeleted bool
	}{
		{
			name:      "no rows",
			stepupErr: sql.ErrNoRows,
			wantStep:  0,
		},
		{
			name:        "fresh with entry",
			step:        2,
			createdAt:   now, // after midday
			hasEntry:    true,
			wantStep:    2,
			wantDeleted: false,
		},
		{
			name:        "stale (before midday)",
			step:        3,
			createdAt:   midday.Add(-1 * time.Hour), // before midday boundary
			wantStep:    0,
			wantDeleted: true,
		},
		{
			name:        "fresh but no entry type",
			step:        2,
			createdAt:   now,
			hasEntry:    false,
			wantStep:    0,
			wantDeleted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gr := &mockGachaRepo{
				stepupStep:   tt.step,
				stepupTime:   tt.createdAt,
				stepupErr:    tt.stepupErr,
				hasEntryType: tt.hasEntry,
			}
			svc := newTestGachaService(gr, &mockUserRepoGacha{}, newMockCharacterRepo())

			status, err := svc.GetStepupStatus(1, 1, now)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if status.Step != tt.wantStep {
				t.Errorf("Step = %d, want %d", status.Step, tt.wantStep)
			}
			if gr.deletedStepup != tt.wantDeleted {
				t.Errorf("deletedStepup = %v, want %v", gr.deletedStepup, tt.wantDeleted)
			}
		})
	}
}

func TestGachaService_GetBoxInfo(t *testing.T) {
	gr := &mockGachaRepo{
		boxEntryIDs: []uint32{10, 20, 30},
	}
	svc := newTestGachaService(gr, &mockUserRepoGacha{}, newMockCharacterRepo())

	ids, err := svc.GetBoxInfo(1, 1)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(ids) != 3 {
		t.Errorf("Got %d entry IDs, want 3", len(ids))
	}
}

func TestGachaService_ResetBox(t *testing.T) {
	gr := &mockGachaRepo{}
	svc := newTestGachaService(gr, &mockUserRepoGacha{}, newMockCharacterRepo())

	err := svc.ResetBox(1, 1)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !gr.deletedBox {
		t.Error("Expected box entries to be deleted")
	}
}

func TestGachaService_Transact_NetcafeCoins(t *testing.T) {
	cr := newMockCharacterRepo()
	cr.ints["netcafe_points"] = 5000
	gr := &mockGachaRepo{
		txItemType:   17,
		txItemNumber: 100,
		txRolls:      1,
	}
	svc := newTestGachaService(gr, &mockUserRepoGacha{}, cr)

	rolls, err := svc.transact(1, 1, 1, 0)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if rolls != 1 {
		t.Errorf("Rolls = %d, want 1", rolls)
	}
	// Netcafe points should have been reduced
	if cr.ints["netcafe_points"] != 4900 {
		t.Errorf("Netcafe points = %d, want 4900", cr.ints["netcafe_points"])
	}
}

func TestGachaService_SpendGachaCoin_TrialFirst(t *testing.T) {
	ur := &mockUserRepoGacha{trialCoins: 100}
	svc := newTestGachaService(&mockGachaRepo{}, ur, newMockCharacterRepo())

	svc.spendGachaCoin(1, 50)
	// Should have used trial coins, not premium
}

func TestGachaService_SpendGachaCoin_PremiumFallback(t *testing.T) {
	ur := &mockUserRepoGacha{trialCoins: 10}
	svc := newTestGachaService(&mockGachaRepo{}, ur, newMockCharacterRepo())

	svc.spendGachaCoin(1, 50)
	// Should have used premium coins since trial < quantity
}
