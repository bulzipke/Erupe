package api

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestDashboardMezFesRankingsJSONAndPage(t *testing.T) {
	r := emptyDashboardRankings()
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"mezFesGames":[]`) {
		t.Fatal(string(b))
	}
	html, err := os.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(html)
	for stage := 463; stage <= 469; stage++ {
		if !strings.Contains(page, `id="mezfes-ranking-`+strconv.Itoa(stage)+`"`) {
			t.Errorf("missing card for stage %d", stage)
		}
	}
	for _, text := range []string{">메제페스 최고 기록<", "입장권을 사용한 플레이만 집계됩니다.", `renderMezFesRankings(rankings.mezFesGames || [])`} {
		if !strings.Contains(page, text) {
			t.Error(text)
		}
	}
}

// All writes are connection-local TEMP tables (see the eligibility test).
func TestDashboardMezFesRankingsPostgres(t *testing.T) {
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
		CREATE TEMP TABLE characters(id int,name text,deleted bool,is_new_character bool);
		INSERT INTO characters VALUES (1,'a',false,false),(2,'b',false,false),(3,'deleted',true,false),(4,'c',false,false);
		CREATE TEMP TABLE mezfes_plays(id bigserial,character_id int,event_id bigint,ticket_kind smallint,stage_id smallint,
			started_at timestamptz DEFAULT now(),raw_score int,result_rank smallint,points int,scored_at timestamptz,cancelled bool DEFAULT false);
		INSERT INTO mezfes_plays(character_id,ticket_kind,stage_id,raw_score,points,scored_at,cancelled) VALUES
		(1,0,467,7705,638,now()-interval '3 hour',false),
		(1,0,467,9000,600,now()-interval '2 hour',false), -- higher score, fewer points: best
		(2,0,467,9000,900,now()-interval '1 hour',false), -- same score, later: after a
		(2,0,467,NULL,5000,now(),false),                  -- no score total: ranks below totals
		(3,0,467,99999,5000,now(),false),                 -- deleted character
		(4,0,467,99999,9999,now(),true),                  -- cancelled
		(4,0,467,99999,NULL,NULL,false),                  -- still open
		(4,1,463,NULL,120,now(),false),
		(4,0,0,777,777,now(),false);                      -- unknown stage
		UPDATE mezfes_plays SET result_rank = 3 WHERE character_id = 1 AND raw_score = 9000;`); err != nil {
		t.Fatal(err)
	}
	s := &APIServer{db: db}
	r := emptyDashboardRankings()
	if err := s.loadDashboardMezFesRankings(context.Background(), &r); err != nil {
		t.Fatal(err)
	}
	got := r.MezFesGames
	if len(got) != 3 {
		t.Fatalf("rows = %+v", got)
	}
	if got[0].StageID != 463 || got[0].Name != "c" || !got[0].Group || got[0].GameName == "" ||
		got[0].BestScore != nil || got[0].BestPoints != 120 {
		t.Fatalf("group row = %+v", got[0])
	}
	a, b := got[1], got[2]
	if a.Name != "a" || a.BestScore == nil || *a.BestScore != 9000 || a.BestPoints != 600 || a.Plays != 2 ||
		a.BestRank == nil || *a.BestRank != 3 {
		t.Fatalf("first solo row = %+v", a)
	}
	if b.Name != "b" || b.BestScore == nil || *b.BestScore != 9000 || b.Plays != 2 {
		t.Fatalf("second solo row = %+v", b)
	}
}
