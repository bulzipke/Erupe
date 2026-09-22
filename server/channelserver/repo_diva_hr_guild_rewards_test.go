package channelserver

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func setDivaHRGuildTestRank(t *testing.T, db *sqlx.DB, char uint32, hr, gr uint16) {
	t.Helper()
	if _, err := db.Exec(`UPDATE characters SET hr=$2,gr=$3 WHERE id=$1`, char, hr, gr); err != nil {
		t.Fatal(err)
	}
}

func assertDivaHRGuildTestGroups(t *testing.T, db *sqlx.DB, char, event uint32, want map[string]string) {
	t.Helper()
	var rows []struct {
		Key     string `db:"group_key"`
		Variant string `db:"variant"`
	}
	if err := db.Select(&rows, `SELECT group_key,variant FROM diva_reward_groups WHERE char_id=$1 AND event_id=$2 AND reward_type=7`, char, event); err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string, len(rows))
	for _, row := range rows {
		got[row.Key] = row.Variant
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("guild variant groups=%v want=%v", got, want)
	}
}

func divaHRGuildTestGroups(areas []int, variant string) map[string]string {
	groups := make(map[string]string, len(areas))
	for _, area := range areas {
		groups[fmt.Sprintf("milestone-%d", area)] = variant
	}
	return groups
}

func TestRepoDivaHRGuildRewardsCommonRanksSaveAndNextRound(t *testing.T) {
	r, db, char, guild, event, now := setupDivaGuildRewardRepoTest(t, 26)
	setDivaHRGuildTestRank(t, db, char, 1, 0)
	if rows, err := r.offerDivaGuildRewardsAt(char, now); err != nil || len(rows) != 0 {
		t.Fatalf("HR1 unexpectedly eligible: %+v %v", rows, err)
	}
	var first []DivaRewardOffer
	for _, hr := range []uint16{2, 99, 100, 999} {
		setDivaHRGuildTestRank(t, db, char, hr, 0)
		rows, err := r.offerDivaGuildRewardsAt(char, now)
		if err != nil || len(rows) != 10 {
			t.Fatalf("HR%d offers=%+v %v", hr, rows, err)
		}
		if first == nil {
			first = rows
		} else if !reflect.DeepEqual(first, rows) {
			t.Fatalf("HR%d changed common HR receipts", hr)
		}
	}
	var tickets, gp uint32
	for _, row := range first {
		if row.ItemType == 7 && row.ItemID == 1026 {
			tickets += uint32(row.Quantity)
		} else if row.ItemType == 26 && row.ItemID == 0 {
			gp += uint32(row.Quantity)
		} else {
			t.Fatalf("unapproved HR guild item: %+v", row)
		}
	}
	if tickets != 55 || gp != 1500 {
		t.Fatalf("HR totals tickets=%d GP=%d", tickets, gp)
	}
	assertDivaHRGuildTestGroups(t, db, char, event.ID, divaHRGuildTestGroups([]int{2, 3, 5, 6, 8, 10, 20, 22, 24, 26}, "hr-2-999"))
	// A new repository models reconnect without any in-memory offer cache.
	reconnected := NewDivaRepository(db)
	if rows, err := reconnected.offerDivaGuildRewardsAt(char, now); err != nil || !reflect.DeepEqual(rows, first) {
		t.Fatalf("reconnect changed offers: %+v %v", rows, err)
	}
	ids := divaGuildRewardTestIDs(first, 0)
	if rows, err := r.prepareDivaGuildRewardClaimsAt(char, ids, now); err != nil || !reflect.DeepEqual(rows, first) {
		t.Fatalf("prepare HR rewards: %+v %v", rows, err)
	}
	for _, id := range ids {
		if divaSaveTestClaimTime(t, db, id).Valid {
			t.Fatal("prepare consumed a receipt")
		}
	}
	CreateTestUserBinary(t, db, char)
	characters := NewCharacterRepository(db)
	itemIDs, gpIDs := divaGuildRewardTestIDs(first, 7), divaGuildRewardTestIDs(first, 26)
	params := SaveAtomicParams{CharID: char, Name: "HRGuild", HR: 999, GR: 0, CompSave: []byte{3, 1, 4}, HouseData: []byte{1, 5}, DivaRewardIDs: itemIDs}
	if err := characters.SaveCharacterDataAtomic(params); err != nil {
		t.Fatal(err)
	}
	var saved []byte
	if err := db.Get(&saved, `SELECT savedata FROM characters WHERE id=$1`, char); err != nil || !bytes.Equal(saved, params.CompSave) {
		t.Fatalf("item savedata not committed: %x %v", saved, err)
	}
	for _, id := range itemIDs {
		if !divaSaveTestClaimTime(t, db, id).Valid {
			t.Fatal("item save did not consume item receipt")
		}
	}
	for _, id := range gpIDs {
		if divaSaveTestClaimTime(t, db, id).Valid {
			t.Fatal("item save consumed GP receipt")
		}
	}
	remaining, err := r.offerDivaGuildRewardsAt(char, now)
	if err != nil || len(remaining) != len(gpIDs) {
		t.Fatalf("remaining GP count=%d %v", len(remaining), err)
	}
	for _, row := range remaining {
		if row.ItemType != 26 {
			t.Fatal("saved item offered again")
		}
	}
	if err = characters.UpdateGCPAndPactWithDivaRewards(char, gp, 0, gpIDs); err != nil {
		t.Fatal(err)
	}
	var balance uint32
	if err = db.Get(&balance, `SELECT gcp FROM characters WHERE id=$1`, char); err != nil || balance != 1500 {
		t.Fatalf("GP save=%d %v", balance, err)
	}
	if rows, err := r.prepareDivaGuildRewardClaimsAt(char, ids, now); err != nil || len(rows) != 0 {
		t.Fatalf("completed retry=%+v %v", rows, err)
	}
	setDivaHRGuildTestRank(t, db, char, 999, 1)
	if rows, err := r.offerDivaGuildRewardsAt(char, now); err != nil || len(rows) != 0 {
		t.Fatalf("GR regranted completed HR thresholds: %+v %v", rows, err)
	}
	// A new real interception has its own groups and receipt IDs. Previous
	// round choices must not suppress its GR bundle or move old receipts.
	nextStart := now.Add(30 * 24 * time.Hour)
	next := insertDivaRewardWindowTestEvent(t, db, nextStart, -1)
	nextNow := nextStart.Add(time.Minute)
	if _, err = r.GetDivaMap(char, guild, next.ID, nextNow); err != nil {
		t.Fatal(err)
	}
	if _, err = r.BindDivaMapDeparture(char, guild, next.ID, 58043, divaRunKeyOne, nextNow, nextNow); err != nil {
		t.Fatal(err)
	}
	if err = r.AddDivaInterceptionPoints(char, next.ID, 58043, 1, guild, divaRunKeyOne, nextNow, nextNow); err != nil {
		t.Fatal(err)
	}
	setDivaGuildRewardTestAreas(t, r, db, char, guild, next.ID, nextNow, 26)
	rows, err := r.offerDivaGuildRewardsAt(char, nextNow)
	if err != nil || len(rows) != 12 {
		t.Fatalf("new round GR rewards=%+v %v", rows, err)
	}
	oldIDs := make(map[uint32]bool, len(first))
	for _, row := range first {
		oldIDs[row.ID] = true
	}
	for _, row := range rows {
		if row.EventID != next.ID || oldIDs[row.ID] {
			t.Fatal("new round reused old receipt")
		}
	}
	assertDivaHRGuildTestGroups(t, db, char, next.ID, divaHRGuildTestGroups([]int{2, 3, 5, 6, 8, 10, 20, 22, 24, 26}, "gr"))
	assertDivaHRGuildTestGroups(t, db, char, event.ID, divaHRGuildTestGroups([]int{2, 3, 5, 6, 8, 10, 20, 22, 24, 26}, "hr-2-999"))
}

func TestRepoDivaHRGuildRewardsPromotionPreservesPending(t *testing.T) {
	r, db, char, guild, event, now := setupDivaGuildRewardRepoTest(t, 10)
	setDivaHRGuildTestRank(t, db, char, 99, 0)
	first, err := r.offerDivaGuildRewardsAt(char, now)
	if err != nil || len(first) != 6 {
		t.Fatalf("first HR offer=%+v %v", first, err)
	}
	setDivaHRGuildTestRank(t, db, char, 999, 1)
	setDivaGuildRewardTestAreas(t, r, db, char, guild, event.ID, now, 26)
	after, err := r.offerDivaGuildRewardsAt(char, now)
	if err != nil || len(after) != 12 || !reflect.DeepEqual(after[:6], first) {
		t.Fatalf("promotion replaced or duplicated HR bundle: %+v %v", after, err)
	}
	want := divaHRGuildTestGroups([]int{2, 3, 5, 6, 8, 10}, "hr-2-999")
	for key, variant := range divaHRGuildTestGroups([]int{20, 22, 24, 26}, "gr") {
		want[key] = variant
	}
	assertDivaHRGuildTestGroups(t, db, char, event.ID, want)
	prepared, err := r.prepareDivaGuildRewardClaimsAt(char, divaGuildRewardTestIDs(first, 0), now)
	if err != nil || !reflect.DeepEqual(prepared, first) {
		t.Fatalf("GR could not claim pending HR snapshot: %+v %v", prepared, err)
	}
	if prepared, err = r.prepareDivaGuildRewardClaimsAt(char, divaGuildRewardTestIDs(after, 0), now); err != nil || !reflect.DeepEqual(prepared, after) {
		t.Fatalf("mixed HR/GR batch rejected: %+v %v", prepared, err)
	}
	if _, err = db.Exec(`UPDATE diva_reward_receipts SET quantity=quantity+1 WHERE id=$1`, first[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.prepareDivaGuildRewardClaimsAt(char, []uint32{first[0].ID}, now); !errors.Is(err, errInvalidDivaReward) {
		t.Fatalf("changed reward quantity accepted: %v", err)
	}
	if _, err = db.Exec(`UPDATE diva_reward_receipts SET quantity=$2 WHERE id=$1`, first[0].ID, first[0].Quantity); err != nil {
		t.Fatal(err)
	}
	// A real GR catalog row is still not authorized if this threshold was
	// reserved as HR. Synthetic insertion isolates the group validation.
	var wrongVariantID uint32
	grRow := divaApprovedGuildRewards()[0]
	if err = db.QueryRow(`INSERT INTO diva_reward_receipts(char_id,event_id,reward_type,catalog_key,item_type,item_id,quantity)
		VALUES($1,$2,7,$3,$4,$5,$6) RETURNING id`, char, event.ID, grRow.Key, grRow.ItemType, grRow.ItemID, grRow.Quantity).Scan(&wrongVariantID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.prepareDivaGuildRewardClaimsAt(char, []uint32{wrongVariantID}, now); !errors.Is(err, errInvalidDivaReward) {
		t.Fatalf("unselected GR variant receipt accepted: %v", err)
	}
	// Claim validation must check the reserved bundle, not only a catalog row
	// that happens to be valid for some rank at this threshold.
	if _, err = db.Exec(`UPDATE diva_reward_groups SET variant='gr' WHERE char_id=$1 AND event_id=$2 AND reward_type=7 AND group_key='milestone-2'`, char, event.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.prepareDivaGuildRewardClaimsAt(char, []uint32{first[0].ID}, now); !errors.Is(err, errInvalidDivaReward) {
		t.Fatalf("mismatched reserved variant accepted: %v", err)
	}
}

func TestRepoDivaHRGuildRewardsRankDecreasePreservesPending(t *testing.T) {
	r, db, char, guild, event, now := setupDivaGuildRewardRepoTest(t, 22)
	first, err := r.offerDivaGuildRewardsAt(char, now)
	if err != nil || len(first) != 10 {
		t.Fatalf("initial GR bundle=%+v %v", first, err)
	}
	setDivaHRGuildTestRank(t, db, char, 2, 0)
	setDivaGuildRewardTestAreas(t, r, db, char, guild, event.ID, now, 26)
	after, err := r.offerDivaGuildRewardsAt(char, now)
	if err != nil || len(after) != 12 || !reflect.DeepEqual(after[:10], first) {
		t.Fatalf("rank decrease replaced or duplicated GR bundle: %+v %v", after, err)
	}
	want := divaHRGuildTestGroups([]int{2, 3, 5, 6, 8, 10, 20, 22}, "gr")
	for key, variant := range divaHRGuildTestGroups([]int{24, 26}, "hr-2-999") {
		want[key] = variant
	}
	assertDivaHRGuildTestGroups(t, db, char, event.ID, want)
	if rows, err := r.prepareDivaGuildRewardClaimsAt(char, divaGuildRewardTestIDs(first, 0), now); err != nil || !reflect.DeepEqual(rows, first) {
		t.Fatalf("HR could not claim pending GR snapshot: %+v %v", rows, err)
	}
}

func TestRepoDivaHRGuildRewardsConcurrentOffer(t *testing.T) {
	r, db, char, _, event, now := setupDivaGuildRewardRepoTest(t, 26)
	setDivaHRGuildTestRank(t, db, char, 2, 0)
	type outcome struct {
		rows []DivaRewardOffer
		err  error
	}
	results := make(chan outcome, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := r.offerDivaGuildRewardsAt(char, now)
			results <- outcome{rows, err}
		}()
	}
	wg.Wait()
	close(results)
	var first []DivaRewardOffer
	for result := range results {
		if result.err != nil || len(result.rows) != 10 {
			t.Fatalf("concurrent HR query=%+v %v", result.rows, result.err)
		}
		if first == nil {
			first = result.rows
		} else if !reflect.DeepEqual(first, result.rows) {
			t.Fatal("concurrent offers changed receipt IDs")
		}
	}
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_reward_receipts WHERE char_id=$1 AND event_id=$2 AND reward_type=7`, char, event.ID); err != nil || count != 10 {
		t.Fatalf("duplicate HR receipts: %d %v", count, err)
	}
	assertDivaHRGuildTestGroups(t, db, char, event.ID, divaHRGuildTestGroups([]int{2, 3, 5, 6, 8, 10, 20, 22, 24, 26}, "hr-2-999"))
}

func TestRepoDivaHRGuildRewardsFirstAreaAndParticipation(t *testing.T) {
	r, db, char, guild, event, now := setupDivaGuildRewardRepoTest(t, 1)
	setDivaHRGuildTestRank(t, db, char, 2, 0)
	if rows, err := r.offerDivaGuildRewardsAt(char, now); err != nil || len(rows) != 0 {
		t.Fatalf("HR first reward opened before 2 areas: %+v %v", rows, err)
	}
	assertDivaHRGuildTestGroups(t, db, char, event.ID, map[string]string{})
	setDivaGuildRewardTestAreas(t, r, db, char, guild, event.ID, now, 2)
	rows, err := r.offerDivaGuildRewardsAt(char, now)
	if err != nil || len(rows) != 1 {
		t.Fatalf("HR first reward missing at exactly 2 areas: %+v %v", rows, err)
	}
	assertDivaHRGuildTestGroups(t, db, char, event.ID, divaHRGuildTestGroups([]int{2}, "hr-2-999"))
	other := CreateTestCharacter(t, db, CreateTestUser(t, db, "hr_guild_no_play"), "NoPlay")
	setDivaHRGuildTestRank(t, db, other, 999, 0)
	if _, err = db.Exec(`INSERT INTO guild_characters(character_id,guild_id,joined_at) VALUES($1,$2,$3)`, other, guild, now); err != nil {
		t.Fatal(err)
	}
	if rows, err = r.offerDivaGuildRewardsAt(other, now); err != nil || len(rows) != 0 {
		t.Fatalf("nonparticipating HR inherited guild rewards: %+v %v", rows, err)
	}
	assertDivaHRGuildTestGroups(t, db, other, event.ID, map[string]string{})
}
