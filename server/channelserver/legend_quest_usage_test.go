package channelserver

import (
	"context"
	"erupe-ce/network/mhfpacket"
	"github.com/jmoiron/sqlx"
	"os"
	"strings"
	"testing"
)

func TestLegendQuestRosterDecoder(t *testing.T) {
	valid := []byte{'L', 'R', 'Q', '1', 3, 1, 3, 1}
	ids, ok := decodeLegendQuestRoster(valid)
	if !ok || len(ids) != 3 || ids[0] != ids[2] {
		t.Fatalf("distinct same-persona NPCs: %v %v", ids, ok)
	}
	for _, b := range [][]byte{nil, valid[:7], append(append([]byte{}, valid...), 0), []byte("LRQ0\x03\x01\x03\x01"), []byte("LRQ1\x04\x01\x03\x01"), []byte("LRQ1\x00\x00\x00\x00"), []byte("LRQ1\x01\x10\x00\x00"), []byte("LRQ1\x01\xff\x00\x00"), []byte("LRQ1\x01\x01\x00\x01")} {
		if _, ok := decodeLegendQuestRoster(b); ok {
			t.Errorf("accepted invalid roster %x", b)
		}
	}
}

type legendUsageSpy struct {
	calls int
	ids   []int64
	run   string
}

func (r *legendUsageSpy) RecordRoster(run string, ids []int64) error {
	r.calls++
	r.ids = append([]int64{}, ids...)
	r.run = run
	return nil
}
func TestLegendQuestRosterAdmission(t *testing.T) {
	srv := createMockServer()
	repo := &legendUsageSpy{}
	srv.legendQuestUsageRepo = repo
	s := createMockSession(1, srv)
	other := createMockSession(2, srv)
	stage := NewStage("sl2Qs200p0a1u0")
	srv.stages.Store(stage.id, stage)
	stage.clients[s] = 1
	pkt := &mhfpacket.MsgSysSetStageBinary{BinaryType0: 254, BinaryType1: 32, StageID: stage.id, RawDataPayload: []byte("LRQ1\x03\x01\x03\x01")}
	handleMsgSysSetStageBinary(s, pkt)
	if repo.calls != 0 {
		t.Fatal("missing quest setup accepted")
	}
	stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(23303, 0)
	handleMsgSysSetStageBinary(other, pkt)
	if repo.calls != 0 {
		t.Fatal("outsider accepted")
	}
	handleMsgSysSetStageBinary(s, pkt)
	if repo.calls != 1 || repo.run != stage.towerGuardianRunID || len(repo.ids) != 3 {
		t.Fatal("valid actual roster rejected")
	}
	if len(stage.rawBinaryData) != 1 {
		t.Fatal("metric retained as gameplay stage data")
	}
	stage.clients[other] = 2
	handleMsgSysSetStageBinary(s, pkt)
	if repo.calls != 1 {
		t.Fatal("more than four party members accepted")
	}
	pkt.RawDataPayload = []byte("LRQ1\x02\x03\x03\x00")
	handleMsgSysSetStageBinary(other, pkt)
	if repo.calls != 2 || len(repo.ids) != 2 || repo.ids[0] != repo.ids[1] {
		t.Fatal("two other-owner same-persona NPCs rejected")
	}
}

// This test creates only session-local temporary tables on an explicitly
// configured test connection. It never calls the shared schema reset helper.
func TestLegendQuestRosterPostgres(t *testing.T) {
	dsn := os.Getenv("LEGEND_USAGE_TEST_DSN")
	if dsn == "" {
		t.Skip("LEGEND_USAGE_TEST_DSN unset")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	migration, err := os.ReadFile("../migrations/sql/0097_legend_rasta_quest_usage.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(strings.ReplaceAll(string(migration), "CREATE TABLE ", "CREATE TEMP TABLE ")); err != nil {
		t.Fatal(err)
	}
	repo := NewLegendQuestUsageRepository(db)
	run := strings.Repeat("a", 32)
	if err = repo.RecordRoster(run, []int64{1, 3, 1}); err != nil {
		t.Fatal(err)
	}
	// Every human's report and host reassignment share the same server run ID.
	if err = repo.RecordRoster(run, []int64{1, 3, 1}); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordRoster(strings.Repeat("b", 32), []int64{3}); err != nil {
		t.Fatal(err)
	}
	var total, flora, natasha int
	if err = db.GetContext(context.Background(), &total, "SELECT sum(usage_count) FROM legend_rasta_usage_stats"); err != nil {
		t.Fatal(err)
	}
	_ = db.Get(&flora, "SELECT usage_count FROM legend_rasta_usage_stats WHERE legend_id=1")
	_ = db.Get(&natasha, "SELECT usage_count FROM legend_rasta_usage_stats WHERE legend_id=3")
	if total != 4 || flora != 2 || natasha != 2 {
		t.Fatalf("total=%d flora=%d natasha=%d", total, flora, natasha)
	}
	if err = repo.RecordRoster(strings.Repeat("c", 32), []int64{16}); err == nil {
		t.Fatal("accepted unknown persona")
	}
	// A rejected aggregate write must also roll back the receipt claim.
	_, err = db.Exec(`ALTER TABLE legend_rasta_usage_stats ADD CONSTRAINT test_reject CHECK(legend_id<>12)`)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordRoster(strings.Repeat("d", 32), []int64{12}); err == nil {
		t.Fatal("expected constraint failure")
	}
	var claimed bool
	_ = db.Get(&claimed, "SELECT EXISTS(SELECT 1 FROM legend_rasta_quest_receipts WHERE run_id=$1)", strings.Repeat("d", 32))
	if claimed {
		t.Fatal("failed write consumed receipt")
	}
}
