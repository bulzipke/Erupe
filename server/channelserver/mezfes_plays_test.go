package channelserver

import (
	"encoding/hex"
	"os"
	"testing"

	"erupe-ce/network/mhfpacket"

	"github.com/jmoiron/sqlx"
)

func mustMezFesBlob(t *testing.T, h string) []byte {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeMezFesSaveProductionSamples(t *testing.T) {
	// Real saves: dksxpsk before/after a solo game in stage 467 (+638 points).
	before, ok := decodeMezFesSave(mustMezFesBlob(t, "6ae753f00200000001000000010000"+"01f4"))
	if !ok || before.EventID != 0x6ae753f0 || before.Used != [2]uint32{1, 1} || before.Points != 500 {
		t.Fatalf("before = %+v %v", before, ok)
	}
	after, _ := decodeMezFesSave(mustMezFesBlob(t, "6ae753f0020000000100000001"+"00000472"))
	if after.Points != 1138 {
		t.Fatalf("after points = %d", after.Points)
	}
	for _, bad := range []string{"", "0000000002", "000000000300000000000000000000000000", "0000000001000000000000000000000000"} {
		if _, ok := decodeMezFesSave(mustMezFesBlob(t, bad)); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestMezFesPlayChange(t *testing.T) {
	const ev = 0x6ae753f0
	save := func(event, solo, group, points uint32) mezFesSave {
		return mezFesSave{EventID: event, Used: [2]uint32{solo, group}, Points: points}
	}
	tests := []struct {
		name   string
		prev   mezFesSave
		prevOK bool
		next   mezFesSave
		want   mezFesPlayChange
	}{
		{"solo ticket spent", save(ev, 0, 1, 500), true, save(ev, 1, 1, 500), mezFesPlayChange{Open: [2]bool{true, false}}},
		{"group ticket spent", save(ev, 0, 0, 0), true, save(ev, 0, 1, 0), mezFesPlayChange{Open: [2]bool{false, true}}},
		{"result adds points", save(ev, 1, 1, 500), true, save(ev, 1, 1, 1138), mezFesPlayChange{Scored: true, Score: 638}},
		{"plain plaza resave", save(ev, 1, 1, 500), true, save(ev, 1, 1, 500), mezFesPlayChange{}},
		{"prize spending lowers points", save(ev, 1, 1, 900), true, save(ev, 1, 1, 100), mezFesPlayChange{}},
		{"group ticket refunded", save(ev, 0, 1, 0), true, save(ev, 0, 0, 0), mezFesPlayChange{Cancel: [2]bool{false, true}}},
		{"group tickets exchanged for points", save(ev, 0, 0, 0), true, save(ev, 0, 1, 300), mezFesPlayChange{}},
		{"first play of a new daily event", save(ev, 5, 1, 900), true, save(ev+86400, 1, 0, 900), mezFesPlayChange{Open: [2]bool{true, false}}},
		{"new event without play is no refund", save(ev, 5, 1, 900), true, save(ev+86400, 0, 0, 900), mezFesPlayChange{}},
		{"no previous save, ticket spent", mezFesSave{}, false, save(ev, 1, 0, 40), mezFesPlayChange{Open: [2]bool{true, false}}},
		{"no previous save, points only", mezFesSave{}, false, save(ev, 0, 0, 40), mezFesPlayChange{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mezFesPlayChangeFor(tt.prev, tt.prevOK, tt.next); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMezFesGameStageID(t *testing.T) {
	for id, want := range map[string]uint16{
		"sl1Ns467p0a0u0": 467, "sl2Ls463p0a0u0": 463, "sl1Ns469p0a0u0": 469,
		"sl1Ns462p0a0u0": 0, "sl1Ns470p0a0u0": 0, "sl1Ns200p0a0u0": 0, "sl1Ns": 0, "": 0, "sl1Nsabcp0": 0,
	} {
		if got := mezFesGameStageID(id); got != want {
			t.Errorf("%q = %d, want %d", id, got, want)
		}
	}
	for stage := uint16(463); stage <= 469; stage++ {
		if MezFesGameNameKO(stage) == "" {
			t.Errorf("stage %d has no name", stage)
		}
	}
	if !MezFesGameIsGroup(463) || !MezFesGameIsGroup(465) || MezFesGameIsGroup(467) {
		t.Error("group stage classification")
	}
}

type mezFesPlaySpy struct {
	calls []string
	open  []uint16
	score []uint32
	raw   []uint32
	rawAt []uint16
	rank  []int16
}

func (r *mezFesPlaySpy) OpenPlay(_, _ uint32, kind uint8, stage uint16) error {
	r.calls = append(r.calls, "open"+string('0'+kind))
	r.open = append(r.open, stage)
	return nil
}
func (r *mezFesPlaySpy) ScorePlay(_, points uint32, _ uint16) error {
	r.calls = append(r.calls, "score")
	r.score = append(r.score, points)
	return nil
}
func (r *mezFesPlaySpy) CancelPlay(_ uint32, kind uint8) error {
	r.calls = append(r.calls, "cancel"+string('0'+kind))
	return nil
}
func (r *mezFesPlaySpy) RecordResult(_, score uint32, rank int16, stage uint16) error {
	r.calls = append(r.calls, "raw")
	r.raw = append(r.raw, score)
	r.rank = append(r.rank, rank)
	r.rawAt = append(r.rawAt, stage)
	return nil
}

func TestMezFesResultFromLogProductionSamples(t *testing.T) {
	// Result logs seen in production: dksxpsk real play in 467 (7705, +638)
	// and asdf practice plays whose result screens showed B, S and A.
	for _, tt := range []struct {
		entry mhfpacket.TerminalLogEntry
		char  uint32
		want  mezFesResult
	}{
		{mhfpacket.TerminalLogEntry{Type1: 147, Type2: 141, Unk0: 5, Unk1: 2, Unk2: 1, Unk3: 1044555510, Unk4: []int32{7705, 638, 0, 0}}, 1044555510, mezFesResult{467, 7705, 2}},
		{mhfpacket.TerminalLogEntry{Type1: 147, Type2: 141, Unk0: 2, Unk1: 1, Unk3: 56, Unk4: []int32{64600, 0, 0, 0}}, 56, mezFesResult{464, 64600, 1}},
		{mhfpacket.TerminalLogEntry{Type1: 147, Type2: 141, Unk0: 5, Unk1: 3, Unk3: 56, Unk4: []int32{10465, 0, 0, 0}}, 56, mezFesResult{467, 10465, 3}},
		{mhfpacket.TerminalLogEntry{Type1: 147, Type2: 141, Unk0: 7, Unk1: 2, Unk3: 56, Unk4: []int32{75000, 0, 0, 0}}, 56, mezFesResult{469, 75000, 2}},
		{mhfpacket.TerminalLogEntry{Type1: 147, Type2: 141, Unk0: 7, Unk1: 9, Unk3: 56, Unk4: []int32{1, 0, 0, 0}}, 56, mezFesResult{469, 1, -1}},
	} {
		if got, ok := mezFesResultFromLog(tt.entry, tt.char); !ok || got != tt.want {
			t.Errorf("%+v = %+v %v, want %+v", tt.entry, got, ok, tt.want)
		}
	}
	for name, e := range map[string]mhfpacket.TerminalLogEntry{
		"entry log":       {Type1: 146, Type2: 140, Unk0: 5, Unk3: 56, Unk4: []int32{0, 0, 0, 0}},
		"other char":      {Type1: 147, Type2: 141, Unk0: 5, Unk3: 57, Unk4: []int32{10, 0, 0, 0}},
		"not a minigame":  {Type1: 147, Type2: 141, Unk0: 8, Unk3: 56, Unk4: []int32{10, 0, 0, 0}},
		"negative score":  {Type1: 147, Type2: 141, Unk0: 5, Unk3: 56, Unk4: []int32{-1, 0, 0, 0}},
		"pre-G1 no Unk4":  {Type1: 147, Type2: 141, Unk0: 5, Unk3: 56},
		"other log types": {Type1: 153, Type2: 145, Unk0: 3, Unk3: 56, Unk4: []int32{0, 0, 0, 0}},
	} {
		if _, ok := mezFesResultFromLog(e, 56); ok {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestHandleTerminalLogRecordsMezFesRawScore(t *testing.T) {
	srv := createMockServer()
	spy := &mezFesPlaySpy{}
	srv.mezFesPlayRepo = spy
	s := createMockSession(56, srv)
	handleMsgSysTerminalLog(s, &mhfpacket.MsgSysTerminalLog{AckHandle: 1, Entries: []mhfpacket.TerminalLogEntry{
		{Type1: 146, Type2: 140, Unk0: 2, Unk3: 56, Unk4: []int32{0, 0, 0, 0}},
		{Type1: 147, Type2: 141, Unk0: 2, Unk1: 1, Unk3: 56, Unk4: []int32{64600, 0, 0, 0}},
	}})
	<-s.sendPackets
	if len(spy.raw) != 1 || spy.raw[0] != 64600 || spy.rawAt[0] != 464 || spy.rank[0] != 1 {
		t.Fatalf("raw %v rank %v at %v", spy.raw, spy.rank, spy.rawAt)
	}
}

func TestHandleSaveMezfesRecordsRealPlay(t *testing.T) {
	srv := createMockServer()
	chars := newMockCharacterRepo()
	srv.charRepo = chars
	spy := &mezFesPlaySpy{}
	srv.mezFesPlayRepo = spy
	s := createMockSession(100, srv)
	stage := NewStage("sl1Ns467p0a0u0")
	s.stage = stage

	save := func(h string) {
		handleMsgMhfSaveMezfesData(s, &mhfpacket.MsgMhfSaveMezfesData{AckHandle: 1, RawDataPayload: mustMezFesBlob(t, h)})
		<-s.sendPackets
	}
	if err := chars.SaveColumn(100, "mezfes", mustMezFesBlob(t, "6ae753f0020000000000000001000001f4")); err != nil {
		t.Fatal(err)
	}
	save("6ae753f0020000000100000001000001f4")      // solo ticket spent in 467
	save("6ae753f00200000001000000010000" + "0472") // result +638
	if got := len(spy.calls); got != 2 || spy.calls[0] != "open0" || spy.calls[1] != "score" {
		t.Fatalf("calls = %v", spy.calls)
	}
	if spy.open[0] != 467 || spy.score[0] != 638 {
		t.Fatalf("open stage %v score %v", spy.open, spy.score)
	}
}

func TestRecordMezFesGroupTicketFallsBackToChannelGame(t *testing.T) {
	srv := createMockServer()
	spy := &mezFesPlaySpy{}
	srv.mezFesPlayRepo = spy
	srv.worldType, srv.mezFesMinigame = 6, 4
	s := createMockSession(100, srv)
	s.stage = NewStage("sl1Ns462p0a0u0") // still in the plaza
	s.recordMezFesPlay(mustMezFesBlob(t, "6ae753f0020000000000000000000001f4"),
		mustMezFesBlob(t, "6ae753f0020000000000000001000001f4"))
	if len(spy.open) != 1 || spy.open[0] != 465 || spy.calls[0] != "open1" {
		t.Fatalf("calls %v stages %v", spy.calls, spy.open)
	}
}

// All writes are connection-local TEMP tables; safe against an operator DB.
func TestMezFesPlayRepositoryPostgres(t *testing.T) {
	dsn := os.Getenv("RANKING_TEMP_TEST_DSN")
	if dsn == "" {
		t.Skip("RANKING_TEMP_TEST_DSN unset")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err = db.Exec(`SET search_path TO pg_temp;
		CREATE TEMP TABLE mezfes_plays(id bigserial PRIMARY KEY,character_id int NOT NULL,event_id bigint NOT NULL,
			ticket_kind smallint NOT NULL,stage_id smallint NOT NULL DEFAULT 0,started_at timestamptz NOT NULL DEFAULT now(),
			raw_score int,result_rank smallint,points int,scored_at timestamptz,cancelled bool NOT NULL DEFAULT false)`); err != nil {
		t.Fatal(err)
	}
	repo := NewMezFesPlayRepository(db)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(repo.OpenPlay(7, 0x6ae753f0, 0, 467)) // crashed, never scored
	must(repo.OpenPlay(7, 0x6ae753f0, 0, 467)) // supersedes the stale one
	must(repo.RecordResult(7, 64600, 1, 464))  // another game's result: not this play
	must(repo.RecordResult(7, 7705, 2, 467))
	must(repo.RecordResult(7, 9999, 3, 467)) // already has its score
	must(repo.ScorePlay(7, 638, 0))
	must(repo.ScorePlay(7, 50, 0)) // no open play left: ignored
	must(repo.OpenPlay(7, 0x6ae753f0, 1, 0))
	must(repo.ScorePlay(7, 120, 465))         // unknown stage filled from the result
	must(repo.RecordResult(7, 3300, -1, 465)) // result log after the save still lands
	must(repo.OpenPlay(8, 0x6ae753f0, 1, 463))
	must(repo.CancelPlay(8, 1)) // refunded group ticket
	must(repo.OpenPlay(9, 0x6ae753f0, 0, 464))
	if _, err := db.Exec(`UPDATE mezfes_plays SET started_at=now()-interval '3 hour' WHERE character_id=9`); err != nil {
		t.Fatal(err)
	}
	must(repo.ScorePlay(9, 999, 0)) // too old to attribute

	type row struct {
		Char      int    `db:"character_id"`
		Stage     int    `db:"stage_id"`
		Points    *int   `db:"points"`
		Raw       *int   `db:"raw_score"`
		Rank      *int   `db:"result_rank"`
		Cancelled bool   `db:"cancelled"`
		Kind      int    `db:"ticket_kind"`
		Event     uint32 `db:"event_id"`
	}
	var rows []row
	must(db.Select(&rows, `SELECT character_id,stage_id,raw_score,result_rank,points,cancelled,ticket_kind,event_id FROM mezfes_plays ORDER BY id`))
	if len(rows) != 5 {
		t.Fatalf("rows = %+v", rows)
	}
	if !rows[0].Cancelled || rows[0].Points != nil {
		t.Errorf("stale play = %+v", rows[0])
	}
	if rows[1].Cancelled || rows[1].Points == nil || *rows[1].Points != 638 || rows[1].Stage != 467 || rows[1].Event != 0x6ae753f0 ||
		rows[1].Raw == nil || *rows[1].Raw != 7705 || rows[1].Rank == nil || *rows[1].Rank != 2 {
		t.Errorf("scored solo = %+v", rows[1])
	}
	if rows[2].Points == nil || *rows[2].Points != 120 || rows[2].Stage != 465 || rows[2].Kind != 1 || rows[2].Raw == nil || *rows[2].Raw != 3300 || rows[2].Rank != nil {
		t.Errorf("scored group = %+v", rows[2])
	}
	if !rows[3].Cancelled || rows[3].Points != nil {
		t.Errorf("refunded = %+v", rows[3])
	}
	if rows[4].Points != nil || rows[4].Cancelled {
		t.Errorf("old result attributed = %+v", rows[4])
	}
}
