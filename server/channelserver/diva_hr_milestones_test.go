package channelserver

import (
	"encoding/binary"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestDivaHRMilestoneApprovedCatalog(t *testing.T) {
	if len(divaHRMilestoneRewards) != 22 {
		t.Fatal("approved HR table changed")
	}
	for _, kind := range []uint8{1, 6} {
		for _, tt := range []struct {
			hr, gr uint16
			want   int
		}{
			{0, 0, 0}, {1, 0, 0}, {1000, 0, 0},
			{2, 0, 1}, {99, 0, 1}, {100, 0, 2}, {999, 0, 2},
		} {
			var rows []DivaRewardCatalogEntry
			if kind == 1 {
				rows = eligibleDivaSongRewards(kind, DivaRewardProgress{HR: tt.hr, GR: tt.gr, Points: 20000})
			} else {
				var err error
				rows, err = eligibleDivaInterceptionRewards(nil, DivaInterceptionProgress{Enabled: true, HR: tt.hr, GR: tt.gr, Points: 20000})
				if err != nil {
					t.Fatal(err)
				}
			}
			var amounts []uint16
			if tt.want > 0 {
				if kind == 1 {
					amounts = []uint16{100, 1, 200, 3, 500, 2}
					if tt.want == 2 {
						amounts = []uint16{200, 2, 400, 5, 1000, 4}
					}
				} else {
					amounts = []uint16{100, 5, 200, 10, 500}
					if tt.want == 2 {
						amounts = []uint16{200, 10, 400, 20, 1000}
					}
				}
			}
			if len(rows) != len(amounts) {
				t.Fatalf("kind=%d hr=%d: %+v", kind, tt.hr, rows)
			}
			if err := validateDivaRewardCatalog(kind, rows); err != nil {
				t.Fatal(err)
			}
			for i, r := range rows {
				if r.Quantity != amounts[i] || r.GR || r.Basis != "operator-approved-2026-09-22-hr-milestones" {
					t.Fatalf("wrong replacement row: %+v", r)
				}
			}
		}
	}
	for _, r := range divaHRMilestoneRewards {
		for _, pts := range []int64{0, int64(r.Threshold) - 1, int64(r.Threshold)} {
			var rows []DivaRewardCatalogEntry
			if r.RewardType == 1 {
				rows = eligibleDivaSongRewards(1, DivaRewardProgress{HR: r.MinHR, Points: pts})
			} else {
				rows, _ = eligibleDivaInterceptionRewards(nil, DivaInterceptionProgress{Enabled: true, HR: r.MinHR, Points: pts})
			}
			found := false
			for _, row := range rows {
				found = found || row.Key == r.Key
			}
			if found != (pts >= int64(r.Threshold)) {
				t.Fatalf("threshold leaked: %+v at %d", r, pts)
			}
		}
		if divaRewardRankMatches(r, r.MinHR, 1) {
			t.Fatal("GR eligible for HR replacement")
		}
	}
	if rows, err := eligibleDivaInterceptionRewards(nil, DivaInterceptionProgress{HR: 100, Points: 20000}); err != nil || len(rows) > 0 {
		t.Fatal("legacy round gained HR rewards")
	}
}

func TestDivaHRNormaWireBounds(t *testing.T) {
	wire := divaNormaRewardPayload(divaPrayerRewardCatalog())
	counts := map[uint32]int{}
	var previous uint32
	if len(wire) != 2+23*19 || binary.BigEndian.Uint16(wire) != 23 {
		t.Fatal("wrong norma count")
	}
	for at := 2; at < len(wire); at += 19 {
		row := wire[at : at+19]
		points := binary.BigEndian.Uint32(row[14:18])
		if points < previous || row[18] != 0 {
			t.Fatal("unsorted or repeating milestones")
		}
		previous = points
		lo, hi := binary.BigEndian.Uint32(row[10:14]), binary.BigEndian.Uint32(row[6:10])
		if row[5] == 1 {
			if lo != 1 || hi != 999 {
				t.Fatal("GR bounds changed")
			}
			counts[0]++
		} else {
			if (lo != 2 || hi != 99) && (lo != 100 || hi != 999) {
				t.Fatal("wrong HR bounds")
			}
			counts[lo]++
		}
	}
	if !reflect.DeepEqual(counts, map[uint32]int{0: 11, 2: 6, 100: 6}) {
		t.Fatal(counts)
	}
}

func TestDivaHRMilestoneAcquireHandlers(t *testing.T) {
	for _, kind := range []uint8{1, 6} {
		s, repo, event := newDivaInterceptionHandlerTestSession()
		want := 5
		if kind == 1 {
			want = 6
			repo.progress = DivaRewardProgress{HR: 100, Points: 20000}
		} else {
			repo.prizes = nil
			repo.interception[event.ID] = DivaInterceptionProgress{Enabled: true, HR: 100, Points: 20000}
		}
		handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 70, Unk0: 1, RewardType: kind})
		ack := readAck(t, s)
		if ack.ErrorCode != 0 || len(ack.Payload) != 2+want*9 || int(ack.Payload[1]) != want {
			t.Fatalf("HR query type %d: %+v", kind, ack)
		}
		var ids []uint32
		for _, row := range repo.offers {
			ids = append(ids, row.ID)
		}
		handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 71, RewardType: kind, ItemIDCount: uint8(len(ids)), RewardIDs: ids})
		if ack := readAck(t, s); ack.ErrorCode != 0 || !s.hasPendingDivaRewardClaims() || repo.prepareKind != kind {
			t.Fatalf("HR claim type %d: %+v", kind, ack)
		}
	}
}

type divaHRDisplayRepo struct {
	mockDivaRepo
	hr, gr uint16
	err    error
}

func (r *divaHRDisplayRepo) GetDivaRewardRanks(uint32) (uint16, uint16, error) {
	return r.hr, r.gr, r.err
}

func TestDivaHRInterceptionDisplayHandler(t *testing.T) {
	for _, tt := range []struct{ hr, gr, quantity uint16 }{{2, 0, 100}, {99, 0, 100}, {100, 0, 200}, {999, 0, 200}, {999, 1, 200}} {
		s := createMockSession(1, createMockServer())
		s.server.erupeConfig.RealClientMode = cfg.ZZ
		s.server.divaRepo = &divaHRDisplayRepo{hr: tt.hr, gr: tt.gr}
		handleMsgMhfGetUdTacticsRewardList(s, &mhfpacket.MsgMhfGetUdTacticsRewardList{AckHandle: 55})
		ack := readAck(t, s)
		if ack.ErrorCode != 0 || len(ack.Payload) != 7+(5+10)*11 || binary.BigEndian.Uint16(ack.Payload[1:3]) != 5 {
			t.Fatalf("HR preview failed: %+v", ack)
		}
		if binary.BigEndian.Uint16(ack.Payload[10:12]) != tt.quantity || ack.Payload[12] != 0 || ack.Payload[13] != 0 {
			t.Fatalf("wrong HR quantity/flags: %x", ack.Payload)
		}
	}
	s := createMockSession(1, createMockServer())
	s.server.erupeConfig.RealClientMode = cfg.ZZ
	s.server.divaRepo = &divaHRDisplayRepo{err: errors.New("database unavailable")}
	handleMsgMhfGetUdTacticsRewardList(s, &mhfpacket.MsgMhfGetUdTacticsRewardList{AckHandle: 55})
	if readAck(t, s).ErrorCode == 0 {
		t.Fatal("DB failure silently became a lower HR table")
	}
}

func TestRepoDivaHRMilestonePromotion(t *testing.T) {
	r, db, c, event := setupDivaRewardRepoTest(t)
	low := eligibleDivaSongRewards(1, DivaRewardProgress{HR: 2, Points: 1000})
	first, err := r.OfferDivaRewards(c, event, 1, low)
	if err != nil || len(first) != 2 {
		t.Fatalf("low HR %+v %v", first, err)
	}
	high := eligibleDivaSongRewards(1, DivaRewardProgress{HR: 100, Points: 20000})
	second, err := r.OfferDivaRewards(c, event, 1, high)
	if err != nil || len(second) != 6 || !reflect.DeepEqual(first, second[:2]) || second[2].Quantity != 400 {
		t.Fatalf("high HR replaced/duplicated first steps: %+v %v", second, err)
	}
	gr, err := r.OfferDivaRewards(c, event, 1, eligibleDivaSongRewards(1, DivaRewardProgress{GR: 1, Points: 20000}))
	if err != nil || !reflect.DeepEqual(gr, second) {
		t.Fatalf("GR duplicated HR 20k: %+v %v", gr, err)
	}
	if _, err = db.Exec(`UPDATE diva_reward_receipts SET claimed_at=NOW() WHERE char_id=$1`, c); err != nil {
		t.Fatal(err)
	}
	if again, err := r.OfferDivaRewards(c, event, 1, high); err != nil || len(again) != 0 {
		t.Fatalf("repeat grant %+v %v", again, err)
	}
	if _, err := r.PrepareDivaRewardClaims(c+1, 1, []uint32{first[0].ID}); err == nil {
		t.Fatal("foreign receipt accepted")
	}
}

func TestRepoDivaHRInterceptionPromotion(t *testing.T) {
	r, db := setupDivaRepo(t)
	c := CreateTestCharacter(t, db, CreateTestUser(t, db, "hr_tactics"), "HR")
	now := TimeAdjusted()
	event := insertDivaRewardWindowTestEvent(t, db, now.Add(-time.Hour), -1)
	low, _ := eligibleDivaInterceptionRewards(nil, DivaInterceptionProgress{Enabled: true, HR: 99, Points: 10000})
	first, err := r.offerDivaRewardsAt(c, event.ID, 6, low, now)
	if err != nil || len(first) != 4 {
		t.Fatalf("low HR %+v %v", first, err)
	}
	high, _ := eligibleDivaInterceptionRewards(nil, DivaInterceptionProgress{Enabled: true, HR: 100, Points: 20000})
	second, err := r.offerDivaRewardsAt(c, event.ID, 6, high, now)
	if err != nil || len(second) != 5 || !reflect.DeepEqual(second[:4], first) || second[4].Quantity != 1000 {
		t.Fatalf("high HR %+v %v", second, err)
	}
	prizes := []DivaPrize{{ID: 1, Type: "personal", GR: true, PointsReq: 10000, ItemType: 7, ItemID: 13974, Quantity: 1},
		{ID: 2, Type: "personal", GR: true, PointsReq: 15000, ItemType: 7, ItemID: 14299, Quantity: 1}}
	gr, _ := eligibleDivaInterceptionRewards(prizes, DivaInterceptionProgress{Enabled: true, GR: 1, Points: 20000})
	third, err := r.offerDivaRewardsAt(c, event.ID, 6, gr, now)
	if err != nil || len(third) != 6 || third[5].CatalogKey != "tactics-personal-2" {
		t.Fatalf("GR overlap %+v %v", third, err)
	}
	if _, err = db.Exec(`UPDATE characters SET hr=100,gr=0 WHERE id=$1`, c); err != nil {
		t.Fatal(err)
	}
	p, err := r.GetDivaInterceptionProgress(c, event.ID)
	if err != nil || p.HR != 100 || p.GR != 0 {
		t.Fatalf("authoritative rank %+v %v", p, err)
	}
}

func TestRepoDivaHRMilestoneConcurrentPromotion(t *testing.T) {
	r, db, c, event := setupDivaRewardRepoTest(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, hr := range []uint16{99, 100} {
		wg.Add(1)
		go func(hr uint16) {
			defer wg.Done()
			_, err := r.OfferDivaRewards(c, event, 1, eligibleDivaSongRewards(1, DivaRewardProgress{HR: hr, Points: 20000}))
			errs <- err
		}(hr)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var receipts, variants int
	if err := db.Get(&receipts, `SELECT COUNT(*) FROM diva_reward_receipts WHERE char_id=$1`, c); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&variants, `SELECT COUNT(DISTINCT variant) FROM diva_reward_groups WHERE char_id=$1`, c); err != nil {
		t.Fatal(err)
	}
	if receipts != 6 || variants != 1 {
		t.Fatalf("concurrent duplicate: %d / %d", receipts, variants)
	}
}
