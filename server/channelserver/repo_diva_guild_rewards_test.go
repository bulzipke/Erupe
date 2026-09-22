package channelserver

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

var _ DivaGuildRewardRepository = (*DivaRepository)(nil)
var _ DivaTreasureRewardRepository = (*DivaRepository)(nil)
var _ DivaSpecialHallRepository = (*DivaRepository)(nil)

// Isolated database fixture only. The parent runs these tests sequentially;
// no test connects to a live game database. Synthetic area progress isolates
// reward semantics from the map settlement tests in repo_diva_map_test.go.
func setupDivaGuildRewardRepoTest(t *testing.T, areas uint32) (*DivaRepository, *sqlx.DB, uint32, uint32, DivaEvent, time.Time) {
	t.Helper()
	cfg := DefaultTestDBConfig()
	if (cfg.Host != "127.0.0.1" && cfg.Host != "localhost") || cfg.Port != "5433" || cfg.DBName != "erupe_test" {
		t.Fatal("guild reward tests require isolated localhost:5433/erupe_test before schema reset")
	}
	r, db, char, guild, event, now := setupDivaInterceptionRepoTest(t)
	start, _ := divaInterceptionWindow(event)
	if _, err := db.Exec(`INSERT INTO diva_map_cutover(singleton,installed_at) VALUES(TRUE,$1)
		ON CONFLICT(singleton) DO UPDATE SET installed_at=EXCLUDED.installed_at`, start.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, row := range divaGuildTestPrizes() {
		if _, err := db.Exec(`INSERT INTO diva_prizes(type,points_req,item_type,item_id,quantity,gr,repeatable)
			VALUES('guild',$1,$2,$3,$4,TRUE,FALSE)`, row.PointsReq, row.ItemType, row.ItemID, row.Quantity); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DELETE FROM diva_map_legacy_events WHERE event_id=$1`, event.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE characters SET gr=1 WHERE id=$1`, char); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetDivaMap(char, guild, event.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.BindDivaMapDeparture(char, guild, event.ID, 58043, divaRunKeyOne, now, now); err != nil {
		t.Fatal(err)
	}
	if err := r.AddDivaInterceptionPoints(char, event.ID, 58043, 1, guild, divaRunKeyOne, now, now); err != nil {
		t.Fatal(err)
	}
	setDivaGuildRewardTestAreas(t, r, db, char, guild, event.ID, now, areas)
	return r, db, char, guild, event, now
}

func setDivaGuildRewardTestAreas(t *testing.T, r *DivaRepository, db *sqlx.DB, char, guild, event uint32, now time.Time, areas uint32) {
	t.Helper()
	view, err := r.GetDivaMap(char, guild, event, now)
	if err != nil || !view.Enabled {
		t.Fatalf("map fixture unavailable: %+v %v", view, err)
	}
	view.Map.AcquiredAreas = areas
	data, err := json.Marshal(view.Map)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE diva_map_guilds SET acquired_areas=$3,map_data=$4 WHERE event_id=$1 AND guild_id=$2`, event, guild, areas, data); err != nil {
		t.Fatal(err)
	}
}

func divaGuildRewardTestIDs(offers []DivaRewardOffer, kind uint8) []uint32 {
	var ids []uint32
	for _, offer := range offers {
		if kind == 0 || offer.ItemType == kind {
			ids = append(ids, offer.ID)
		}
	}
	return ids
}

func TestRepoDivaGuildRewardsOfferClaimAndSave(t *testing.T) {
	r, db, char, _, event, now := setupDivaGuildRewardRepoTest(t, 26)
	offers, err := r.offerDivaGuildRewardsAt(char, now)
	if err != nil || len(offers) != 12 {
		t.Fatalf("expected twelve approved rows: %+v %v", offers, err)
	}
	again, err := r.offerDivaGuildRewardsAt(char, now.Add(time.Minute))
	if err != nil || !reflect.DeepEqual(again, offers) {
		t.Fatalf("offer replay changed receipts: %+v %v", again, err)
	}
	ids := divaGuildRewardTestIDs(offers, 0)
	prepared, err := r.prepareDivaGuildRewardClaimsAt(char, ids, now)
	if err != nil || !reflect.DeepEqual(prepared, offers) {
		t.Fatalf("claim: %+v %v", prepared, err)
	}
	for _, id := range ids {
		if divaSaveTestClaimTime(t, db, id).Valid {
			t.Fatal("prepare consumed before client save")
		}
	}
	CreateTestUserBinary(t, db, char)
	characters := NewCharacterRepository(db)
	items := divaGuildRewardTestIDs(offers, 7)
	gp := divaGuildRewardTestIDs(offers, 26)
	if err = characters.SaveCharacterDataAtomic(SaveAtomicParams{CharID: char, Name: "GuildReward", GR: 1, CompSave: []byte{1, 2}, HouseData: []byte{3}, DivaRewardIDs: items}); err != nil {
		t.Fatal(err)
	}
	remaining, err := r.offerDivaGuildRewardsAt(char, now)
	if err != nil || len(remaining) != 1 || remaining[0].ItemType != 26 {
		t.Fatalf("item save consumed GP too: %+v %v", remaining, err)
	}
	if err = characters.UpdateGCPAndPactWithDivaRewards(char, 3000, 0, gp); err != nil {
		t.Fatal(err)
	}
	if left, err := r.offerDivaGuildRewardsAt(char, now); err != nil || len(left) != 0 {
		t.Fatalf("consumed rewards offered again: %+v %v", left, err)
	}
	var count int
	if err = db.Get(&count, `SELECT COUNT(*) FROM diva_reward_receipts WHERE char_id=$1 AND event_id=$2 AND reward_type=7 AND claimed_at IS NOT NULL`, char, event.ID); err != nil || count != 12 {
		t.Fatalf("claimed=%d %v", count, err)
	}
}

func TestRepoDivaGuildRewardsEligibilityAndTransfer(t *testing.T) {
	r, db, char, guild, event, now := setupDivaGuildRewardRepoTest(t, 26)
	if _, err := db.Exec(`UPDATE characters SET gr=0 WHERE id=$1`, char); err != nil {
		t.Fatal(err)
	}
	if offers, err := r.offerDivaGuildRewardsAt(char, now); err != nil || len(offers) != 0 {
		t.Fatal("HR got GR prizes", err)
	}
	if _, err := db.Exec(`UPDATE characters SET gr=1 WHERE id=$1`, char); err != nil {
		t.Fatal(err)
	}
	user := CreateTestUser(t, db, "guild_other")
	other := CreateTestCharacter(t, db, user, "NoParticipation")
	if _, err := db.Exec(`UPDATE characters SET gr=1 WHERE id=$1`, other); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO guild_characters(character_id,guild_id,joined_at) VALUES($1,$2,$3)`, other, guild, now); err != nil {
		t.Fatal(err)
	}
	if offers, err := r.offerDivaGuildRewardsAt(other, now); err != nil || len(offers) != 0 {
		t.Fatal("nonparticipant inherited guild progress", err)
	}
	offers, err := r.offerDivaGuildRewardsAt(char, now)
	if err != nil || len(offers) != 12 {
		t.Fatal(offers, err)
	}
	if _, err = r.prepareDivaGuildRewardClaimsAt(other, []uint32{offers[0].ID}, now); !errors.Is(err, errInvalidDivaReward) {
		t.Fatal("foreign receipt accepted", err)
	}
	if _, err = db.Exec(`DELETE FROM guild_characters WHERE character_id=$1`, other); err != nil {
		t.Fatal(err)
	}
	newGuild := CreateTestGuild(t, db, other, "NewGuild")
	if _, err = db.Exec(`UPDATE guild_characters SET guild_id=$1 WHERE character_id=$2`, newGuild, char); err != nil {
		t.Fatal(err)
	}
	if _, err = r.prepareDivaGuildRewardClaimsAt(char, []uint32{offers[0].ID}, now); !errors.Is(err, ErrDivaInterceptionMembership) {
		t.Fatal("transferred pending claim accepted", err)
	}
	if _, err = r.GetDivaMap(char, newGuild, event.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = r.BindDivaMapDeparture(char, newGuild, event.ID, 58043, divaRunKeyTwo, now, now); err != nil {
		t.Fatal(err)
	}
	if err = r.AddDivaInterceptionPoints(char, event.ID, 58043, 1, newGuild, divaRunKeyTwo, now, now); err != nil {
		t.Fatal(err)
	}
	setDivaGuildRewardTestAreas(t, r, db, char, newGuild, event.ID, now, 40)
	if next, err := r.offerDivaGuildRewardsAt(char, now); err != nil || len(next) != 0 {
		t.Fatalf("new guild bypassed sticky binding: %+v %v", next, err)
	}
	var pinned uint32
	if err = db.Get(&pinned, `SELECT guild_id FROM diva_guild_reward_memberships WHERE char_id=$1 AND event_id=$2`, char, event.ID); err != nil || pinned != guild {
		t.Fatalf("binding changed: %d %v", pinned, err)
	}
}

func TestRepoDivaGuildRewardsExpireAndCompletedRetry(t *testing.T) {
	r, db, char, _, _, now := setupDivaGuildRewardRepoTest(t, 3)
	offers, err := r.offerDivaGuildRewardsAt(char, now)
	if err != nil || len(offers) != 2 {
		t.Fatal(offers, err)
	}
	if _, err = db.Exec(`UPDATE diva_reward_receipts SET claimed_at=NOW() WHERE id=$1`, offers[0].ID); err != nil {
		t.Fatal(err)
	}
	deadline := now.Add(30 * 24 * time.Hour)
	insertDivaRewardWindowTestEvent(t, db, deadline, -1)
	if _, err = r.prepareDivaGuildRewardClaimsAt(char, []uint32{offers[1].ID}, deadline.Add(-time.Microsecond)); err != nil {
		t.Fatal("expired before next actual start", err)
	}
	if _, err = r.prepareDivaGuildRewardClaimsAt(char, []uint32{offers[1].ID}, deadline); !errors.Is(err, ErrDivaInterceptionRewardExpired) {
		t.Fatal("expired receipt accepted", err)
	}
	if retry, err := r.prepareDivaGuildRewardClaimsAt(char, []uint32{offers[0].ID}, deadline); err != nil || len(retry) != 0 {
		t.Fatalf("completed retry failed/regranted: %+v %v", retry, err)
	}
}

func TestRepoDivaGuildRewardsConcurrentOffers(t *testing.T) {
	r, db, char, _, event, now := setupDivaGuildRewardRepoTest(t, 26)
	var wg sync.WaitGroup
	results := make(chan []DivaRewardOffer, 4)
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := r.offerDivaGuildRewardsAt(char, now)
			results <- rows
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first []DivaRewardOffer
	for rows := range results {
		if len(rows) != 12 {
			t.Fatalf("got %d", len(rows))
		}
		if first == nil {
			first = rows
		} else if !reflect.DeepEqual(first, rows) {
			t.Fatal("concurrent queries changed IDs")
		}
	}
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_reward_receipts WHERE char_id=$1 AND event_id=$2 AND reward_type=7`, char, event.ID); err != nil || count != 12 {
		t.Fatalf("duplicate receipts: %d %v", count, err)
	}
}

func insertDivaGuildTreasureTestAward(t *testing.T, db *sqlx.DB, char, guild, event uint32, route uint16, key string, now time.Time, participated bool) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO diva_map_departures(char_id,event_id,run_key,guild_id,map_number,quest_id,route,eligible,started_at)
		VALUES($1,$2,$3,$4,1,58079,$5,TRUE,$6)`, char, event, key, guild, route, now); err != nil {
		t.Fatal(err)
	}
	// Zero credited points models the last member of a party whose simultaneous
	// contributions already saturated the branch; that member still participated.
	if _, err := db.Exec(`INSERT INTO diva_map_contributions(char_id,event_id,run_key,guild_id,map_number,route,points,credited_points,submitted_at,eligible_at,settled_at,treasure_participation)
		VALUES($1,$2,$3,$4,1,$5,5000,0,$6,$6,$6,$7)`, char, event, key, guild, route, now, participated); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO diva_map_area_awards(event_id,guild_id,map_number,coordinate,is_branch,awarded_at)
		VALUES($1,$2,1,$3,TRUE,$4) ON CONFLICT DO NOTHING`, event, guild, route, now); err != nil {
		t.Fatal(err)
	}
}

func TestRepoDivaTreasureContributionAndMapCarry(t *testing.T) {
	r, db, char, guild, event, now := setupDivaGuildRewardRepoTest(t, 26)
	insertDivaGuildTreasureTestAward(t, db, char, guild, event.ID, 407, divaRunKeyTwo, now, true)
	insertDivaGuildTreasureTestAward(t, db, char, guild, event.ID, 106, "12345678-1234-4567-89ab-123456789abe", now, false)
	// Move to page two. Historical page-one treasures must remain collectable.
	previous, err := divaCustomMapInitial()
	if err != nil {
		t.Fatal(err)
	}
	progress, err := advanceDivaCustomMap(previous, map[uint16]uint64{0: 1000000})
	if err != nil {
		t.Fatal(err)
	}
	m, err := divaCustomMapNext(progress.Map)
	if err != nil {
		t.Fatal(err)
	}
	m.AcquiredAreas = 26
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE diva_map_guilds SET current_number=2,map_data=$3 WHERE event_id=$1 AND guild_id=$2`, event.ID, guild, data); err != nil {
		t.Fatal(err)
	}
	offers, err := r.offerDivaMapRewardsAt(char, 5, now)
	if err != nil || len(offers) != 2 {
		t.Fatalf("branch participation got %+v %v", offers, err)
	}
	if offers[0].ItemType != 7 || offers[0].ItemID != 1026 || offers[0].Quantity != 5 || offers[1].ItemType != 26 || offers[1].Quantity != 500 {
		t.Fatal("wrong approved treasure", offers)
	}
	if _, err = r.prepareDivaMapRewardClaimsAt(char, 5, divaGuildRewardTestIDs(offers, 0), now); err != nil {
		t.Fatal(err)
	}
	if _, err = r.prepareDivaGuildRewardClaimsAt(char, []uint32{offers[0].ID}, now); !errors.Is(err, errInvalidDivaReward) {
		t.Fatal("treasure used as guild prize", err)
	}
	again, err := r.offerDivaMapRewardsAt(char, 5, now)
	if err != nil || !reflect.DeepEqual(again, offers) {
		t.Fatal("treasure replay duplicated", again, err)
	}
	if _, err = db.Exec(`UPDATE diva_map_contributions SET treasure_participation=TRUE WHERE char_id=$1 AND event_id=$2 AND route=106`, char, event.ID); err != nil {
		t.Fatal(err)
	}
	all, err := r.offerDivaMapRewardsAt(char, 5, now)
	if err != nil || len(all) != 4 {
		t.Fatal("second qualifying branch not independent", all, err)
	}
	if _, err = db.Exec(`UPDATE diva_reward_receipts SET claimed_at=NOW() WHERE char_id=$1 AND reward_type=5`, char); err != nil {
		t.Fatal(err)
	}
	if remaining, err := r.offerDivaMapRewardsAt(char, 5, now); err != nil || len(remaining) != 0 {
		t.Fatal("claimed treasures returned", remaining, err)
	}
}

func TestRepoDivaSpecialHallCurrentMembershipAndWelcomeOnly(t *testing.T) {
	r, db, char, guild, event, now := setupDivaGuildRewardRepoTest(t, 1)
	_, end := divaInterceptionWindow(event)
	start := end.Add(time.Duration(divaInterlude) * time.Second)
	finish := end.Add(time.Duration(divaWeekDuration) * time.Second)
	for _, tt := range []struct {
		at   time.Time
		want bool
	}{{now, false}, {start.Add(-time.Microsecond), false}, {start, true}, {finish.Add(-time.Microsecond), true}, {finish, false}} {
		got, err := r.GetDivaSpecialHall(char, guild, tt.at)
		if err != nil || got != tt.want {
			t.Fatalf("hall at %s=%v want=%v %v", tt.at, got, tt.want, err)
		}
	}
	user := CreateTestUser(t, db, "hall_member")
	member := CreateTestCharacter(t, db, user, "HallMember")
	if _, err := db.Exec(`INSERT INTO guild_characters(character_id,guild_id,joined_at) VALUES($1,$2,$3)`, member, guild, now); err != nil {
		t.Fatal(err)
	}
	if got, err := r.GetDivaSpecialHall(member, guild, start); err != nil || !got {
		t.Fatal("current member without personal points lost guild perk", got, err)
	}
	if _, err := db.Exec(`UPDATE guild_characters SET joined_at=NULL WHERE character_id=$1`, member); err != nil {
		t.Fatal(err)
	}
	if got, err := r.GetDivaSpecialHall(member, guild, start); err != nil || got {
		t.Fatal("applicant entered special hall", got, err)
	}
	if _, err := db.Exec(`DELETE FROM guild_characters WHERE character_id=$1`, char); err != nil {
		t.Fatal(err)
	}
	if got, err := r.GetDivaSpecialHall(char, guild, start); err != nil || got {
		t.Fatal("departed member entered special hall", got, err)
	}
}
