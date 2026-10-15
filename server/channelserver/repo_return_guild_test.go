package channelserver

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
)

func returnGuildTestExec(t *testing.T, db *sqlx.DB, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func returnGuildTestMember(t *testing.T, repo *GuildRepository, db *sqlx.DB, guildID uint32, name string) uint32 {
	t.Helper()
	userID := CreateTestUser(t, db, name)
	charID := CreateTestCharacter(t, db, userID, name)
	if err := repo.AddMember(guildID, charID); err != nil {
		t.Fatal(err)
	}
	return charID
}

// Capture entire relevant rows, including RP, join dates, order, applications,
// invitations and alliances. Rollback must restore more than just guild_id.
func returnGuildTestSnapshot(t *testing.T, db *sqlx.DB) string {
	t.Helper()
	var state string
	if err := db.Get(&state, `SELECT json_build_array(
		(SELECT json_agg(g ORDER BY id) FROM guilds g),
		(SELECT json_agg(m ORDER BY id) FROM guild_characters m),
		(SELECT json_agg(a ORDER BY id) FROM guild_applications a),
		(SELECT json_agg(i ORDER BY id) FROM guild_invites i),
		(SELECT json_agg(a ORDER BY id) FROM guild_alliances a))::text`); err != nil {
		t.Fatal(err)
	}
	return state
}

func returnGuildTestDestination(t *testing.T, repo *GuildRepository, charID, wantID uint32) {
	t.Helper()
	g, err := repo.GetByCharID(charID)
	if err != nil || g == nil || g.ID != wantID || g.ReturnType != 2 || g.LeaderCharID != 0 {
		t.Fatal("invalid return guild membership", g, err)
	}
}

func TestReturnGuildMoveOrdinaryMember(t *testing.T) {
	repo, db, oldID, leaderID := setupGuildRepo(t)
	charID := returnGuildTestMember(t, repo, db, oldID, "MovingMember")
	returnGuildTestExec(t, db, `UPDATE guilds SET rank_rp=1234,event_rp=321,pugi_name_1='Pugi' WHERE id=$1`, oldID)
	returnGuildTestExec(t, db, `INSERT INTO guild_applications(guild_id,character_id,actor_id,application_type) VALUES($1,$2,$2,'applied')`, oldID, charID)
	id, err := repo.JoinReturnGuild(charID, 2, "Return %d")
	if err != nil {
		t.Fatal(err)
	}
	returnGuildTestDestination(t, repo, charID, id)
	var unchanged bool
	if err = db.Get(&unchanged, `SELECT leader_id=$2 AND rank_rp=1234 AND event_rp=321 AND pugi_name_1='Pugi'
		AND (SELECT COUNT(*) FROM guild_characters WHERE guild_id=$1)=1 FROM guilds WHERE id=$1`, oldID, leaderID); err != nil || !unchanged {
		t.Fatal("ordinary member move changed old guild", unchanged, err)
	}
	var count int
	if err = db.Get(&count, `SELECT COUNT(*) FROM guild_applications WHERE character_id=$1`, charID); err != nil || count != 0 {
		t.Fatal("pending applications survived move", count, err)
	}
}

func TestReturnGuildMoveLeaderSelectsEligibleSuccessor(t *testing.T) {
	repo, db, oldID, leaderID := setupGuildRepo(t)
	avoidID := returnGuildTestMember(t, repo, db, oldID, "AvoidLeader")
	nextID := returnGuildTestMember(t, repo, db, oldID, "NextLeader")
	returnGuildTestExec(t, db, `UPDATE guild_characters SET avoid_leadership=true WHERE character_id=$1`, avoidID)
	returnGuildTestExec(t, db, `UPDATE guild_characters SET rp_today=17 WHERE character_id=$1`, nextID)
	returnGuildTestExec(t, db, `UPDATE guilds SET rank_rp=1234,event_rp=321,pugi_name_1='Pugi' WHERE id=$1`, oldID)
	id, err := repo.JoinReturnGuild(leaderID, 2, "Return %d")
	if err != nil {
		t.Fatal(err)
	}
	returnGuildTestDestination(t, repo, leaderID, id)
	var correct bool
	if err = db.Get(&correct, `SELECT leader_id=$2 AND rank_rp=1234 AND event_rp=321 AND pugi_name_1='Pugi'
		AND (SELECT COUNT(*) FROM guild_characters WHERE guild_id=$1)=2
		AND EXISTS(SELECT 1 FROM guild_characters WHERE guild_id=$1 AND character_id=$2 AND order_index=1 AND rp_today=17)
		FROM guilds WHERE id=$1`, oldID, nextID); err != nil || !correct {
		t.Fatal("leader handover lost guild/member state", correct, err)
	}
}

func TestReturnGuildMoveWithoutSuccessorRollsBack(t *testing.T) {
	repo, db, oldID, leaderID := setupGuildRepo(t)
	charID := returnGuildTestMember(t, repo, db, oldID, "AvoidLeader")
	returnGuildTestExec(t, db, `UPDATE guild_characters SET avoid_leadership=true WHERE character_id=$1`, charID)
	before := returnGuildTestSnapshot(t, db)
	if _, err := repo.JoinReturnGuild(leaderID, 2, "Return %d"); !errors.Is(err, ErrNoEligibleLeader) {
		t.Fatal(err)
	}
	if after := returnGuildTestSnapshot(t, db); after != before {
		t.Fatal("failed handover changed guild state", before, after)
	}
}

func TestReturnGuildMoveSoloLeaderDisbandsWithPendingReferences(t *testing.T) {
	for _, role := range []string{"parent", "sub1", "sub2"} {
		t.Run(role, func(t *testing.T) {
			repo, db, oldID, leaderID := setupGuildRepo(t)
			userID := CreateTestUser(t, db, "applicant")
			charID := CreateTestCharacter(t, db, userID, "Applicant")
			returnGuildTestExec(t, db, `INSERT INTO guild_applications(guild_id,character_id,actor_id,application_type) VALUES($1,$2,$2,'applied')`, oldID, charID)
			returnGuildTestExec(t, db, `INSERT INTO guild_invites(guild_id,character_id,actor_id) VALUES($1,$2,$3)`, oldID, charID, leaderID)
			// The alliance IDs have no guild FK; distinct sentinels expose cleanup
			// without creating other guild fixtures or affecting allocation.
			parent, sub1, sub2 := uint32(8001), uint32(8002), uint32(8003)
			switch role {
			case "parent":
				parent = oldID
			case "sub1":
				sub1 = oldID
			case "sub2":
				sub2 = oldID
			}
			returnGuildTestExec(t, db, `INSERT INTO guild_alliances(name,parent_id,sub1_id,sub2_id) VALUES('Alliance',$1,$2,$3)`, parent, sub1, sub2)
			id, err := repo.JoinReturnGuild(leaderID, 2, "Return %d")
			if err != nil {
				t.Fatal(err)
			}
			returnGuildTestDestination(t, repo, leaderID, id)
			var clean bool
			if err = db.Get(&clean, `SELECT NOT EXISTS(SELECT 1 FROM guilds WHERE id=$1)
				AND NOT EXISTS(SELECT 1 FROM guild_applications WHERE guild_id=$1)
				AND NOT EXISTS(SELECT 1 FROM guild_invites WHERE guild_id=$1)
				AND NOT EXISTS(SELECT 1 FROM guild_alliances WHERE parent_id=$1 OR sub1_id=$1 OR sub2_id=$1)`, oldID); err != nil || !clean {
				t.Fatal("dangling reference after automatic disband", clean, err)
			}
			if role == "sub1" {
				if err = db.Get(&clean, `SELECT sub1_id=8003 AND sub2_id IS NULL FROM guild_alliances`); err != nil || !clean {
					t.Fatal("remaining alliance member not shifted", clean, err)
				}
			}
		})
	}
}

func TestReturnGuildMoveInsertFailureRestoresEverything(t *testing.T) {
	for _, scenario := range []string{"member", "leader", "solo leader"} {
		t.Run(scenario, func(t *testing.T) {
			repo, db, oldID, leaderID := setupGuildRepo(t)
			charID := leaderID
			if scenario != "solo leader" {
				memberID := returnGuildTestMember(t, repo, db, oldID, "OtherMember")
				if scenario == "member" {
					charID = memberID
				}
			}
			returnGuildTestExec(t, db, `UPDATE guilds SET rank_rp=1234,event_rp=567,comment='keep',pugi_name_1='Pugi' WHERE id=$1`, oldID)
			returnGuildTestExec(t, db, `UPDATE guild_characters SET rp_today=17,recruiter=true WHERE character_id=$1`, charID)
			returnGuildTestExec(t, db, `INSERT INTO guild_applications(guild_id,character_id,actor_id,application_type) VALUES($1,$2,$2,'applied')`, oldID, charID)
			returnGuildTestExec(t, db, `INSERT INTO guild_invites(guild_id,character_id,actor_id) VALUES($1,$2,$2)`, oldID, charID)
			returnGuildTestExec(t, db, `INSERT INTO guild_alliances(name,parent_id) VALUES('KeepAlliance',$1)`, oldID)
			returnGuildTestExec(t, db, `CREATE FUNCTION reject_return_join_test() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN IF EXISTS(SELECT 1 FROM guilds WHERE id=NEW.guild_id AND return_type>0) THEN
				RAISE EXCEPTION 'injected return guild insert failure'; END IF; RETURN NEW; END $$;
				CREATE TRIGGER reject_return_join_test BEFORE INSERT ON guild_characters
				FOR EACH ROW EXECUTE FUNCTION reject_return_join_test()`)
			t.Cleanup(func() {
				returnGuildTestExec(t, db, `DROP TRIGGER IF EXISTS reject_return_join_test ON guild_characters; DROP FUNCTION IF EXISTS reject_return_join_test()`)
			})
			before := returnGuildTestSnapshot(t, db)
			if _, err := repo.JoinReturnGuild(charID, 2, "Return %d"); err == nil {
				t.Fatal("injected destination failure was ignored")
			}
			if after := returnGuildTestSnapshot(t, db); after != before {
				t.Fatal("destination failure did not restore all source state", before, after)
			}
		})
	}
}

func TestReturnGuildMoveConcurrentDuplicatePreservesJoin(t *testing.T) {
	repo, db, oldID, charID := setupGuildRepo(t)
	returnGuildTestMember(t, repo, db, oldID, "NextLeader")
	var wg sync.WaitGroup
	ids := make(chan uint32, 8)
	for i := 0; i < cap(ids); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := repo.JoinReturnGuild(charID, 2, "Return %d")
			if err != nil {
				t.Error(err)
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	var wantID uint32
	for id := range ids {
		if wantID != 0 && id != wantID {
			t.Fatal("duplicate packets joined different guilds", id, wantID)
		}
		wantID = id
	}
	before := returnGuildTestSnapshot(t, db)
	id, err := repo.JoinReturnGuild(charID, 2, "Return %d")
	if err != nil || id != wantID {
		t.Fatal(id, err)
	}
	if after := returnGuildTestSnapshot(t, db); after != before {
		t.Fatal("retry rewrote membership or guild state", before, after)
	}
	returnGuildTestDestination(t, repo, charID, wantID)
}

func TestReturnGuildMoveConcurrentLastMembers(t *testing.T) {
	repo, db, oldID, leaderID := setupGuildRepo(t)
	memberID := returnGuildTestMember(t, repo, db, oldID, "OtherMember")
	var wg sync.WaitGroup
	for _, charID := range []uint32{leaderID, memberID} {
		wg.Add(1)
		go func(charID uint32) {
			defer wg.Done()
			if _, err := repo.JoinReturnGuild(charID, 2, "Return %d"); err != nil {
				t.Error(err)
			}
		}(charID)
	}
	wg.Wait()
	var correct bool
	if err := db.Get(&correct, `SELECT NOT EXISTS(SELECT 1 FROM guilds WHERE id=$1)
		AND (SELECT COUNT(*) FROM guild_characters WHERE character_id IN($2,$3))=2
		AND (SELECT COUNT(DISTINCT guild_id) FROM guild_characters WHERE character_id IN($2,$3))=1`, oldID, leaderID, memberID); err != nil || !correct {
		t.Fatal("concurrent final departures lost membership or left old guild", correct, err)
	}
}

func TestReturnGuildMoveSwitchesTemporaryType(t *testing.T) {
	db := SetupTestDB(t)
	repo := NewGuildRepository(db)
	userID := CreateTestUser(t, db, "temp_switch")
	charID := CreateTestCharacter(t, db, userID, "SwitchMember")
	oldID, err := repo.JoinReturnGuild(charID, 1, "Rookie %d")
	if err != nil {
		t.Fatal(err)
	}
	id, err := repo.JoinReturnGuild(charID, 2, "Return %d")
	if err != nil || id == oldID {
		t.Fatal(id, err)
	}
	returnGuildTestDestination(t, repo, charID, id)
	var reusable bool
	if err = db.Get(&reusable, `SELECT return_type=1 AND leader_id=0 AND NOT EXISTS(SELECT 1 FROM guild_characters WHERE guild_id=$1) FROM guilds WHERE id=$1`, oldID); err != nil || !reusable {
		t.Fatal("empty automatic guild should remain reusable", reusable, err)
	}
}

func TestReturnGuildMoveConcurrentCapacityAllocation(t *testing.T) {
	db := SetupTestDB(t)
	repo := NewGuildRepository(db)
	userID := CreateTestUser(t, db, "move_capacity")
	id, err := repo.FindOrCreateReturnGuild(2, "Return %d")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 59; i++ {
		charID := CreateTestCharacter(t, db, userID, fmt.Sprintf("Member%d", i))
		if err = repo.AddMember(id, charID); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		charID := CreateTestCharacter(t, db, userID, fmt.Sprintf("Last%d", i))
		wg.Add(1)
		go func(charID uint32) {
			defer wg.Done()
			if _, err := repo.JoinReturnGuild(charID, 2, "Return %d"); err != nil {
				t.Error(err)
			}
		}(charID)
	}
	wg.Wait()
	var correct bool
	if err = db.Get(&correct, `SELECT COUNT(*)=2 AND SUM(n)=61 AND MAX(n)=60 FROM
		(SELECT COUNT(*) n FROM guild_characters GROUP BY guild_id) counts`); err != nil || !correct {
		t.Fatal("allocation exceeded member limit or lost join", correct, err)
	}
}

func TestReturnGuildMoveInvalidRequestPreservesState(t *testing.T) {
	repo, db, _, charID := setupGuildRepo(t)
	before := returnGuildTestSnapshot(t, db)
	for _, args := range [][2]uint32{{charID, 0}, {charID, 3}, {999999, 2}} {
		if _, err := repo.JoinReturnGuild(args[0], uint8(args[1]), "Return %d"); err == nil {
			t.Fatal("invalid request succeeded", args)
		}
		if after := returnGuildTestSnapshot(t, db); after != before {
			t.Fatal("invalid request changed state", before, after)
		}
	}
}
