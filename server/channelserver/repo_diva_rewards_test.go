package channelserver

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

var _ DivaRewardRepository = (*DivaRepository)(nil)

func divaRewardRepoTestEntry(key string, kind uint8) DivaRewardCatalogEntry {
	return DivaRewardCatalogEntry{Key: key, RewardType: kind, ItemType: 7, ItemID: 0x3694, Quantity: 5}
}

func TestDivaRewardCatalogValidation(t *testing.T) {
	base := divaRewardRepoTestEntry("daily-1", 0)
	for _, tt := range []struct {
		name   string
		change func(*DivaRewardCatalogEntry)
	}{
		{"empty key", func(r *DivaRewardCatalogEntry) { r.Key = "" }},
		{"whitespace key", func(r *DivaRewardCatalogEntry) { r.Key = " daily-1" }},
		{"nul key", func(r *DivaRewardCatalogEntry) { r.Key = "a\x00b" }},
		{"wrong reward type", func(r *DivaRewardCatalogEntry) { r.RewardType = 2 }},
		{"unsupported item", func(r *DivaRewardCatalogEntry) { r.ItemType = 1 }},
		{"zero material", func(r *DivaRewardCatalogEntry) { r.ItemID = 0 }},
		{"nonzero GP ID", func(r *DivaRewardCatalogEntry) { r.ItemType = 26 }},
		{"zero quantity", func(r *DivaRewardCatalogEntry) { r.Quantity = 0 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := base
			tt.change(&r)
			if err := validateDivaRewardCatalog(0, []DivaRewardCatalogEntry{r}); !errors.Is(err, errInvalidDivaReward) {
				t.Fatalf("invalid catalog accepted: %v", err)
			}
		})
	}
	if err := validateDivaRewardCatalog(0, []DivaRewardCatalogEntry{base, base}); err == nil {
		t.Fatal("duplicate key accepted")
	}
	if err := validateDivaRewardCatalog(8, nil); err == nil {
		t.Fatal("invalid reward type accepted")
	}
	gp := base
	gp.Key, gp.ItemType, gp.ItemID, gp.Quantity = "gp", 26, 0, 12000
	if err := validateDivaRewardCatalog(0, []DivaRewardCatalogEntry{base, gp}); err != nil {
		t.Fatal(err)
	}
	if err := validateDivaRewardCatalog(0, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDivaRewardClaimIDValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		char uint32
		kind uint8
		ids  []uint32
	}{
		{"missing character", 0, 0, nil},
		{"invalid kind", 1, 8, nil},
		{"zero ID", 1, 0, []uint32{0}},
		{"duplicate ID", 1, 0, []uint32{1, 1}},
		{"over protocol limit", 1, 0, make([]uint32, 33)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateDivaRewardClaimIDs(tt.char, tt.kind, tt.ids); err == nil {
				t.Fatal("invalid claim accepted")
			}
		})
	}
	if err := validateDivaRewardClaimIDs(1, 7, []uint32{1, 0xffffffff}); err != nil {
		t.Fatal(err)
	}
	if err := validateDivaRewardClaimIDs(1, 0, nil); err != nil {
		t.Fatal(err)
	}
}

func setupDivaRewardRepoTest(t *testing.T) (*DivaRepository, *sqlx.DB, uint32, uint32) {
	t.Helper()
	r, db := setupDivaRepo(t)
	u := CreateTestUser(t, db, "diva_reward_test")
	c := CreateTestCharacter(t, db, u, "RewardTester")
	var event uint32
	if err := db.QueryRow("INSERT INTO events(event_type,start_time) VALUES('diva',$1) RETURNING id", divaTestTime(21, 12, 0)).Scan(&event); err != nil {
		t.Fatal(err)
	}
	return r, db, c, event
}

func TestRepoDivaRewardProgress(t *testing.T) {
	r, db, charID, eventID := setupDivaRewardRepoTest(t)
	start, end := divaTestTime(21, 12, 0), divaTestTime(28, 12, 0)
	if _, err := db.Exec("UPDATE characters SET gr=7 WHERE id=$1", charID); err != nil {
		t.Fatal(err)
	}
	u := CreateTestUser(t, db, "diva_reward_rankers")
	tied := CreateTestCharacter(t, db, u, "Tied")
	top := CreateTestCharacter(t, db, u, "Top")
	lower := CreateTestCharacter(t, db, u, "Lower")
	idle := CreateTestCharacter(t, db, u, "Idle")
	if _, err := db.Exec("UPDATE characters SET gr=NULL WHERE id=$1", idle); err != nil {
		t.Fatal(err)
	}
	var otherEvent uint32
	if err := db.QueryRow("INSERT INTO events(event_type,start_time) VALUES('diva',$1) RETURNING id", start).Scan(&otherEvent); err != nil {
		t.Fatal(err)
	}
	add := func(c, e uint32, at time.Time, q, b int) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO diva_song_records
			(char_id,event_id,day_start,bead_index,quest_points,bonus_points,submitted_at)
			VALUES($1,$2,$3,1,$4,$5,$6)`, c, e, divaNoon(at), q, b, at); err != nil {
			t.Fatal(err)
		}
	}
	add(charID, eventID, start.Add(-time.Second), 9000, 0)
	add(charID, eventID, start, 10, 5)
	add(charID, eventID, start.Add(time.Hour), 5, 0)
	add(charID, eventID, start.Add(24*time.Hour), 0, 10)
	add(charID, eventID, start.Add(48*time.Hour), 0, 0)
	add(charID, eventID, end, 9000, 0)
	add(charID, otherEvent, start, 9000, 0)
	add(tied, eventID, start, 30, 0)
	add(top, eventID, start, 60, 0)
	add(lower, eventID, start, 15, 0)
	got, err := r.GetDivaRewardProgress(charID, eventID, start, end)
	if err != nil || got != (DivaRewardProgress{GR: 7, Points: 30, ParticipationDays: 2, Rank: 2}) {
		t.Fatalf("window, days or rank mismatch: %+v, %v", got, err)
	}
	got, err = r.GetDivaRewardProgress(lower, eventID, start, end)
	if err != nil || got.Rank != 4 {
		t.Fatalf("competition ranking lost tied place: %+v, %v", got, err)
	}
	got, err = r.GetDivaRewardProgress(idle, eventID, start, end)
	if err != nil || got != (DivaRewardProgress{}) {
		t.Fatalf("idle/NULL-GR character gained progress: %+v, %v", got, err)
	}
	if _, err := r.GetDivaRewardProgress(charID, eventID, end, start); err == nil {
		t.Fatal("reversed progress window accepted")
	}
	if _, err := r.GetDivaRewardProgress(0xffffffff, eventID, start, end); err == nil {
		t.Fatal("unknown character accepted")
	}
}

func TestRepoDivaRewardOffersSnapshotAndEligibility(t *testing.T) {
	r, db, charID, eventID := setupDivaRewardRepoTest(t)
	a := divaRewardRepoTestEntry("rank-item", 2)
	b := DivaRewardCatalogEntry{Key: "rank-gp", RewardType: 2, ItemType: 26, Quantity: 12000}
	first, err := r.OfferDivaRewards(charID, eventID, 2, []DivaRewardCatalogEntry{a, b})
	if err != nil || len(first) != 2 || first[0].ID >= first[1].ID {
		t.Fatalf("first offer: %+v, %v", first, err)
	}
	a.ItemID, a.Quantity = 0x1d2e, 25
	next, err := r.OfferDivaRewards(charID, eventID, 2, []DivaRewardCatalogEntry{b, a})
	if err != nil || !reflect.DeepEqual(first, next) {
		t.Fatalf("edited/reordered catalog changed entitlement: %+v, %v", next, err)
	}
	next, err = r.OfferDivaRewards(charID, eventID, 2, []DivaRewardCatalogEntry{b})
	if err != nil || len(next) != 1 || next[0].ID != first[1].ID {
		t.Fatalf("no-longer eligible key leaked: %+v, %v", next, err)
	}
	var total, claimed int
	if err := db.QueryRow("SELECT COUNT(*),COUNT(claimed_at) FROM diva_reward_receipts").Scan(&total, &claimed); err != nil || total != 2 || claimed != 0 {
		t.Fatalf("query consumed or duplicated receipts: %d/%d, %v", total, claimed, err)
	}
	if _, err := db.Exec("UPDATE diva_reward_receipts SET claimed_at=NOW() WHERE id=$1", first[0].ID); err != nil {
		t.Fatal(err)
	}
	next, err = r.OfferDivaRewards(charID, eventID, 2, []DivaRewardCatalogEntry{a, b})
	if err != nil || len(next) != 1 || next[0].ID != first[1].ID {
		t.Fatalf("claimed reward reoffered: %+v, %v", next, err)
	}
	if next, err := r.OfferDivaRewards(charID, eventID, 2, nil); err != nil || len(next) != 0 {
		t.Fatalf("empty eligibility leaked old rewards: %+v, %v", next, err)
	}
	bad := divaRewardRepoTestEntry("bad", 2)
	bad.Quantity = 0
	if _, err := r.OfferDivaRewards(charID, eventID, 2, []DivaRewardCatalogEntry{divaRewardRepoTestEntry("not-inserted", 2), bad}); err == nil {
		t.Fatal("invalid later catalog row accepted")
	}
	if err := db.Get(&total, "SELECT COUNT(*) FROM diva_reward_receipts"); err != nil || total != 2 {
		t.Fatalf("invalid request partially inserted: %d, %v", total, err)
	}
}

func TestRepoDivaRewardPrepareClaims(t *testing.T) {
	r, db, charID, eventID := setupDivaRewardRepoTest(t)
	entries := []DivaRewardCatalogEntry{divaRewardRepoTestEntry("first", 0), divaRewardRepoTestEntry("second", 0)}
	rows, err := r.OfferDivaRewards(charID, eventID, 0, entries)
	if err != nil {
		t.Fatal(err)
	}
	u := CreateTestUser(t, db, "diva_foreign_reward")
	other := CreateTestCharacter(t, db, u, "Other")
	foreign, err := r.OfferDivaRewards(other, eventID, 0, entries[:1])
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		kind uint8
		ids  []uint32
	}{
		{"foreign", 0, []uint32{rows[0].ID, foreign[0].ID}},
		{"wrong kind", 2, []uint32{rows[0].ID}},
		{"unknown", 0, []uint32{rows[0].ID, 0xffffffff}},
		{"duplicate", 0, []uint32{rows[0].ID, rows[0].ID}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := r.PrepareDivaRewardClaims(charID, tt.kind, tt.ids); err == nil || len(got) != 0 {
				t.Fatalf("invalid request exposed rewards: %+v, %v", got, err)
			}
		})
	}
	for i := 0; i < 2; i++ {
		got, err := r.PrepareDivaRewardClaims(charID, 0, []uint32{rows[1].ID, rows[0].ID})
		if err != nil || !reflect.DeepEqual(got, rows) {
			t.Fatalf("prepare consumed or reordered rewards: %+v, %v", got, err)
		}
	}
	var claimed int
	if err := db.Get(&claimed, "SELECT COUNT(*) FROM diva_reward_receipts WHERE claimed_at IS NOT NULL"); err != nil || claimed != 0 {
		t.Fatalf("prepare consumed receipts: %d, %v", claimed, err)
	}
	if _, err := db.Exec("UPDATE diva_reward_receipts SET claimed_at=NOW() WHERE id=$1", rows[0].ID); err != nil {
		t.Fatal(err)
	}
	got, err := r.PrepareDivaRewardClaims(charID, 0, []uint32{rows[0].ID})
	if err != nil || len(got) != 0 {
		t.Fatalf("completed retry not idempotent: %+v, %v", got, err)
	}
	got, err = r.PrepareDivaRewardClaims(charID, 0, []uint32{rows[0].ID, rows[1].ID})
	if err != nil || len(got) != 1 || got[0].ID != rows[1].ID {
		t.Fatalf("mixed pending/completed retry: %+v, %v", got, err)
	}
}

func TestRepoDivaRewardOfferPagination(t *testing.T) {
	r, db, charID, eventID := setupDivaRewardRepoTest(t)
	var entries []DivaRewardCatalogEntry
	for i := 0; i < 40; i++ {
		entries = append(entries, divaRewardRepoTestEntry(fmt.Sprintf("personal-%02d", i), 0))
	}
	first, err := r.OfferDivaRewards(charID, eventID, 0, entries)
	if err != nil || len(first) != 32 {
		t.Fatalf("first page: %d, %v", len(first), err)
	}
	var ids []uint32
	for _, row := range first {
		ids = append(ids, row.ID)
	}
	if _, err := db.Exec("UPDATE diva_reward_receipts SET claimed_at=NOW() WHERE id=ANY($1::bigint[])", pq.Array(ids)); err != nil {
		t.Fatal(err)
	}
	next, err := r.OfferDivaRewards(charID, eventID, 0, entries)
	if err != nil || len(next) != 8 || next[0].ID <= first[31].ID {
		t.Fatalf("remaining page: %+v, %v", next, err)
	}
	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM diva_reward_receipts"); err != nil || count != 40 {
		t.Fatalf("pagination duplicated rows: %d, %v", count, err)
	}
}

func TestRepoDivaRewardConcurrentOffers(t *testing.T) {
	r, db, charID, eventID := setupDivaRewardRepoTest(t)
	var wg sync.WaitGroup
	results := make(chan []DivaRewardOffer, 8)
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := r.OfferDivaRewards(charID, eventID, 0, []DivaRewardCatalogEntry{divaRewardRepoTestEntry("daily", 0)})
			results <- rows
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first uint32
	for rows := range results {
		if len(rows) != 1 {
			t.Fatalf("wrong concurrent offer count: %+v", rows)
		}
		if first == 0 {
			first = rows[0].ID
		} else if rows[0].ID != first {
			t.Fatal("same entitlement allocated different IDs")
		}
	}
	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM diva_reward_receipts"); err != nil || count != 1 {
		t.Fatalf("concurrent requests duplicated receipt: %d, %v", count, err)
	}
}
