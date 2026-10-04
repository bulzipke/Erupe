package channelserver

import (
	"fmt"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// This suite uses its own schema on a disposable loopback server. It never
// calls SetupTestDB, which rebuilds public, and refuses the production port.
func towerD463DB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("D463_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("D463_TEST_POSTGRES_URL unset")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Port() != "55463" {
		t.Fatal("requires disposable loopback port 55463")
	}
	admin, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("d463_%x", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sqlx.Connect("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); admin.Exec("DROP SCHEMA " + schema + " CASCADE"); admin.Close() })
	ddl := `CREATE TABLE characters(id integer PRIMARY KEY,name text);
 CREATE TABLE guilds(id integer PRIMARY KEY,tower_mission_page integer DEFAULT 1,tower_rp integer DEFAULT 0);
 CREATE TABLE guild_characters(id serial PRIMARY KEY,guild_id integer,character_id integer,tower_mission_1 integer,tower_mission_2 integer,tower_mission_3 integer);
 CREATE TABLE tower(char_id integer PRIMARY KEY REFERENCES characters(id),tr integer DEFAULT 1,trp integer DEFAULT 0,tsp integer DEFAULT 0,block1 integer DEFAULT 0,block2 integer DEFAULT 0,skills text,gems text);
 INSERT INTO characters VALUES(100,'sender'),(101,'receiver'),(102,'outsider');INSERT INTO tower(char_id) VALUES(100),(101),(102);
 INSERT INTO guilds(id) VALUES(1),(2);
 INSERT INTO guild_characters(guild_id,character_id) VALUES(1,100),(1,101),(2,102);`
	if _, err = db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"0063_tower_gem_history.sql", "0064_tower_guardian_kills.sql", "0065_tower_event_rewards.sql", "0066_tower_daily_bin.sql", "0068_tower_guardian_milestones.sql", "0069_tower_settlements.sql", "0070_tower_gem_deposits.sql", "0071_tower_gem_notices.sql", "0073_tower_event_lifecycle.sql"} {
		b, e := os.ReadFile(filepath.Join("..", "migrations", "sql", file))
		if e != nil {
			t.Fatal(e)
		}
		sql := string(b)
		if _, e = db.Exec(sql); e != nil {
			t.Fatalf("%s: %v", file, e)
		}
	}
	return db
}
func TestTowerD463PostgresAtomicRetryDepositAndGift(t *testing.T) {
	db := towerD463DB(t)
	r := NewTowerRepository(db)
	day := time.Date(2026, 9, 28, 3, 0, 0, 0, time.UTC)
	run := strings.Repeat("a", 32)
	v := TowerSettlement{Block: 1, TR: 3, TRP: 200, TSP: 2, Floors: 2}
	// Fail the final daily write after rank/floors and event counters have changed.
	if _, e := db.Exec(`ALTER TABLE tower_daily_progress ADD CONSTRAINT injected_failure CHECK(trp<>200)`); e != nil {
		t.Fatal(e)
	}
	if _, e := r.SettleTowerRun(100, run, 36, day, v); e == nil {
		t.Fatal("injected late failure succeeded")
	}
	var trp, floor, receipts, event int
	if e := db.QueryRow(`SELECT trp,block1,(SELECT count(*) FROM tower_settlements),(SELECT count(*) FROM tower_event_progress) FROM tower WHERE char_id=100`).Scan(&trp, &floor, &receipts, &event); e != nil {
		t.Fatal(e)
	}
	if trp != 0 || floor != 0 || receipts != 0 || event != 0 {
		t.Fatalf("partial commit %d/%d/%d/%d", trp, floor, receipts, event)
	}
	if _, e := db.Exec(`ALTER TABLE tower_daily_progress DROP CONSTRAINT injected_failure`); e != nil {
		t.Fatal(e)
	}
	// Concurrent identical requests, including a commit whose ACK was lost.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gain, e := r.SettleTowerRun(100, run, 36, day, v)
			if e == nil && gain != 2 {
				e = fmt.Errorf("gain %d", gain)
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if e := db.QueryRow(`SELECT trp,block1,(SELECT count(*) FROM tower_settlements),(SELECT block1_floors FROM tower_event_progress) FROM tower WHERE char_id=100`).Scan(&trp, &floor, &receipts, &event); e != nil {
		t.Fatal(e)
	}
	if trp != 200 || floor != 2 || receipts != 1 || event != 2 {
		t.Fatalf("duplicate credit %d/%d/%d/%d", trp, floor, receipts, event)
	}
	v.TRP++
	if _, e := r.SettleTowerRun(100, run, 36, day, v); e == nil {
		t.Fatal("changed retry accepted")
	}
	if e := r.DepositTowerGem(100, strings.Repeat("b", 32), 0, 1, 2); e == nil {
		t.Fatal("unsettled deposit accepted")
	}
	for i := 0; i < 2; i++ {
		if e := r.DepositTowerGem(100, run, 0, 1, 2); e != nil {
			t.Fatal(e)
		}
	}
	if e := r.DepositTowerGem(100, run, 0, 1, 3); e == nil {
		t.Fatal("changed gem retry accepted")
	}
	csv, e := r.GetGems(100)
	if e != nil || !strings.HasPrefix(csv, "2,") {
		t.Fatalf("gem deposit %q %v", csv, e)
	}
	if e := r.TransferGem(100, 102, 1, 0); e == nil {
		t.Fatal("cross-guild gift accepted")
	}
	// An error when recording history must roll back both inventories.
	if _, e := db.Exec(`ALTER TABLE tower_gem_history ADD CONSTRAINT injected_gift CHECK(message<>5)`); e != nil {
		t.Fatal(e)
	}
	if e := r.TransferGem(100, 101, 1, 5); e == nil {
		t.Fatal("late gift failure succeeded")
	}
	csv, e = r.GetGems(100)
	if e != nil || !strings.HasPrefix(csv, "2,") {
		t.Fatal("failed gift consumed sender inventory")
	}
	if e := r.TransferGem(100, 101, 1, 0); e != nil {
		t.Fatal(e)
	}
	if e := r.TransferGem(100, 101, 1, 0); e == nil {
		t.Fatal("duplicate gift accepted")
	}
	history, e := r.GetGemHistory(101)
	if e != nil || len(history) != 1 {
		t.Fatalf("history=%v %v", history, e)
	}
	latest, seen, e := r.GetGemNotice(101)
	if e != nil || latest != history[0].ID || seen != 0 {
		t.Fatal("new notice missing")
	}
	// Add a concurrent receipt after the displayed snapshot, then clear only snapshot.
	if _, e := db.Exec(`INSERT INTO tower_gem_history(receiver_id,sender_id,gem_id,message) VALUES(101,100,2,0)`); e != nil {
		t.Fatal(e)
	}
	if e := r.ReadGemNotice(101, latest); e != nil {
		t.Fatal(e)
	}
	next, read, e := r.GetGemNotice(101)
	if e != nil || next <= read || read != latest {
		t.Fatal("unseen concurrent gift lost")
	}
}
