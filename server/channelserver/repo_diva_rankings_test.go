package channelserver

import (
	"testing"
	"time"
)

func TestRepoDivaGuildRankingHistoricalMembership(t *testing.T) {
	r, db := setupDivaRepo(t)
	uid := CreateTestUser(t, db, "diva_guild_rank")
	a := CreateTestCharacter(t, db, uid, "A")
	b := CreateTestCharacter(t, db, uid, "B")
	c := CreateTestCharacter(t, db, uid, "C")
	ga := CreateTestGuild(t, db, a, "Alpha")
	gb := CreateTestGuild(t, db, c, "Beta")
	if _, err := db.Exec(`INSERT INTO guild_characters(guild_id,character_id) VALUES($1,$2)`, ga, b); err != nil {
		t.Fatal(err)
	}
	now := divaTestTime(21, 11, 0)
	event, err := r.EnsureDivaSongEvent(now)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ charID, points uint32 }{{a, 10}, {b, 10}, {c, 15}} {
		if err := r.RecordDivaPoints(row.charID, event.ID, row.points, 0, now); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := r.GetDivaGuildRanking(event.ID, a, now.Add(time.Second))
	if err != nil || len(rows) != 2 || rows[0].GuildID != ga || rows[0].Points != 20 || rows[0].Rank != 1 || !rows[0].IsOwn {
		t.Fatalf("guild rank must be SUM not AVG: %+v %v", rows, err)
	}
	// A cutoff excludes a contribution occurring at exactly that time.
	rows, err = r.GetDivaGuildRanking(event.ID, a, now)
	if err != nil || len(rows) != 1 || rows[0].Rank != 0 || rows[0].Name != "Alpha" {
		t.Fatalf("unpublished guild: %+v %v", rows, err)
	}
	// Transferring never drags the old contribution into the new guild.
	if _, err := db.Exec(`UPDATE guild_characters SET guild_id=$1 WHERE character_id=$2`, gb, b); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDivaPoints(b, event.ID, 5, 0, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Legacy points with unknown historical membership remain individual only.
	if _, err := db.Exec(`INSERT INTO diva_song_records(char_id,event_id,day_start,bead_index,quest_points,bonus_points,submitted_at)
		VALUES($1,$2,$3,0,999,0,$3)`, b, event.ID, now); err != nil {
		t.Fatal(err)
	}
	rows, err = r.GetDivaGuildRanking(event.ID, b, now.Add(2*time.Minute))
	if err != nil || len(rows) != 2 || rows[0].Points != 20 || rows[1].Points != 20 || rows[0].Rank != 1 || rows[1].Rank != 1 || rows[0].IsOwn || !rows[1].IsOwn {
		t.Fatalf("historical guild/tie changed: %+v %v", rows, err)
	}
	personal, err := r.GetDivaRanking(event.ID, b, now.Add(2*time.Minute))
	if err != nil || len(personal) != 3 || personal[0].CharID != b || personal[0].Points != 1014 {
		t.Fatalf("legacy personal points lost: %+v %v", personal, err)
	}
	// A separate round and out-of-song timestamps cannot leak into this ranking.
	if _, err := db.Exec(`INSERT INTO diva_song_records(char_id,event_id,day_start,bead_index,quest_points,bonus_points,submitted_at,guild_id)
		VALUES($1,$2,$3,0,10000,0,$3,$4)`, a, event.ID, time.Unix(int64(event.StartTime)+divaPhaseDuration, 0), ga); err != nil {
		t.Fatal(err)
	}
	rows, err = r.GetDivaGuildRanking(event.ID, a, now.Add(20*24*time.Hour))
	if err != nil || len(rows) != 2 || rows[0].Points != 20 {
		t.Fatalf("out-of-phase contribution leaked: %+v %v", rows, err)
	}
	if err := r.InsertEvent(uint32(now.Add(30 * 24 * time.Hour).Unix())); err != nil {
		t.Fatal(err)
	}
	events, err := r.GetEvents()
	if err != nil {
		t.Fatal(err)
	}
	rows, err = r.GetDivaGuildRanking(events[len(events)-1].ID, a, now.Add(40*24*time.Hour))
	if err != nil || len(rows) != 1 || rows[0].Rank != 0 || rows[0].Points != 0 {
		t.Fatalf("old round leaked: %+v %v", rows, err)
	}
}

func TestRepoDivaGuildRankingOwnOutsideTop100(t *testing.T) {
	r, db := setupDivaRepo(t)
	uid := CreateTestUser(t, db, "diva_rank_101")
	cid := CreateTestCharacter(t, db, uid, "Hunter")
	own := CreateTestGuild(t, db, cid, "Own")
	now := divaTestTime(21, 11, 0)
	event, err := r.EnsureDivaSongEvent(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDivaPoints(cid, event.ID, 1, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`WITH g AS (
		INSERT INTO guilds(name,leader_id) SELECT 'Rank'||n,0 FROM generate_series(1,100) n RETURNING id
	) INSERT INTO diva_song_records(char_id,event_id,day_start,bead_index,quest_points,bonus_points,submitted_at,guild_id)
		SELECT $1,$2,$3,0,1000+id,0,$3,id FROM g`, cid, event.ID, now); err != nil {
		t.Fatal(err)
	}
	rows, err := r.GetDivaGuildRanking(event.ID, cid, now.Add(time.Second))
	if err != nil || len(rows) != 101 || rows[100].GuildID != own || rows[100].Rank != 101 || !rows[100].IsOwn {
		t.Fatalf("top100+own: count=%d err=%v", len(rows), err)
	}
	if len(divaRankingPayload(2, rows)) != 3100 {
		t.Fatal("wire exceeded top100")
	}
}
