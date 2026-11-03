package migrations

import (
	"reflect"
	"testing"

	"github.com/lib/pq"
)

func TestDivaBeadRotationUpgradeAndRecovery(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "configured eighteen"}[configured], func(t *testing.T) {
			db := testDB(t)
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE events(id integer PRIMARY KEY,event_type text);
				CREATE TABLE diva_beads(id serial PRIMARY KEY,type integer);
				CREATE TABLE diva_event_lifecycle(mode integer PRIMARY KEY,event_id integer);
				CREATE TABLE diva_debug_song_event(singleton boolean PRIMARY KEY,event_id integer);
				CREATE TABLE existing_progress(event_id integer,points bigint);
				INSERT INTO events VALUES(10,'diva'),(11,'diva'),(12,'diva'),(13,'diva'),(14,'festa');
				INSERT INTO diva_event_lifecycle VALUES(-1,10),(2,11),(3,12);
				INSERT INTO diva_debug_song_event VALUES(TRUE,13);
				INSERT INTO existing_progress VALUES(10,123456);`); err != nil {
				t.Fatal(err)
			}
			want := pq.Int64Array{1, 3, 4, 8}
			if configured {
				if _, err := db.Exec(`INSERT INTO diva_beads(type) VALUES(25),(19),(14),(17),
					(1),(3),(4),(8),(9),(10),(11),(15),(18),(20),(21),(22),(23),(24)`); err != nil {
					t.Fatal(err)
				}
				want = pq.Int64Array{25, 19, 14, 17}
			}
			migration, err := migrationFS.ReadFile("sql/0105_diva_bead_rotation.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(string(migration)); err != nil {
				t.Fatal(err)
			}
			for id, mode := range map[int]int{10: -1, 11: 2, 12: 3, 13: 1} {
				var effects pq.Int64Array
				var actualMode int
				if err = db.QueryRow(`SELECT effects,mode FROM diva_bead_rounds WHERE event_id=$1`, id).Scan(&effects, &actualMode); err != nil || actualMode != mode || !reflect.DeepEqual(effects, want) {
					t.Fatal("legacy selection/mode changed", id, effects, actualMode, err)
				}
			}
			if _, err = db.Exec(`DELETE FROM diva_beads;
				INSERT INTO diva_beads(type) VALUES(20),(21),(22),(23);
				INSERT INTO events VALUES(20,'diva'),(21,'diva');
				INSERT INTO diva_bead_rounds VALUES(20,-1,ARRAY[1,14,17,19],array_fill(1,ARRAY[13]),2,1,1234,10);
				UPDATE diva_event_lifecycle SET event_id=20 WHERE mode=-1;`); err != nil {
				t.Fatal(err)
			}
			var before, after string
			const digest = `SELECT md5(string_agg(row_to_json(r)::text,',' ORDER BY event_id)) FROM diva_bead_rounds r`
			if err = db.Get(&before, digest); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(string(migration)); err != nil {
				t.Fatal(err)
			}
			if err = db.Get(&after, digest); err != nil || before != after {
				t.Fatal("recovery overwrote snapshots or froze future event", before, after, err)
			}
			var points int
			if err = db.Get(&points, `SELECT points FROM existing_progress WHERE event_id=10`); err != nil || points != 123456 {
				t.Fatal("existing progress altered", points, err)
			}
			for _, invalid := range []string{
				`UPDATE diva_bead_rounds SET effects=ARRAY[1,1,17,19] WHERE event_id=20`,
				`UPDATE diva_bead_rounds SET effects=ARRAY[1,2,17,19] WHERE event_id=20`,
				`UPDATE diva_bead_rounds SET effects=ARRAY[1,14,17,19,25] WHERE event_id=20`,
				`UPDATE diva_bead_rounds SET effects=ARRAY[1,14,17,NULL] WHERE event_id=20`,
				`UPDATE diva_bead_rounds SET seed=NULL WHERE event_id=20`,
				`UPDATE diva_bead_rounds SET same_count=3 WHERE event_id=20`,
				`UPDATE diva_bead_rounds SET support_absence=array_fill(37,ARRAY[13]) WHERE event_id=20`,
				`UPDATE diva_bead_rounds SET support_absence=ARRAY[1] WHERE event_id=20`,
			} {
				if _, err = db.Exec(invalid); err == nil {
					t.Error("invalid rotation state accepted", invalid)
				}
			}
		})
	}
}
