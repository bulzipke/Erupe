package channelserver

import (
	"testing"
	"time"

	"erupe-ce/network/mhfpacket"
)

func TestTowerZeroFloorDepartures(t *testing.T) {
	for _, trp := range []int32{0, 1, 30} {
		for _, tr := range []int32{0, 1, 50, 51} {
			srv := createMockServer()
			srv.erupeConfig.TowerZone1UnlockTRP = 1
			srv.erupeConfig.TowerZone2UnlockTRP = 1
			srv.erupeConfig.TowerZone1UnlockFloor = 0
			srv.erupeConfig.TowerZone2UnlockFloor = 0
			srv.erupeConfig.TowerZone2UnlockTR = 51
			srv.towerRepo = &mockTowerRepo{towerData: TowerData{TR: tr, TRP: trp}}
			s := createMockSession(100, srv)
			var cache *TowerData
			if !s.allowsTowerQuest(EventQuest{QuestID: towerQuestPrologue}, &cache) {
				t.Fatal("practice must always be available")
			}
			if got := s.allowsTowerQuest(EventQuest{QuestID: towerQuestZone1}, &cache); got != (trp > 0) {
				t.Fatalf("TR%d/TRP%d: zero-floor zone 1 available=%v, want %v", tr, trp, got, trp > 0)
			}
			if got := s.allowsTowerQuest(EventQuest{QuestID: towerQuestZone2}, &cache); got != (tr >= 51 && trp > 0) {
				t.Fatalf("TR%d/TRP%d: zone 2 available=%v, want %v", tr, trp, got, tr >= 51 && trp > 0)
			}
			if s.allowsTowerQuest(EventQuest{QuestID: towerQuestGuardian1}, &cache) ||
				s.allowsTowerQuest(EventQuest{QuestID: towerQuestGuardian2}, &cache) {
				t.Fatal("zero floors must not offer a Guardian milestone")
			}
		}
	}
}

func TestTowerZone1TRPGateOptions(t *testing.T) {
	for _, tc := range []struct {
		name               string
		needTRP, needFloor int32
		trp, floor, passed int32
		want               bool
	}{
		{"disabled", 0, 0, 0, 0, 0, true},
		{"custom TRP below", 30, 0, 29, 0, 0, false},
		{"custom TRP reached", 30, 0, 30, 0, 0, true},
		{"custom floor below", 1, 3, 30, 0, 0, false},
		{"custom floor reached", 1, 3, 30, 3, 0, true},
		{"urgent replaces normal", 1, 0, 30, 10, 0, false},
		{"urgent consumed", 1, 0, 30, 10, 10, true},
		{"disabled gate keeps urgent", 0, 0, 0, 10, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := createMockServer()
			srv.erupeConfig.TowerZone1UnlockTRP = tc.needTRP
			srv.erupeConfig.TowerZone1UnlockFloor = tc.needFloor
			srv.towerRepo = &mockTowerRepo{towerData: TowerData{TRP: tc.trp, Block1: tc.floor, Guardian1: tc.passed}}
			s := createMockSession(100, srv)
			var cache *TowerData
			if got := s.allowsTowerQuest(EventQuest{QuestID: towerQuestZone1}, &cache); got != tc.want {
				t.Fatalf("zone 1 available=%v, want %v", got, tc.want)
			}
		})
	}
}

type prologueProgressRepo struct {
	mockTowerRepo
	progressTRP int32
}

func (r *prologueProgressRepo) UpdateProgress(_ uint32, _ int32, trp int32, _ int32, _ int32) error {
	r.progressTRP += trp
	return nil
}

func TestTowerPrologueKeepsZeroFloorsAndTRP(t *testing.T) {
	for _, it7 := range []bool{false, true} {
		srv := createMockServer()
		srv.guildRepo = &mockGuildRepo{}
		repo := &prologueProgressRepo{}
		srv.towerRepo = repo
		srv.erupeConfig.TowerRankTRPPerRank = 600
		s := createMockSession(100, srv)
		s.questWeaponGeneration = 1
		s.activeQuestID.Store(towerQuestPrologue)
		if it7 {
			// Native practice result: 0 floors, 10 TRP with the 3x course.
			handleMsgMhfPostTowerInfo(s, &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 7, TR: 1, TRP: 30, Unk6: 1, Block1: 0})
			<-s.sendPackets
		}
		// The legacy report's traversed floor must not become a permanent floor,
		// whether native IT7 already posted or the compatibility fallback runs.
		for i := 0; i < 2; i++ {
			handleMsgMhfPostTenrouirai(s, &mhfpacket.MsgMhfPostTenrouirai{AckHandle: 2, Op: 1, Floors: 1, TRP: 30})
			<-s.sendPackets
		}
		if repo.floorBlock != 0 || repo.floorValue != 0 {
			t.Fatalf("IT7=%v: prologue added block=%d floors=%d", it7, repo.floorBlock, repo.floorValue)
		}
		if repo.progressTRP+repo.addedTRP != 30 {
			t.Fatalf("IT7=%v: TRP credited=%d+%d, want exactly 30", it7, repo.progressTRP, repo.addedTRP)
		}
		if it7 && repo.addedTRP != 0 {
			t.Fatal("investigation report duplicated the native TRP result")
		}
	}
}

func TestTowerZone2TRPGateOptions(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		needTRP, trp, tr                  int32
		needFloor, block1, block2, passed int32
		want                              bool
	}{
		{"disabled", 0, 0, 51, 0, 0, 0, 0, true},
		{"custom TRP below", 30, 29, 51, 0, 0, 0, 0, false},
		{"custom TRP reached", 30, 30, 51, 0, 0, 0, 0, true},
		{"TR still required", 0, 30, 50, 0, 0, 0, 0, false},
		{"custom floor below", 1, 30, 51, 3, 0, 0, 0, false},
		{"custom floor reached", 1, 30, 51, 3, 3, 0, 0, true},
		{"urgent replaces normal", 1, 30, 51, 0, 0, 10, 0, false},
		{"urgent consumed", 1, 30, 51, 0, 0, 10, 10, true},
		{"disabled gate keeps urgent", 0, 0, 51, 0, 0, 10, 0, false},
		{"other block urgent independent", 1, 30, 51, 0, 10, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := createMockServer()
			srv.erupeConfig.TowerZone2UnlockTRP = tc.needTRP
			srv.erupeConfig.TowerZone2UnlockTR = 51
			srv.erupeConfig.TowerZone2UnlockFloor = tc.needFloor
			srv.towerRepo = &mockTowerRepo{towerData: TowerData{TRP: tc.trp, TR: tc.tr, Block1: tc.block1, Block2: tc.block2, Guardian2: tc.passed}}
			s := createMockSession(100, srv)
			var cache *TowerData
			if got := s.allowsTowerQuest(EventQuest{QuestID: towerQuestZone2}, &cache); got != tc.want {
				t.Fatalf("zone 2 available=%v, want %v", got, tc.want)
			}
		})
	}
}

func (r *prologueProgressRepo) SettleTowerRun(charID uint32, key string, earth int32, day time.Time, v TowerSettlement) (int32, error) {
	before := r.settlementWrites
	gain, err := r.mockTowerRepo.SettleTowerRun(charID, key, earth, day, v)
	if err == nil && r.settlementWrites > before {
		r.progressTRP += v.TRP
	}
	return gain, err
}
