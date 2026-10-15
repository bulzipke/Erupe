package channelserver

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func TestGuildLeadershipWeekBoundary(t *testing.T) {
	for _, tc := range []struct{ now, want string }{
		{"2026-10-18T14:59:59Z", "2026-10-12T00:00:00+09:00"},
		{"2026-10-18T15:00:00Z", "2026-10-19T00:00:00+09:00"},
		{"2026-10-19T20:00:00Z", "2026-10-19T00:00:00+09:00"},
		{"2027-01-03T15:00:00Z", "2027-01-04T00:00:00+09:00"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		want, _ := time.Parse(time.RFC3339, tc.want)
		if got := guildLeadershipWeekStart(now); !got.Equal(want) {
			t.Fatal(tc.now, got, want)
		}
	}
}

func TestGuildLeadershipSuccessorPolicy(t *testing.T) {
	members := []guildLeadershipMember{{1, 1, false, true}, {2, 2, true, true}, {3, 3, true, true}, {4, 4, true, true}, {5, 5, false, true}}
	if got := guildLeaderSuccessor(members, 1, true); got != 1 {
		t.Fatal("automatic deputy must override refusal", got)
	}
	if got := guildLeaderSuccessor(members, 1, false); got != 4 {
		t.Fatal("explicit resignation must respect all refusals", got)
	}
	members[1].Eligible, members[2].Eligible = false, false
	if got := guildLeaderSuccessor(members, 1, true); got != 4 {
		t.Fatal("ordinary member refusal must still apply", got)
	}
	members[4].Avoid = true
	if got := guildLeaderSuccessor(members, 1, true); got != -1 {
		t.Fatal("no eligible candidate", got)
	}
}

func setupGuildLeadershipTest(t *testing.T) (*GuildRepository, *sqlx.DB, uint32, uint32, time.Time) {
	t.Helper()
	repo, db, guildID, leaderID := setupGuildRepo(t)
	// A deterministic boundary after real DB time lets the appointment trigger
	// remain enabled while testing future weeks without changing system clocks.
	week := guildLeadershipWeekStart(TimeAdjusted()).AddDate(0, 0, 7)
	returnGuildTestExec(t, db, `INSERT INTO guild_leadership_maintenance(singleton,checked_week) VALUES(true,$1)`, week.AddDate(0, 0, -7))
	returnGuildTestExec(t, db, `UPDATE guilds SET leader_changed_at=$2 WHERE id=$1`, guildID, week.AddDate(0, 0, -30))
	returnGuildTestExec(t, db, `UPDATE characters SET last_login=$2 WHERE id=$1`, leaderID, week.AddDate(0, 0, -21).Unix())
	return repo, db, guildID, leaderID, week
}

func runGuildLeadershipTest(t *testing.T, repo *GuildRepository, now time.Time) []GuildLeadershipChange {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	completed, changes, err := repo.RotateInactiveGuildLeaders(ctx, now)
	if err != nil || !completed {
		t.Fatal("weekly maintenance did not complete", completed, changes, err)
	}
	return changes
}

func guildLeadershipTestOrder(t *testing.T, db *sqlx.DB, guildID uint32) []uint32 {
	t.Helper()
	var ids []uint32
	if err := db.Select(&ids, `SELECT character_id FROM guild_characters WHERE guild_id=$1 ORDER BY order_index,character_id`, guildID); err != nil {
		t.Fatal(err)
	}
	return ids
}

func guildLeadershipTestSnapshot(t *testing.T, db *sqlx.DB) string {
	t.Helper()
	var cursor string
	if err := db.Get(&cursor, `SELECT checked_week::text FROM guild_leadership_maintenance WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	return returnGuildTestSnapshot(t, db) + cursor
}

func TestGuildLeadershipWeeklyPromotionPreservesData(t *testing.T) {
	repo, db, guildID, leaderID, week := setupGuildLeadershipTest(t)
	deputy := returnGuildTestMember(t, repo, db, guildID, "Deputy")
	third := returnGuildTestMember(t, repo, db, guildID, "Third")
	member := returnGuildTestMember(t, repo, db, guildID, "Member")
	returnGuildTestExec(t, db, `UPDATE guild_characters SET avoid_leadership=true,rp_today=17 WHERE character_id=$1`, deputy)
	returnGuildTestExec(t, db, `UPDATE characters SET last_login=$2 WHERE id=$1`, deputy, week.AddDate(0, 0, -90).Unix())
	returnGuildTestExec(t, db, `UPDATE guilds SET rank_rp=9876,event_rp=456,pugi_name_1='Keep' WHERE id=$1`, guildID)
	stale, err := repo.GetByID(guildID)
	if err != nil || stale == nil {
		t.Fatal(stale, err)
	}
	changes := runGuildLeadershipTest(t, repo, week)
	if len(changes) != 1 || changes[0] != (GuildLeadershipChange{guildID, leaderID, deputy}) {
		t.Fatal(changes)
	}
	wantOrder := []uint32{deputy, third, member, leaderID}
	if got := guildLeadershipTestOrder(t, db, guildID); !reflect.DeepEqual(got, wantOrder) {
		t.Fatal("former leader must be last, others retain relative order", got, wantOrder)
	}
	var correct bool
	if err := db.Get(&correct, `SELECT leader_id=$2 AND rank_rp=9876 AND event_rp=456 AND pugi_name_1='Keep'
		AND EXISTS(SELECT 1 FROM guild_characters WHERE guild_id=$1 AND character_id=$2 AND order_index=1 AND rp_today=17 AND avoid_leadership)
		FROM guilds WHERE id=$1`, guildID, deputy); err != nil || !correct {
		t.Fatal("promotion lost guild/member data", correct, err)
	}
	stale.Comment = "UI save"
	if err := repo.Save(stale); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&correct, `SELECT leader_id=$2 AND comment='UI save' FROM guilds WHERE id=$1`, guildID, deputy); err != nil || !correct {
		t.Fatal("stale metadata restored former leader", correct, err)
	}
	if err := repo.SetAvoidLeadership(guildID, deputy, false); err != nil {
		t.Fatal(err)
	}
	if got := guildLeadershipTestOrder(t, db, guildID); !reflect.DeepEqual(got, wantOrder) {
		t.Fatal("refusal update rewrote promoted member order", got)
	}
	if err := repo.SetAvoidLeadership(guildID+999, deputy, true); !errors.Is(err, ErrGuildMemberMissing) {
		t.Fatal("refusal update must be guild scoped", err)
	}
}

func TestGuildLeadershipAllInactiveOncePerWeekNoLoop(t *testing.T) {
	repo, db, guildID, leaderID, week := setupGuildLeadershipTest(t)
	second := returnGuildTestMember(t, repo, db, guildID, "Second")
	third := returnGuildTestMember(t, repo, db, guildID, "Third")
	returnGuildTestExec(t, db, `UPDATE characters SET last_login=$1`, week.AddDate(0, 0, -90).Unix())
	// Even a long outage performs one latest-boundary check, not 20 historical
	// rotations which could loop through an entirely inactive membership list.
	returnGuildTestExec(t, db, `UPDATE guild_leadership_maintenance SET checked_week=$1`, week.AddDate(0, 0, -140))
	order := []uint32{leaderID, second, third}
	for i := 0; i < 7; i++ {
		now := week.AddDate(0, 0, 7*i)
		changes := runGuildLeadershipTest(t, repo, now)
		if len(changes) != 1 || changes[0].FormerLeaderID != order[0] || changes[0].NewLeaderID != order[1] {
			t.Fatal("must perform one list-order promotion per week", i, changes, order)
		}
		order = append(order[1:], order[0])
		if got := guildLeadershipTestOrder(t, db, guildID); !reflect.DeepEqual(got, order) {
			t.Fatal(got, order)
		}
		before := guildLeadershipTestSnapshot(t, db)
		if extra := runGuildLeadershipTest(t, repo, now.Add(6*24*time.Hour)); len(extra) != 0 {
			t.Fatal("successor reprocessed within the same week", extra)
		}
		if after := guildLeadershipTestSnapshot(t, db); after != before {
			t.Fatal("repeat/clock retry rewrote state", before, after)
		}
	}
}

func TestGuildLeadershipCutoffAsOfMonday(t *testing.T) {
	for _, tc := range []struct {
		name   string
		age    time.Duration
		rotate bool
	}{
		{"one second short", guildLeaderInactivePeriod - time.Second, false},
		{"exactly fourteen days", guildLeaderInactivePeriod, true},
		{"older than fourteen days", guildLeaderInactivePeriod + time.Second, true},
		{"not until next Monday", guildLeaderInactivePeriod - time.Hour, false},
		{"future login", -time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, db, guildID, leaderID, week := setupGuildLeadershipTest(t)
			returnGuildTestMember(t, repo, db, guildID, "Deputy")
			returnGuildTestExec(t, db, `UPDATE characters SET last_login=$2 WHERE id=$1`, leaderID, week.Add(-tc.age).Unix())
			changes := runGuildLeadershipTest(t, repo, week.Add(48*time.Hour))
			if (len(changes) == 1) != tc.rotate {
				t.Fatal(tc, changes)
			}
		})
	}
}

func TestGuildLeadershipProtectedCases(t *testing.T) {
	for _, scenario := range []string{"solo", "unknown login", "null login", "online binding", "live connection lease", "permanently banned", "deleted leader", "temporary guild", "recent appointment", "leader membership missing", "no eligible successor"} {
		t.Run(scenario, func(t *testing.T) {
			repo, db, guildID, leaderID, week := setupGuildLeadershipTest(t)
			var deputy uint32
			if scenario != "solo" {
				deputy = returnGuildTestMember(t, repo, db, guildID, "Deputy")
			}
			var userID uint32
			if err := db.Get(&userID, `SELECT user_id FROM characters WHERE id=$1`, leaderID); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "unknown login":
				returnGuildTestExec(t, db, `UPDATE characters SET last_login=0 WHERE id=$1`, leaderID)
			case "null login":
				returnGuildTestExec(t, db, `UPDATE characters SET last_login=NULL WHERE id=$1`, leaderID)
			case "online binding":
				returnGuildTestExec(t, db, `INSERT INTO sign_sessions(user_id,char_id,token,server_id) VALUES($1,$2,'leader-test',999)`, userID, leaderID)
			case "live connection lease":
				returnGuildTestExec(t, db, `INSERT INTO dashboard_coin_sessions(id,user_id,character_id,server_id,token) VALUES('00000000-0000-4000-8000-000000000001',$1,$2,999,'leader-test')`, userID, leaderID)
			case "permanently banned":
				returnGuildTestExec(t, db, `INSERT INTO bans(user_id,expires) VALUES($1,NULL)`, userID)
			case "deleted leader":
				returnGuildTestExec(t, db, `UPDATE characters SET deleted=true WHERE id=$1`, leaderID)
			case "temporary guild":
				returnGuildTestExec(t, db, `UPDATE guilds SET return_type=2 WHERE id=$1`, guildID)
			case "recent appointment":
				returnGuildTestExec(t, db, `UPDATE guilds SET leader_changed_at=$2 WHERE id=$1`, guildID, week.Add(time.Hour))
			case "leader membership missing":
				returnGuildTestExec(t, db, `DELETE FROM guild_characters WHERE character_id=$1`, leaderID)
			case "no eligible successor":
				returnGuildTestExec(t, db, `UPDATE guild_characters SET order_index=4,avoid_leadership=true WHERE character_id=$1`, deputy)
			}
			before := returnGuildTestSnapshot(t, db)
			if changes := runGuildLeadershipTest(t, repo, week.Add(2*time.Hour)); len(changes) != 0 {
				t.Fatal("protected guild was changed", scenario, changes)
			}
			if after := returnGuildTestSnapshot(t, db); after != before {
				t.Fatal("protected guild metadata/membership changed", before, after)
			}
		})
	}
}

func TestGuildLeadershipTieAndInvalidDeputies(t *testing.T) {
	repo, db, guildID, leaderID, week := setupGuildLeadershipTest(t)
	deleted := returnGuildTestMember(t, repo, db, guildID, "DeletedDeputy")
	first := returnGuildTestMember(t, repo, db, guildID, "TieFirst")
	second := returnGuildTestMember(t, repo, db, guildID, "TieSecond")
	returnGuildTestExec(t, db, `UPDATE characters SET deleted=true WHERE id=$1`, deleted)
	returnGuildTestExec(t, db, `UPDATE guild_characters SET order_index=3 WHERE character_id IN($1,$2)`, first, second)
	changes := runGuildLeadershipTest(t, repo, week)
	if len(changes) != 1 || changes[0] != (GuildLeadershipChange{guildID, leaderID, first}) {
		t.Fatal("invalid deputy skipped; tied order must use character ID", changes)
	}
}

func TestGuildLeadershipStaleBindingDoesNotBlockForever(t *testing.T) {
	repo, db, guildID, leaderID, week := setupGuildLeadershipTest(t)
	returnGuildTestMember(t, repo, db, guildID, "Deputy")
	var userID uint32
	if err := db.Get(&userID, `SELECT user_id FROM characters WHERE id=$1`, leaderID); err != nil {
		t.Fatal(err)
	}
	returnGuildTestExec(t, db, `INSERT INTO sign_sessions(user_id,char_id,token,server_id,last_used_at) VALUES($1,$2,'stale-leader',999,$3)`, userID, leaderID, week.AddDate(0, 0, -30))
	if changes := runGuildLeadershipTest(t, repo, week); len(changes) != 1 {
		t.Fatal("stale offline binding blocked weekly promotion", changes)
	}
}

func TestGuildLeadershipFailureRollsBackCursorAndOrders(t *testing.T) {
	repo, db, guildID, _, week := setupGuildLeadershipTest(t)
	returnGuildTestMember(t, repo, db, guildID, "Deputy")
	returnGuildTestExec(t, db, `CREATE FUNCTION reject_leader_test() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected leadership failure'; END $$;
		CREATE TRIGGER reject_leader_test BEFORE UPDATE OF leader_id ON guilds FOR EACH ROW EXECUTE FUNCTION reject_leader_test()`)
	t.Cleanup(func() {
		returnGuildTestExec(t, db, `DROP TRIGGER IF EXISTS reject_leader_test ON guilds; DROP FUNCTION IF EXISTS reject_leader_test()`)
	})
	before := guildLeadershipTestSnapshot(t, db)
	complete, changes, err := repo.RotateInactiveGuildLeaders(context.Background(), week)
	if err == nil || complete || len(changes) != 0 {
		t.Fatal("injected update failure not propagated", complete, changes, err)
	}
	if after := guildLeadershipTestSnapshot(t, db); after != before {
		t.Fatal("failed promotion changed cursor/order/leader", before, after)
	}
	returnGuildTestExec(t, db, `DROP TRIGGER reject_leader_test ON guilds`)
	if changes := runGuildLeadershipTest(t, repo, week); len(changes) != 1 {
		t.Fatal("failed maintenance must remain retryable", changes)
	}
}

func TestGuildLeadershipConcurrentChannelsAndBusyRetry(t *testing.T) {
	repo, db, guildID, _, week := setupGuildLeadershipTest(t)
	returnGuildTestMember(t, repo, db, guildID, "Deputy")
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock($1,$2)`, returnGuildLockNamespace, 0); err != nil {
		t.Fatal(err)
	}
	complete, changes, err := repo.RotateInactiveGuildLeaders(context.Background(), week)
	if complete || len(changes) != 0 || err != nil {
		t.Fatal("busy transition must be retried, not marked complete", complete, changes, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	counts := make(chan int, 8)
	for i := 0; i < cap(counts); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, changes, err := repo.RotateInactiveGuildLeaders(context.Background(), week)
			if err != nil {
				t.Error(err)
			}
			counts <- len(changes)
		}()
	}
	wg.Wait()
	close(counts)
	total := 0
	for count := range counts {
		total += count
	}
	if total != 1 {
		t.Fatal("channels duplicated the promotion", total)
	}
	if changes := runGuildLeadershipTest(t, repo, week); len(changes) != 0 {
		t.Fatal("restart repeated committed promotion", changes)
	}
}

func TestGuildLeadershipFirstInitializationDoesNotDemote(t *testing.T) {
	repo, db, guildID, _, week := setupGuildLeadershipTest(t)
	returnGuildTestMember(t, repo, db, guildID, "Deputy")
	returnGuildTestExec(t, db, `DELETE FROM guild_leadership_maintenance`)
	if changes := runGuildLeadershipTest(t, repo, week); len(changes) != 0 {
		t.Fatal("first initialization must not immediately demote", changes)
	}
	if changes := runGuildLeadershipTest(t, repo, week.AddDate(0, 0, 7)); len(changes) != 1 {
		t.Fatal("next scheduled Monday must run normally", changes)
	}
}

func TestGuildLeadershipManualResignationAtomic(t *testing.T) {
	repo, db, guildID, leaderID, _ := setupGuildLeadershipTest(t)
	deputy := returnGuildTestMember(t, repo, db, guildID, "Deputy")
	next := returnGuildTestMember(t, repo, db, guildID, "Next")
	returnGuildTestExec(t, db, `UPDATE guild_characters SET avoid_leadership=true WHERE character_id=$1`, deputy)
	if got, err := repo.ResignLeadership(guildID, deputy); !errors.Is(err, ErrUnauthorized) || got != 0 {
		t.Fatal("nonleader resigned another character", got, err)
	}
	before := guildLeadershipTestSnapshot(t, db)
	returnGuildTestExec(t, db, `CREATE FUNCTION reject_manual_leader_test() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected manual failure'; END $$;
		CREATE TRIGGER reject_manual_leader_test BEFORE UPDATE OF leader_id ON guilds FOR EACH ROW EXECUTE FUNCTION reject_manual_leader_test()`)
	t.Cleanup(func() {
		returnGuildTestExec(t, db, `DROP TRIGGER IF EXISTS reject_manual_leader_test ON guilds; DROP FUNCTION IF EXISTS reject_manual_leader_test()`)
	})
	if got, err := repo.ResignLeadership(guildID, leaderID); err == nil || got != 0 {
		t.Fatal("failed manual handover returned success", got, err)
	}
	if after := guildLeadershipTestSnapshot(t, db); after != before {
		t.Fatal("manual failure partially saved member order", before, after)
	}
	returnGuildTestExec(t, db, `DROP TRIGGER reject_manual_leader_test ON guilds`)
	if got, err := repo.ResignLeadership(guildID, leaderID); err != nil || got != next {
		t.Fatal("manual resignation must still skip refusal", got, err)
	}
	if got := guildLeadershipTestOrder(t, db, guildID); !reflect.DeepEqual(got, []uint32{next, deputy, leaderID}) {
		t.Fatal(got)
	}
}

type mockGuildLeaderRotator struct {
	*mockGuildRepo
	called chan time.Time
}

func (m *mockGuildLeaderRotator) RotateInactiveGuildLeaders(_ context.Context, now time.Time) (bool, []GuildLeadershipChange, error) {
	m.called <- now
	return true, nil, nil
}

func TestGuildLeadershipWorkerShutdownAndNoChat(t *testing.T) {
	s := createMockServer()
	s.db = &sqlx.DB{} // The fake rotator does not use a DB connection.
	s.done = make(chan struct{})
	repo := &mockGuildLeaderRotator{&mockGuildRepo{}, make(chan time.Time, 2)}
	s.guildRepo = repo
	finished := make(chan struct{})
	go func() { defer close(finished); s.rotateInactiveGuildLeaders() }()
	select {
	case <-repo.called:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not perform startup catch-up")
	}
	close(s.done)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("weekly worker ignored shutdown")
	}
	if len(repo.called) != 0 {
		t.Fatal("worker repeated same week's maintenance")
	}
	// No player notification path is invoked by the worker.
	if len(s.sessions) != 0 {
		t.Fatal(fmt.Sprint(s.sessions))
	}
}

func TestGuildLeadershipWorkerWithoutDBOrInProxyMode(t *testing.T) {
	for _, proxy := range []bool{false, true} {
		t.Run(fmt.Sprint(proxy), func(t *testing.T) {
			s := createMockServer()
			repo := &mockGuildLeaderRotator{&mockGuildRepo{}, make(chan time.Time, 1)}
			s.guildRepo = repo
			if proxy {
				s.db = &sqlx.DB{}
				s.erupeConfig.DebugOptions.ProxyPort = 1
			}
			s.rotateInactiveGuildLeaders()
			if len(repo.called) != 0 {
				t.Fatal("disabled worker queried maintenance")
			}
		})
	}
}
