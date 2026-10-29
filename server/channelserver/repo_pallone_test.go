package channelserver

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// This suite refuses production/default ports and never calls SetupTestDB,
// whose public-schema reset is inappropriate for a developer's running server.
func pallonePostgres(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("PALLONE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("PALLONE_TEST_POSTGRES_URL unset")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Port() != "55488" || u.Path != "/pallone_test" {
		t.Fatal("Pallone integration tests require disposable loopback port 55488/database pallone_test")
	}
	q := u.Query()
	q.Set("connect_timeout", "5")
	q.Set("statement_timeout", "10000")
	u.RawQuery = q.Encode()
	admin, err := sqlx.Connect("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("pallone_%x", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema)); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sqlx.Connect("postgres", u.String())
	if err != nil {
		admin.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	db.SetMaxOpenConns(12)
	t.Cleanup(func() {
		db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE"); err != nil {
			t.Errorf("cleanup isolated Pallone schema: %v", err)
		}
		admin.Close()
	})
	_, err = db.Exec(`CREATE TABLE characters(id integer PRIMARY KEY,name text NOT NULL,deleted boolean NOT NULL DEFAULT false);
		CREATE TABLE guilds(id bigint PRIMARY KEY,name text NOT NULL);
		CREATE TABLE guild_characters(character_id integer PRIMARY KEY REFERENCES characters(id),guild_id bigint REFERENCES guilds(id));
		INSERT INTO characters(id,name) VALUES(100,'A'),(101,'B'),(102,'C'),(103,'D');
		INSERT INTO guilds VALUES(10,'Alpha'),(20,'Beta');
		INSERT INTO guild_characters VALUES(100,10),(101,10),(102,20);`)
	if err != nil {
		t.Fatal(err)
	}
	ddl, err := os.ReadFile(filepath.Join("..", "migrations", "sql", "0088_pallone_festival.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	ddl, err = os.ReadFile(filepath.Join("..", "migrations", "sql", "0101_pallone_rewards.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	return db
}

func palloneRepoFixture(t *testing.T) (*PalloneRepository, *sqlx.DB, PalloneEvent, time.Time) {
	t.Helper()
	db := pallonePostgres(t)
	r := NewPalloneRepository(db)
	now := time.Date(2026, 10, 23, 3, 0, 0, 0, time.UTC)
	e := PalloneEvent{ID: 41, Start: now.Add(-time.Hour), End: now.Add(time.Hour), ClaimUntil: now.Add(24 * time.Hour)}
	if err := r.RegisterPalloneEvent(e, []PalloneRoute{{Key: 1, Star: 0, QuestIDs: []uint16{27438, 27365}}, {Key: 2, Star: 1, QuestIDs: []uint16{27479}}}); err != nil {
		t.Fatal(err)
	}
	return r, db, e, now
}

func palloneReceipt(e PalloneEvent, charID uint32, key int32, run int, now time.Time) PalloneRunReceipt {
	final := uint16(27365)
	if key == 2 {
		final = 27479
	}
	return PalloneRunReceipt{EventID: e.ID, CharID: charID, RouteKey: key, RunID: fmt.Sprintf("%032x", run), StartedAt: now.Add(-10*time.Minute + 987*time.Nanosecond), CompletedAt: now.Add(-time.Minute + 222*time.Nanosecond), FinalQuestID: final}
}

func TestPallonePostgresLedgerAndRankings(t *testing.T) {
	r, db, e, now := palloneRepoFixture(t)
	first := palloneReceipt(e, 100, 1, 1, now)
	v := PalloneSubmission{BaseScore: 1000, PremiumBonus: 500, ItemBonus: 2500, GuildID: 10}
	if result, err := r.SettlePalloneResult(first, v, now); err != nil || result.Duplicate || result.Multiplier != 0 {
		t.Fatalf("first settlement: %+v %v", result, err)
	}
	if _, err := db.Exec(`UPDATE guild_characters SET guild_id=20 WHERE character_id=100`); err != nil {
		t.Fatal(err)
	}
	// The same receipt remains retryable after guild transfer AND claim expiry.
	if result, err := r.SettlePalloneResult(first, v, e.ClaimUntil.Add(time.Hour)); err != nil || !result.Duplicate {
		t.Fatalf("exact retry: %+v %v", result, err)
	}
	changed := v
	changed.BaseScore++
	if _, err := r.SettlePalloneResult(first, changed, now); !errors.Is(err, ErrPalloneConflict) {
		t.Fatalf("changed retry = %v", err)
	}
	otherRound := first
	otherRound.EventID = 42
	if _, err := r.SettlePalloneResult(otherRound, v, now); !errors.Is(err, ErrPalloneConflict) {
		t.Fatalf("cross-round replay = %v", err)
	}
	for _, row := range []struct {
		char  uint32
		key   int32
		run   int
		base  int32
		guild uint32
	}{
		{100, 1, 2, 2000, 20}, {101, 1, 1, 3000, 10}, {102, 2, 1, 2000, 20}, {103, 1, 1, 3000, 0},
	} {
		if _, err := r.SettlePalloneResult(palloneReceipt(e, row.char, row.key, row.run, now), PalloneSubmission{BaseScore: row.base, GuildID: row.guild}, now); err != nil {
			t.Fatal(err)
		}
	}
	scores, err := r.GetPalloneScores(e.ID, 100)
	if err != nil || len(scores) != 1 || scores[0].Total != 5500 || scores[0].Best != 2000 || scores[0].Premium != 500 {
		t.Fatalf("score/best/premium mixed: %+v %v", scores, err)
	}
	key := int32(1)
	route, err := r.GetPallonePersonalRanking(e.ID, &key, 0, 100)
	if err != nil || len(route) != 3 || route[0].Name != "B" || route[1].Name != "D" || route[2].Name != "A" || route[2].Score != 2000 {
		t.Fatalf("route must rank best BASE score: %+v %v", route, err)
	}
	overall, err := r.GetPallonePersonalRanking(e.ID, nil, 0, 100)
	if err != nil || len(overall) != 4 || overall[0].Name != "A" || overall[0].Score != 5500 {
		t.Fatalf("overall includes item, excludes premium: %+v %v", overall, err)
	}
	guilds, err := r.GetPalloneGuildRanking(e.ID, nil)
	if err != nil || len(guilds) != 2 || guilds[0].GuildID != 10 || guilds[0].Score != 6500 || guilds[1].GuildID != 20 || guilds[1].Score != 4000 {
		t.Fatalf("historical guild attribution: %+v %v", guilds, err)
	}
	members, err := r.GetPallonePersonalRanking(e.ID, nil, 10, 5)
	if err != nil || len(members) != 2 || members[0].Name != "A" || members[0].Score != 3500 {
		t.Fatalf("guild contributors moved with membership: %+v %v", members, err)
	}
	ranks, err := r.GetPalloneRanks(e.ID, 100, 0)
	if err != nil || len(ranks) != 2 || ranks[0].Key != 0 || ranks[0].Rank != 1 || ranks[1].Key != 1 || ranks[1].Rank != 3 {
		t.Fatalf("personal rank and ties: %+v %v", ranks, err)
	}
	ranks, err = r.GetPalloneRanks(e.ID, 101, 0)
	if err != nil || ranks[0].Rank != 2 || ranks[1].Rank != 1 {
		t.Fatalf("tied rank should not depend on character ID: %+v %v", ranks, err)
	}
	ranks, err = r.GetPalloneRanks(e.ID, 100, 10)
	if err != nil || len(ranks) != 2 || ranks[0].Score != 6500 || ranks[0].Rank != 1 {
		t.Fatalf("guild my-rank: %+v %v", ranks, err)
	}
	newEvent := e
	newEvent.ID = 42
	if err := r.RegisterPalloneEvent(newEvent, []PalloneRoute{{Key: 1, Star: 0, QuestIDs: []uint16{27365}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SettlePalloneResult(palloneReceipt(newEvent, 100, 1, 3, now), PalloneSubmission{BaseScore: 99999}, now); err != nil {
		t.Fatal(err)
	}
	scores, err = r.GetPalloneScores(e.ID, 100)
	if err != nil || scores[0].Total != 5500 {
		t.Fatalf("new round leaked into old score: %+v %v", scores, err)
	}
}

func TestPallonePostgresRefusalsAndRollback(t *testing.T) {
	r, db, e, now := palloneRepoFixture(t)
	receipt := palloneReceipt(e, 100, 1, 1, now)
	for _, test := range []struct {
		receipt PalloneRunReceipt
		value   PalloneSubmission
		want    error
	}{
		{receipt, PalloneSubmission{BaseScore: 1, GuildID: 20}, ErrPalloneGuild},
		{func() PalloneRunReceipt { v := receipt; v.FinalQuestID = 27438; return v }(), PalloneSubmission{BaseScore: 1}, ErrPalloneInvalid},
		{func() PalloneRunReceipt { v := receipt; v.StartedAt = e.Start.Add(-time.Second); return v }(), PalloneSubmission{BaseScore: 1}, ErrPalloneIneligible},
		{receipt, PalloneSubmission{BaseScore: 1, ItemBonus: -1}, ErrPalloneInvalid},
	} {
		if _, err := r.SettlePalloneResult(test.receipt, test.value, now); !errors.Is(err, test.want) {
			t.Fatalf("refusal = %v, want %v", err, test.want)
		}
	}
	if _, err := r.SettlePalloneResult(receipt, PalloneSubmission{BaseScore: 1}, e.ClaimUntil); !errors.Is(err, ErrPalloneIneligible) {
		t.Fatalf("late new result = %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE pallone_results ADD CONSTRAINT injected_failure CHECK(base_score<>1234)`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SettlePalloneResult(receipt, PalloneSubmission{BaseScore: 1234}, now); err == nil {
		t.Fatal("injected write failure succeeded")
	}
	var n int
	if err := db.Get(&n, `SELECT count(*) FROM pallone_results`); err != nil || n != 0 {
		t.Fatalf("failed writes consumed receipts: %d %v", n, err)
	}
	if _, err := db.Exec(`ALTER TABLE pallone_results DROP CONSTRAINT injected_failure`); err != nil {
		t.Fatal(err)
	}
	if v, err := r.SettlePalloneResult(receipt, PalloneSubmission{BaseScore: 1234}, now); err != nil || v.Duplicate {
		t.Fatalf("failed receipt was not retryable: %+v %v", v, err)
	}
	late := palloneReceipt(e, 100, 1, 2, e.End.Add(10*time.Minute))
	late.StartedAt = e.End.Add(-time.Minute)
	if _, err := r.SettlePalloneResult(late, PalloneSubmission{BaseScore: 1}, e.End.Add(10*time.Minute)); !errors.Is(err, ErrPalloneIneligible) {
		t.Fatalf("post-deadline clear changed the closed round: %v", err)
	}
	if err := db.Get(&n, `SELECT count(*) FROM pallone_results`); err != nil || n != 1 {
		t.Fatalf("late clear wrote festival credit: %d %v", n, err)
	}
}

func TestPallonePostgresConcurrentRetriesAndGuildCap(t *testing.T) {
	r, db, e, now := palloneRepoFixture(t)
	receipt := palloneReceipt(e, 100, 1, 1, now)
	var wg sync.WaitGroup
	results := make(chan PalloneSettlement, 16)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := r.SettlePalloneResult(receipt, PalloneSubmission{BaseScore: 1000, GuildID: 10}, now)
			results <- v
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
	first := 0
	for v := range results {
		if !v.Duplicate {
			first++
		}
	}
	var n int
	if err := db.Get(&n, `SELECT count(*) FROM pallone_results`); err != nil || n != 1 || first != 1 {
		t.Fatalf("concurrent retry credited %d rows / %d first writes: %v", n, first, err)
	}
	// Fill one guild near its native limit, then let different members race.
	if _, err := db.Exec(`UPDATE guild_characters SET guild_id=10 WHERE character_id=102`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SettlePalloneResult(palloneReceipt(e, 100, 1, 2, now), PalloneSubmission{BaseScore: math.MaxInt32 - 1050, GuildID: 10}, now); err != nil {
		t.Fatal(err)
	}
	errs = make(chan error, 2)
	for _, cid := range []uint32{101, 102} {
		wg.Add(1)
		go func(cid uint32) {
			defer wg.Done()
			_, err := r.SettlePalloneResult(palloneReceipt(e, cid, 1, 1, now), PalloneSubmission{BaseScore: 30, GuildID: 10}, now)
			errs <- err
		}(cid)
	}
	wg.Wait()
	close(errs)
	accepted, limited := 0, 0
	for err := range errs {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrPalloneScoreLimit) {
			limited++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || limited != 1 {
		t.Fatalf("guild cap race: accepted=%d limited=%d", accepted, limited)
	}
	guilds, err := r.GetPalloneGuildRanking(e.ID, nil)
	if err != nil || len(guilds) != 1 || guilds[0].Score != math.MaxInt32-20 {
		t.Fatalf("guild integer overflow: %+v %v", guilds, err)
	}
}

func TestPallonePostgresRegistrationIsImmutable(t *testing.T) {
	r, _, e, _ := palloneRepoFixture(t)
	routes := []PalloneRoute{{Key: 2, Star: 1, QuestIDs: []uint16{27479}}, {Key: 1, Star: 0, QuestIDs: []uint16{27438, 27365}}}
	if err := r.RegisterPalloneEvent(e, routes); err != nil {
		t.Fatalf("identical event re-registration: %v", err)
	}
	changed := e
	changed.End = changed.End.Add(time.Hour)
	if err := r.RegisterPalloneEvent(changed, routes); !errors.Is(err, ErrPalloneConflict) {
		t.Fatalf("changed dates reset an existing event: %v", err)
	}
	routes[0].QuestIDs = []uint16{27438}
	if err := r.RegisterPalloneEvent(e, routes); !errors.Is(err, ErrPalloneConflict) {
		t.Fatalf("changed routes reset an existing event: %v", err)
	}
	stored, err := r.GetPalloneEvent(e.ID)
	if err != nil || !stored.End.Equal(e.End) {
		t.Fatalf("refused edit changed event: %+v %v", stored, err)
	}
}

func TestPallonePostgresRestoredAntiqueBonusAndUint32GuildID(t *testing.T) {
	r, db, e, now := palloneRepoFixture(t)
	guildID := uint32(1<<31 + 5)
	if _, err := db.Exec(`INSERT INTO guilds(id,name) VALUES($1,'High ID')`, guildID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE guild_characters SET guild_id=$1 WHERE character_id=100`, guildID); err != nil {
		t.Fatal(err)
	}
	// HCandleSend adds the earned antique amount to the stock item-bonus
	// word. It must contribute to total scores while staying out of best-base.
	if _, err := r.SettlePalloneResult(palloneReceipt(e, 100, 1, 1, now), PalloneSubmission{BaseScore: 1000, ItemBonus: 2742, GuildID: guildID}, now); err != nil {
		t.Fatal(err)
	}
	scores, err := r.GetPalloneScores(e.ID, 100)
	if err != nil || len(scores) != 1 || scores[0].Total != 3742 || scores[0].Best != 1000 {
		t.Fatalf("antique bonus mixed into best: %+v %v", scores, err)
	}
	guilds, err := r.GetPalloneGuildRanking(e.ID, nil)
	if err != nil || len(guilds) != 1 || guilds[0].GuildID != guildID || guilds[0].Score != 3742 {
		t.Fatalf("guild ID narrowing: %+v %v", guilds, err)
	}
}

func TestPallonePostgresOwnRankOutsidePublicTop100(t *testing.T) {
	r, db, e, now := palloneRepoFixture(t)
	if _, err := r.SettlePalloneResult(palloneReceipt(e, 100, 1, 1, now), PalloneSubmission{BaseScore: 1, GuildID: 10}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO characters(id,name) SELECT 1000+g,'Bulk'||g FROM generate_series(0,102) g;
		INSERT INTO guilds(id,name) SELECT 10000+g,'Guild'||g FROM generate_series(0,102) g;
		INSERT INTO guild_characters(character_id,guild_id) SELECT 1000+g,10000+g FROM generate_series(0,102) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO pallone_results(character_id,run_id,event_id,route_key,started_at,completed_at,final_quest_id,
		base_score,premium_bonus,item_bonus,total_score,submitted_guild_id,guild_id)
		SELECT 1000+g,md5(g::text),$1,1,$2,$3,27365,2+g,0,0,2+g,10000+g,10000+g FROM generate_series(0,102) g`,
		e.ID, now.Add(-10*time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	public, err := r.GetPallonePersonalRanking(e.ID, nil, 0, 100)
	if err != nil || len(public) != 100 || public[0].Score != 104 {
		t.Fatalf("personal public capacity/order: %+v %v", public, err)
	}
	for _, row := range public {
		if row.Name == "A" {
			t.Fatal("rank 104 leaked into public top 100")
		}
	}
	personal, err := r.GetPalloneRanks(e.ID, 100, 0)
	if err != nil || len(personal) != 2 || personal[0].Rank != 104 || personal[1].Rank != 104 {
		t.Fatalf("own rank was clipped to public capacity: %+v %v", personal, err)
	}
	guilds, err := r.GetPalloneGuildRanking(e.ID, nil)
	if err != nil || len(guilds) != 100 || guilds[0].Score != 104 {
		t.Fatalf("guild public capacity/order: %+v %v", guilds, err)
	}
	ownGuild, err := r.GetPalloneRanks(e.ID, 100, 10)
	if err != nil || len(ownGuild) != 2 || ownGuild[0].Rank != 104 || ownGuild[1].Rank != 104 {
		t.Fatalf("own guild rank was clipped: %+v %v", ownGuild, err)
	}
	if _, err := db.Exec(`UPDATE characters SET deleted=true WHERE id=1102`); err != nil {
		t.Fatal(err)
	}
	personal, err = r.GetPalloneRanks(e.ID, 100, 0)
	if err != nil || personal[0].Rank != 103 {
		t.Fatalf("deleted character remained in personal ranking: %+v %v", personal, err)
	}
	// Deletion does not undo an earned guild contribution.
	ownGuild, err = r.GetPalloneRanks(e.ID, 100, 10)
	if err != nil || ownGuild[0].Rank != 104 {
		t.Fatalf("deletion erased historical guild contribution: %+v %v", ownGuild, err)
	}
}
