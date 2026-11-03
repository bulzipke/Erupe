package channelserver

import (
	"encoding/binary"
	"reflect"
	"sync"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"github.com/lib/pq"
)

var _ DivaEventBeadRepository = (*DivaRepository)(nil)

func TestRepoDivaBeadRotationSnapshotsAndConcurrentRollover(t *testing.T) {
	r, db := setupDivaRepo(t)
	if _, err := db.Exec(`INSERT INTO diva_beads(type) VALUES(25),(19),(14),(17)`); err != nil {
		t.Fatal(err)
	}
	now := divaTestTime(21, 12, 0)
	first, err := r.EnsureDivaEvent(now, -1)
	if err != nil {
		t.Fatal(err)
	}
	old := []int{25, 19, 14, 17}
	if ids, err := r.GetEventBeads(first.ID); err != nil || !reflect.DeepEqual(ids, old) {
		t.Fatal("current quartet changed", ids, err)
	}
	if _, err = db.Exec(`DELETE FROM diva_beads; INSERT INTO diva_beads(type) VALUES(1),(3),(4),(8)`); err != nil {
		t.Fatal(err)
	}
	if ids, err := NewDivaRepository(db).GetEventBeads(first.ID); err != nil || !reflect.DeepEqual(ids, old) {
		t.Fatal("restart/global config altered snapshot", ids, err)
	}
	var wg sync.WaitGroup
	events := make(chan DivaEvent, 16)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, err := NewDivaRepository(db).EnsureDivaEvent(divaLifecycleExpiry(first, -1), -1)
			events <- e
			errs <- err
		}()
	}
	wg.Wait()
	close(events)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var next DivaEvent
	for e := range events {
		if e.ID == first.ID || (next.ID != 0 && next != e) {
			t.Fatalf("concurrent channels selected different events: %+v %+v", next, e)
		}
		next = e
	}
	row, err := loadDivaBeadRound(db, next.ID)
	var seed int64
	var previousID uint32
	if err != nil || row.Version != 1 || row.Mode != -1 {
		t.Fatal("rollover not generated", row, err)
	}
	if err = db.QueryRow(`SELECT seed,previous_event_id FROM diva_bead_rounds WHERE event_id=$1`, next.ID).Scan(&seed, &previousID); err != nil || previousID != first.ID {
		t.Fatal("previous round/seed not persisted", previousID, err)
	}
	previous, err := loadDivaBeadRound(db, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := nextDivaBeadRotation(previous.state(), seed); !reflect.DeepEqual(want, row.state()) {
		t.Fatal("stored draw differs from persisted seed/history", want, row.state())
	}
	if ids, err := NewDivaRepository(db).GetEventBeads(next.ID); err != nil || !reflect.DeepEqual(ids, row.state().Slots) {
		t.Fatal("channel/restart changed draw", ids, err)
	}
	var count int
	if err = db.Get(&count, `SELECT count(*) FROM diva_bead_rounds`); err != nil || count != 2 {
		t.Fatal("extra draws/events", count, err)
	}
}

func TestRepoDivaBeadRotationModesAreIndependent(t *testing.T) {
	r, db := setupDivaRepo(t)
	now := divaTestTime(21, 12, 0)
	normal, err := r.EnsureDivaEvent(now, -1)
	if err != nil {
		t.Fatal(err)
	}
	before, err := loadDivaBeadRound(db, normal.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []int{1, 2, 3} {
		first, err := r.EnsureDivaEvent(now, mode)
		if err != nil {
			t.Fatal(err)
		}
		expiry := divaLifecycleExpiry(first, mode)
		if mode == 1 {
			expiry = time.Unix(int64(first.StartTime)+divaPhaseDuration, 0)
		}
		next, err := r.EnsureDivaEvent(expiry, mode)
		if err != nil {
			t.Fatal(err)
		}
		var modeInDB int
		var parent uint32
		if err = db.QueryRow(`SELECT mode,previous_event_id FROM diva_bead_rounds WHERE event_id=$1`, next.ID).Scan(&modeInDB, &parent); err != nil || modeInDB != mode || parent != first.ID {
			t.Fatal("debug inherited another mode", mode, modeInDB, parent, err)
		}
	}
	after, err := loadDivaBeadRound(db, normal.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("debug changed normal rotation history", before, after, err)
	}
}

func TestRepoDivaBeadRotationLegacyLazyFreeze(t *testing.T) {
	r, db := setupDivaRepo(t)
	var eventID uint32
	if err := db.QueryRow(`INSERT INTO events(event_type,start_time) VALUES('diva',now()) RETURNING id`).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO diva_beads(type) VALUES(19),(14),(1),(25),(17)`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids, err := r.GetEventBeads(eventID)
			if err == nil && !reflect.DeepEqual(ids, []int{19, 14, 1, 25}) {
				t.Errorf("legacy slots changed: %v", ids)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.GetEventBeads(eventID + 1000); err == nil {
		t.Fatal("nonexistent event frozen")
	}
}

func TestRepoDivaBeadRotationBattleSongUsesSnapshot(t *testing.T) {
	r, db := setupDivaRepo(t)
	user := CreateTestUser(t, db, "rotated_song")
	char := CreateTestCharacter(t, db, user, "RotatedSong")
	now := divaTestTime(21, 12, 0)
	if _, err := db.Exec(`INSERT INTO diva_beads(type) VALUES(19),(14),(17),(25)`); err != nil {
		t.Fatal(err)
	}
	event, err := r.EnsureDivaEvent(now, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM diva_beads; INSERT INTO diva_beads(type) VALUES(1),(3),(4),(8)`); err != nil {
		t.Fatal(err)
	}
	prayer := time.Unix(int64(event.StartTime)+60, 0)
	if _, err = db.Exec(`INSERT INTO diva_song_records(char_id,event_id,day_start,bead_index,quest_points,bonus_points,submitted_at)
		VALUES($1,$2,$3,1,60,0,$4)`, char, event.ID, divaNoon(prayer), prayer); err != nil {
		t.Fatal(err)
	}
	if err = r.AddPoints(char, event.ID, 500000, 0); err != nil {
		t.Fatal(err)
	}
	state, err := r.useDivaBattleSongAt(char, event.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	var actual []int64
	for _, effect := range state.Effects {
		actual = append(actual, int64(effect.ID))
	}
	if !reflect.DeepEqual(pq.Int64Array(actual), pq.Int64Array{14, 17, 19, 25}) {
		t.Fatal("activation used global configuration instead of round snapshot", actual)
	}
}

func TestRepoDivaBeadRotationWireUsesSnapshot(t *testing.T) {
	r, db := setupDivaRepo(t)
	user := CreateTestUser(t, db, "rotated_wire")
	char := CreateTestCharacter(t, db, user, "RotatedWire")
	if _, err := db.Exec(`INSERT INTO diva_beads(type) VALUES(25),(19),(14),(17)`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EnsureDivaEvent(TimeAdjusted(), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM diva_beads; INSERT INTO diva_beads(type) VALUES(1),(3),(4),(8)`); err != nil {
		t.Fatal(err)
	}
	srv := createMockServer()
	srv.divaRepo = r
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.erupeConfig.DebugOptions.DivaOverride = 2
	s := createMockSession(char, srv)
	handleMsgMhfGetKijuInfo(s, &mhfpacket.MsgMhfGetKijuInfo{AckHandle: 1})
	p := readAck(t, s).Payload
	if len(p) != 2185 || p[0] != 4 {
		t.Fatalf("Kiju snapshot response invalid: %x", p)
	}
	for slot, id := range []int{25, 19, 14, 17} {
		if p[1+slot*546+544] != byte(slot+1) || p[1+slot*546+545] != byte(id) {
			t.Fatal("Kiju used global effect or changed color", slot)
		}
	}
	getDivaBattleSong(s, 2)
	p = readAck(t, s).Payload
	if len(p) != 22 {
		t.Fatalf("song snapshot response invalid: %x", p)
	}
	for slot, id := range []int{25, 19, 14, 17} {
		if binary.BigEndian.Uint16(p[10+3*slot:]) != uint16(id) {
			t.Fatal("song preview used global effects", slot)
		}
	}
}

func TestRepoDivaBeadRotationStorageFailureRollsBackEvent(t *testing.T) {
	r, db := setupDivaRepo(t)
	first, err := r.EnsureDivaEvent(divaTestTime(21, 12, 0), -1)
	if err != nil {
		t.Fatal(err)
	}
	// Inject a failed snapshot INSERT without changing production error paths.
	if _, err = db.Exec(`ALTER TABLE diva_bead_rounds ADD CONSTRAINT test_reject_draw CHECK(rotation_version=0)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`ALTER TABLE diva_bead_rounds DROP CONSTRAINT test_reject_draw`); err != nil {
			t.Error(err)
		}
	})
	if _, err = r.EnsureDivaEvent(divaLifecycleExpiry(first, -1), -1); err == nil {
		t.Fatal("snapshot storage failure ignored")
	}
	var active uint32
	var events, snapshots int
	if err = db.Get(&active, `SELECT event_id FROM diva_event_lifecycle WHERE mode=-1`); err != nil || active != first.ID {
		t.Fatal("failed selection published a new active event", active, err)
	}
	if err = db.Get(&events, `SELECT count(*) FROM events WHERE event_type='diva'`); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&snapshots, `SELECT count(*) FROM diva_bead_rounds`); err != nil || events != 1 || snapshots != 1 {
		t.Fatal("failed transaction leaked event/snapshot", events, snapshots, err)
	}
}
