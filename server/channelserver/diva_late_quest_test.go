package channelserver

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"erupe-ce/network/mhfpacket"
	"fmt"
	"github.com/jmoiron/sqlx"
	"sync/atomic"
	"testing"
	"time"
)

var lateDivaInterceptionQuestIDs = map[uint16]bool{58129: true, 58130: true, 58131: true, 58132: true, 58139: true}

func originalDivaMainQuestBaseline(id uint16) bool {
	return id == 58043 || (id >= 58050 && id <= 58072) || (id >= 58074 && id <= 58078) || (id >= 58088 && id <= 58091) || (id >= 58096 && id <= 58099) || (id >= 58101 && id <= 58109) || (id >= 58112 && id <= 58115) || (id >= 58118 && id <= 58123) || (id >= 58125 && id <= 58128)
}
func TestDivaLateInterceptionExactFiveIDDelta(t *testing.T) {
	input := map[uint16]int64{}
	for n := 0; n < 65536; n++ {
		input[uint16(n)] = 1
	}
	got := map[uint16]bool{}
	for _, id := range divaTacticsQuestIDs(input) {
		if got[id] {
			t.Fatal("duplicate ID", id)
		}
		got[id] = true
	}
	for n := 0; n < 65536; n++ {
		id := uint16(n)
		oldPoints := id >= 58043 && id <= 58128
		want := oldPoints || lateDivaInterceptionQuestIDs[id]
		if got[id] != want {
			t.Errorf("point roster id%d accepted%v want%v", id, got[id], want)
		}
		if divaCustomMainQuest(id) != (originalDivaMainQuestBaseline(id) || lateDivaInterceptionQuestIDs[id]) {
			t.Errorf("main route id%d delta differs", id)
		}
	}
	maps := []DivaInterceptionMap{mustCustomDivaMap(t), mustRandomDivaMap(t, 516), mustProgressiveDivaPage(t, 516, 1)}
	for i, m := range maps {
		for id := range lateDivaInterceptionQuestIDs {
			route, ok := divaCustomMapQuestRoute(m, id)
			if !ok || route != 0 {
				t.Errorf("map%d id%d missing main route", i, id)
			}
		}
		for id := uint16(58133); id <= 58138; id++ {
			if _, ok := divaCustomMapQuestRoute(m, id); ok {
				t.Errorf("unreviewed gap%d accepted", id)
			}
		}
	}
}
func TestDivaLateInterceptionDeparturePointsAndRoundBinding(t *testing.T) {
	for id := range lateDivaInterceptionQuestIDs {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			s, repo, event := newDivaInterceptionHandlerTestSession()
			enterDivaInterceptionTestQuest(t, s, "sl2Qs200p0a1u0", id)
			run := s.divaInterceptionRunSnapshot()
			if run.QuestID != id || run.EventID != event.ID || run.GuildID != 99 || !validDivaInterceptionRunKey(run.Key) {
				t.Fatalf("late quest departure not bound: %+v", run)
			}
			s.server.stages.Store("sl2Ns200p0a1u0", NewStage("sl2Ns200p0a1u0"))
			if !doStageTransfer(s, 101, "sl2Ns200p0a1u0") {
				t.Fatal("return to town")
			}
			drainDivaInterceptionStagePackets(s)
			if s.divaInterceptionRunSnapshot() != run {
				t.Fatal("town erased binding")
			}
			handleDivaTacticsAdd(s, &mhfpacket.MsgMhfAddUdTacticsPoint{AckHandle: 516, QuestID: id, TacticsPoints: 25})
			ack := readAck(t, s)
			if ack.ErrorCode != 0 || len(repo.newAdds) != 1 {
				t.Fatal("point report not delivered", ack, len(repo.newAdds))
			}
			a := repo.newAdds[0]
			if a.questID != id || a.eventID != event.ID || a.guildID != 99 || a.points != 25 || a.key != run.Key {
				t.Fatal("original point or run attribution changed", a)
			}
			_, ids := readDivaTacticsQuestPayload(t, ack.Payload, false)
			found := false
			for _, q := range ids {
				if q == id {
					found = true
				}
			}
			if !found {
				t.Fatal("ACK omitted completed late quest")
			}
			handleDivaTacticsPoint(s, &mhfpacket.MsgMhfGetUdTacticsPoint{AckHandle: 517})
			ack = readAck(t, s)
			total, ids := readDivaTacticsQuestPayload(t, ack.Payload, true)
			found = false
			for _, q := range ids {
				if q == id {
					found = true
				}
			}
			if total != 125 || !found || repo.legacyAdds != 0 {
				t.Fatal("point read lost late quest or wrote legacy totals", total, ids)
			}
		})
	}
}

var divaLateInterceptionDriverSequence atomic.Int64

var errDivaLateInterceptionOffline = errors.New("fixture transaction sentinel; no SQL connection")

type divaLateInterceptionDriver struct{ begins *atomic.Int64 }

func (d divaLateInterceptionDriver) Open(string) (driver.Conn, error) {
	return divaLateInterceptionConn{d.begins}, nil
}

type divaLateInterceptionConn struct{ begins *atomic.Int64 }

func (c divaLateInterceptionConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("SQL execution forbidden in fixture")
}
func (c divaLateInterceptionConn) Close() error { return nil }
func (c divaLateInterceptionConn) Begin() (driver.Tx, error) {
	c.begins.Add(1)
	return nil, errDivaLateInterceptionOffline
}
func TestDivaLateInterceptionRepositoryGuardOnly(t *testing.T) {
	var begins atomic.Int64
	driverName := fmt.Sprintf("diva_late_interception_offline_%d", divaLateInterceptionDriverSequence.Add(1))
	sql.Register(driverName, divaLateInterceptionDriver{&begins})
	raw, e := sql.Open(driverName, "")
	if e != nil {
		t.Fatal(e)
	}
	db := sqlx.NewDb(raw, driverName)
	defer db.Close()
	repo := &DivaRepository{db: db}
	now := time.Now()
	started := now.Add(-time.Minute)
	for _, id := range []uint16{58043, 58128, 58129, 58130, 58131, 58132, 58139} {
		before := begins.Load()
		e := repo.AddDivaInterceptionPoints(1, 42, id, 25, 99, divaRunKeyOne, started, now)
		if !errors.Is(e, errDivaLateInterceptionOffline) || begins.Load() != before+1 {
			t.Errorf("reviewed id%d rejected before transaction guard: %v", id, e)
		}
	}
	for _, id := range []uint16{0, 58042, 58133, 58134, 58135, 58136, 58137, 58138, 58140, 65535} {
		before := begins.Load()
		e := repo.AddDivaInterceptionPoints(1, 42, id, 25, 99, divaRunKeyOne, started, now)
		if !errors.Is(e, ErrDivaInterceptionInvalid) || begins.Load() != before {
			t.Errorf("unreviewed id%d reached transaction: %v", id, e)
		}
	}
}
