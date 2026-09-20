package channelserver

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"erupe-ce/server/channelserver/compression/nullcomp"
)

func testDivaItemOffer(id uint32) DivaRewardOffer {
	return DivaRewardOffer{ID: id, EventID: 1, ItemType: 7, ItemID: 0x3694, Quantity: 5}
}

func testDivaGPOffer(id uint32) DivaRewardOffer {
	return DivaRewardOffer{ID: id, EventID: 1, RewardType: 2, ItemType: 26, Quantity: 12000}
}

func TestDivaRewardSessionSnapshotsAndCompletion(t *testing.T) {
	s := &Session{}
	if s.hasPendingDivaRewardClaims() || len(s.pendingDivaRewardClaims(7)) != 0 {
		t.Fatal("new session has pending receipts")
	}
	offers := []DivaRewardOffer{testDivaItemOffer(3), testDivaItemOffer(1), testDivaGPOffer(2)}
	if err := s.stageDivaRewardClaims(offers); err != nil {
		t.Fatal(err)
	}
	if err := s.stageDivaRewardClaims(offers); err != nil {
		t.Fatalf("identical claim retry: %v", err)
	}
	ids := s.pendingDivaRewardClaims(7)
	if !reflect.DeepEqual(ids, []uint32{1, 3}) {
		t.Fatalf("item snapshot = %v", ids)
	}
	ids[0] = 99
	if !reflect.DeepEqual(s.pendingDivaRewardClaims(7), []uint32{1, 3}) {
		t.Fatal("caller mutated pending state via snapshot")
	}
	s.completeDivaRewardClaims(7, []uint32{2})
	if len(s.pendingDivaRewardClaims(26)) != 1 {
		t.Fatal("item completion cleared GP receipt")
	}
	if err := s.stageDivaRewardClaims([]DivaRewardOffer{testDivaItemOffer(4)}); err != nil {
		t.Fatal(err)
	}
	s.completeDivaRewardClaims(7, []uint32{1, 3})
	if !reflect.DeepEqual(s.pendingDivaRewardClaims(7), []uint32{4}) {
		t.Fatal("completion cleared a later staged receipt")
	}
	s.completeDivaRewardClaims(7, []uint32{4})
	if !s.hasPendingDivaRewardClaims() {
		t.Fatal("GP receipt should remain pending")
	}
	s.completeDivaRewardClaims(26, []uint32{2})
	if s.hasPendingDivaRewardClaims() {
		t.Fatal("committed receipts remain pending")
	}
}

func TestDivaRewardSessionRejectsInvalidBatchAtomically(t *testing.T) {
	for _, tc := range []struct {
		name  string
		offer DivaRewardOffer
	}{
		{"zero ID", DivaRewardOffer{ItemType: 7, ItemID: 1, Quantity: 1}},
		{"unknown type", DivaRewardOffer{ID: 2, ItemType: 8, ItemID: 1, Quantity: 1}},
		{"zero quantity", DivaRewardOffer{ID: 2, ItemType: 7, ItemID: 1}},
		{"zero item", DivaRewardOffer{ID: 2, ItemType: 7, Quantity: 1}},
		{"metadata conflict", DivaRewardOffer{ID: 1, ItemType: 7, ItemID: 1, Quantity: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{}
			if err := s.stageDivaRewardClaims([]DivaRewardOffer{testDivaItemOffer(1)}); err != nil {
				t.Fatal(err)
			}
			if err := s.stageDivaRewardClaims([]DivaRewardOffer{testDivaItemOffer(3), tc.offer}); err == nil {
				t.Fatal("invalid batch accepted")
			}
			if !reflect.DeepEqual(s.pendingDivaRewardClaims(7), []uint32{1}) {
				t.Fatal("partial batch was staged")
			}
		})
	}
	s := &Session{}
	if err := s.stageDivaRewardClaims([]DivaRewardOffer{testDivaItemOffer(1), testDivaItemOffer(1)}); err == nil {
		t.Fatal("duplicate batch accepted")
	}
	offers := make([]DivaRewardOffer, 32)
	for i := range offers {
		offers[i] = testDivaItemOffer(uint32(i + 1))
	}
	if err := s.stageDivaRewardClaims(offers); err != nil {
		t.Fatal(err)
	}
	if err := s.stageDivaRewardClaims([]DivaRewardOffer{testDivaItemOffer(33)}); err == nil {
		t.Fatal("pending limit exceeded")
	}
	if len(s.pendingDivaRewardClaims(7)) != 32 {
		t.Fatal("capacity failure changed pending state")
	}
}

func TestDivaRewardSessionConcurrentAccess(t *testing.T) {
	s := &Session{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := s.stageDivaRewardClaims([]DivaRewardOffer{testDivaItemOffer(1)}); err != nil {
					t.Error(err)
					return
				}
				_ = s.hasPendingDivaRewardClaims()
				s.completeDivaRewardClaims(7, s.pendingDivaRewardClaims(7))
			}
		}()
	}
	wg.Wait()
}

func TestDivaRewardMercenarySaveKeepsFailedReceipts(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			server := createMockServer()
			repo := newMockCharacterRepo()
			server.charRepo = repo
			if fail {
				repo.divaGCPSaveErr = errors.New("save failed")
			}
			s := createMockSession(42, server)
			if err := s.stageDivaRewardClaims([]DivaRewardOffer{testDivaItemOffer(1), testDivaGPOffer(2)}); err != nil {
				t.Fatal(err)
			}
			handleMsgMhfSaveMercenary(s, &mhfpacket.MsgMhfSaveMercenary{AckHandle: 5, GCP: 15000, PactMercID: 9})
			ack := readAck(t, s)
			if (ack.ErrorCode != 0) != fail {
				t.Fatalf("ack = %+v", ack)
			}
			if repo.divaGCPCharID != 42 || repo.divaGCPValue != 15000 || repo.divaPactID != 9 || !reflect.DeepEqual(repo.divaGCPRewardIDs, []uint32{2}) {
				t.Fatal("incorrect atomic GP save")
			}
			if (len(s.pendingDivaRewardClaims(26)) != 0) != fail {
				t.Fatal("incorrect pending GP state")
			}
			if !reflect.DeepEqual(s.pendingDivaRewardClaims(7), []uint32{1}) {
				t.Fatal("GP save consumed item receipt")
			}
		})
	}
}

func TestDivaRewardMercenaryOversizeRetainsReceipts(t *testing.T) {
	server := createMockServer()
	repo := newMockCharacterRepo()
	server.charRepo = repo
	s := createMockSession(42, server)
	if err := s.stageDivaRewardClaims([]DivaRewardOffer{testDivaGPOffer(1)}); err != nil {
		t.Fatal(err)
	}
	handleMsgMhfSaveMercenary(s, &mhfpacket.MsgMhfSaveMercenary{AckHandle: 7, MercData: make([]byte, 65537)})
	if ack := readAck(t, s); ack.ErrorCode == 0 {
		t.Fatal("oversize save acknowledged")
	}
	if !s.hasPendingDivaRewardClaims() || len(repo.divaGCPRewardIDs) != 0 {
		t.Fatal("oversize request affected receipt")
	}
}

type divaMercenaryFailureRepo struct {
	CharacterRepo
	blobErr      error
	plainGPCalls int
}

func (r *divaMercenaryFailureRepo) SaveMercenary(uint32, []byte, uint32) error {
	return r.blobErr
}

func (r *divaMercenaryFailureRepo) UpdateGCPAndPact(uint32, uint32, uint32) error {
	r.plainGPCalls++
	return nil
}

func TestDivaRewardMercenaryBlobFailureRetainsReceipts(t *testing.T) {
	server := createMockServer()
	base := newMockCharacterRepo()
	repo := &divaMercenaryFailureRepo{CharacterRepo: base, blobErr: errors.New("blob save failed")}
	server.charRepo = repo
	s := createMockSession(42, server)
	if err := s.stageDivaRewardClaims([]DivaRewardOffer{testDivaGPOffer(1)}); err != nil {
		t.Fatal(err)
	}
	handleMsgMhfSaveMercenary(s, &mhfpacket.MsgMhfSaveMercenary{AckHandle: 7, MercData: []byte{1, 0, 0, 0}, GCP: 12000})
	if ack := readAck(t, s); ack.ErrorCode == 0 {
		t.Fatal("failed mercenary blob acknowledged")
	}
	if !s.hasPendingDivaRewardClaims() || len(base.divaGCPRewardIDs) != 0 || repo.plainGPCalls != 0 {
		t.Fatal("blob failure affected GP receipt or balance")
	}
}

func TestDivaRewardMercenaryUnrelatedSaveUsesOriginalPath(t *testing.T) {
	server := createMockServer()
	base := newMockCharacterRepo()
	repo := &divaMercenaryFailureRepo{CharacterRepo: base, blobErr: errors.New("original blob error path")}
	server.charRepo = repo
	s := createMockSession(42, server)
	if err := s.stageDivaRewardClaims([]DivaRewardOffer{testDivaItemOffer(1)}); err != nil {
		t.Fatal(err)
	}
	handleMsgMhfSaveMercenary(s, &mhfpacket.MsgMhfSaveMercenary{AckHandle: 7, MercData: []byte{1, 0, 0, 0}, GCP: 12000})
	if ack := readAck(t, s); ack.ErrorCode != 0 {
		t.Fatal("unrelated save changed original ACK behavior")
	}
	if !s.hasPendingDivaRewardClaims() || len(base.divaGCPRewardIDs) != 0 || repo.plainGPCalls != 1 {
		t.Fatal("unrelated save did not preserve original GP path")
	}
}

func TestDivaRewardCharacterSaveKeepsFailedReceipts(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.RealClientMode = cfg.ZZ
			repo := newMockCharacterRepo()
			repo.loadSaveDataID, repo.loadSaveDataName = 42, "Hunter"
			repo.strings["name"] = "Hunter"
			repo.loadSaveDataData, _ = nullcomp.Compress(characterSaveBlob(t, "Hunter"))
			if fail {
				repo.saveAtomicErr = errors.New("save failed")
			}
			server.charRepo = repo
			s := createMockSession(42, server)
			if err := s.stageDivaRewardClaims([]DivaRewardOffer{testDivaItemOffer(1), testDivaGPOffer(2)}); err != nil {
				t.Fatal(err)
			}
			handleMsgMhfSavedata(s, &mhfpacket.MsgMhfSavedata{AckHandle: 9, RawDataPayload: characterSaveBlob(t, "Hunter")})
			ack := readAck(t, s)
			if (ack.ErrorCode != 0) != fail {
				t.Fatalf("ack = %+v", ack)
			}
			if len(repo.saveAtomicParams) != 1 || !reflect.DeepEqual(repo.saveAtomicParams[0].DivaRewardIDs, []uint32{1}) {
				t.Fatal("item receipts not passed to atomic save")
			}
			if (len(s.pendingDivaRewardClaims(7)) != 0) != fail {
				t.Fatal("incorrect pending item state")
			}
			if !reflect.DeepEqual(s.pendingDivaRewardClaims(26), []uint32{2}) {
				t.Fatal("savedata consumed GP receipt")
			}
		})
	}
}

func TestDivaRewardServerSaveDoesNotConsumeReceipts(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	repo := newMockCharacterRepo()
	repo.loadSaveDataID, repo.loadSaveDataName = 42, "Hunter"
	repo.strings["name"] = "Hunter"
	repo.loadSaveDataData, _ = nullcomp.Compress(characterSaveBlob(t, "Hunter"))
	server.charRepo = repo
	s := createMockSession(42, server)
	if err := s.stageDivaRewardClaims([]DivaRewardOffer{testDivaItemOffer(1), testDivaGPOffer(2)}); err != nil {
		t.Fatal(err)
	}
	save, err := GetCharacterSaveData(s, 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := save.Save(s); err != nil {
		t.Fatal(err)
	}
	if len(repo.saveAtomicParams) != 1 || len(repo.saveAtomicParams[0].DivaRewardIDs) != 0 {
		t.Fatal("server-side save included diva receipts")
	}
	if len(s.pendingDivaRewardClaims(7)) != 1 || len(s.pendingDivaRewardClaims(26)) != 1 {
		t.Fatal("server-side save consumed pending receipts")
	}
}

func TestDivaRewardSaveRejectsWrongCharacter(t *testing.T) {
	s := createMockSession(42, createMockServer())
	save := &CharacterSaveData{CharID: 43}
	if err := save.SaveWithDivaRewards(s, []uint32{1}); err == nil {
		t.Fatal("another character's reward save accepted")
	}
}
