package channelserver

import (
	"context"
	"encoding/binary"
	"strconv"
	"time"

	"erupe-ce/network/mhfpacket"

	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
)

// MezFes save blob (MSG_MHF_SAVE/LOAD_MEZFES_DATA), confirmed in the ZZ client
// (non-HD FUN_107aa800 / FUN_10719010) and in production saves:
//
//	u32 eventID | u8 count(=2) | u32 usedSolo | u32 usedGroup | u32 points
//
// Remaining tickets = login allowance - used. On a new event ID the client
// zeroes both used counts (refill) and keeps points. A real play saves once
// when the ticket is spent and again when the result adds points; practice
// spends no ticket and adds no points, so it never appears as a play.
const (
	mezFesSaveLen        = 17
	mezFesTicketSolo     = 0
	mezFesTicketGroup    = 1
	mezFesFirstGameStage = 463
	mezFesLastGameStage  = 469
	// A result arriving later than this is not attributed to an old ticket.
	mezFesPlayMaxAge = 2 * time.Hour
)

type mezFesSave struct {
	EventID uint32
	Used    [2]uint32
	Points  uint32
}

func decodeMezFesSave(data []byte) (mezFesSave, bool) {
	if len(data) != mezFesSaveLen || data[4] != 2 {
		return mezFesSave{}, false
	}
	return mezFesSave{
		EventID: binary.BigEndian.Uint32(data[0:4]),
		Used:    [2]uint32{binary.BigEndian.Uint32(data[5:9]), binary.BigEndian.Uint32(data[9:13])},
		Points:  binary.BigEndian.Uint32(data[13:17]),
	}, true
}

// mezFesPlayChange is what one save means for the play log.
type mezFesPlayChange struct {
	Open   [2]bool // a ticket of that kind was spent: a real game started
	Cancel [2]bool // the client refunded that ticket (group game never started)
	Scored bool    // the game result added Score points
	Score  uint32
}

func mezFesPlayChangeFor(prev mezFesSave, prevOK bool, next mezFesSave) mezFesPlayChange {
	if !prevOK {
		prev = mezFesSave{EventID: next.EventID, Points: next.Points}
	}
	if prev.EventID != next.EventID {
		prev.Used = [2]uint32{} // the client restarts usage for every event
	}
	var change mezFesPlayChange
	spent := false
	for kind := range next.Used {
		switch {
		case next.Used[kind] > prev.Used[kind]:
			change.Open[kind], spent = true, true
		case next.Used[kind] < prev.Used[kind]:
			change.Cancel[kind] = true
		}
	}
	if next.Points > prev.Points {
		if spent {
			// Tickets and points rising together is the point exchange for
			// unused group tickets, never a game result.
			return mezFesPlayChange{}
		}
		change.Scored, change.Score = true, next.Points-prev.Points
	}
	return change
}

// mezFesGameStageID returns the minigame stage (463-469) of a stage ID such as
// "sl1Ns467p0a0u0", or 0 for the plaza and every other stage.
func mezFesGameStageID(stageID string) uint16 {
	if len(stageID) < 8 {
		return 0
	}
	n, err := strconv.Atoi(stageID[5:8])
	if err != nil || n < mezFesFirstGameStage || n > mezFesLastGameStage {
		return 0
	}
	return uint16(n)
}

// Names follow the in-game help order (hlp_06..hlp_12). 463 and 465 are the
// group games confirmed by party search; every solo stage was confirmed by
// test plays.
var mezFesGameNamesKO = map[uint16]string{
	463: "아장아장 파트냥",
	464: "울키 새총",
	465: "다같이 볼뻐끔",
	466: "구쿠 건지기",
	467: "냥련도",
	468: "패닉 허니",
	469: "펑! 통던지냥",
}

// MezFesGameNameKO returns the Korean minigame name for a stage, or "".
func MezFesGameNameKO(stageID uint16) string {
	return mezFesGameNamesKO[stageID]
}

// MezFesGameIsGroup reports whether the stage hosts a 4-player game.
func MezFesGameIsGroup(stageID uint16) bool {
	return stageID == 463 || stageID == 465
}

// MezFesPlayRepo stores the per-character play log behind the dashboard.
type MezFesPlayRepo interface {
	OpenPlay(charID, eventID uint32, ticketKind uint8, stageID uint16) error
	ScorePlay(charID, points uint32, stageID uint16) error
	CancelPlay(charID uint32, ticketKind uint8) error
	RecordResult(charID, rawScore uint32, rank int16, stageID uint16) error
}

type MezFesPlayRepository struct{ db *sqlx.DB }

func NewMezFesPlayRepository(db *sqlx.DB) *MezFesPlayRepository {
	return &MezFesPlayRepository{db: db}
}

// A new ticket supersedes any game that never reported a result (crash,
// disconnect); those are cancelled so they never count as plays.
const openMezFesPlaySQL = `
WITH stale AS (
    UPDATE mezfes_plays SET cancelled = true
    WHERE character_id = $1 AND points IS NULL AND NOT cancelled
)
INSERT INTO mezfes_plays (character_id, event_id, ticket_kind, stage_id)
VALUES ($1, $2, $3, $4)`

const scoreMezFesPlaySQL = `
UPDATE mezfes_plays
SET points = $2, scored_at = now(),
    stage_id = CASE WHEN stage_id = 0 THEN $3 ELSE stage_id END
WHERE id = (
    SELECT id FROM mezfes_plays
    WHERE character_id = $1 AND points IS NULL AND NOT cancelled
      AND started_at > now() - $4 * interval '1 second'
    ORDER BY started_at DESC, id DESC LIMIT 1
)`

const cancelMezFesPlaySQL = `
UPDATE mezfes_plays SET cancelled = true
WHERE id = (
    SELECT id FROM mezfes_plays
    WHERE character_id = $1 AND ticket_kind = $2 AND points IS NULL AND NOT cancelled
    ORDER BY started_at DESC, id DESC LIMIT 1
)`

// The result log arrives just before the result save, while the ticketed play
// is still open. A practice game has no open play, so its score is dropped;
// the stage must match so a stale ticket never takes another game's score.
const rawScoreMezFesPlaySQL = `
UPDATE mezfes_plays
SET raw_score = $2, result_rank = NULLIF($5, -1),
    stage_id = CASE WHEN stage_id = 0 THEN $3 ELSE stage_id END
WHERE id = (
    SELECT id FROM mezfes_plays
    WHERE character_id = $1 AND NOT cancelled AND raw_score IS NULL
      AND (stage_id = 0 OR stage_id = $3)
      AND ((points IS NULL AND started_at > now() - $4 * interval '1 second')
           OR scored_at > now() - interval '1 minute')
    ORDER BY started_at DESC, id DESC LIMIT 1
)`

func (r *MezFesPlayRepository) exec(query string, args ...interface{}) error {
	if r == nil || r.db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := r.db.ExecContext(ctx, query, args...)
	return err
}

func (r *MezFesPlayRepository) OpenPlay(charID, eventID uint32, ticketKind uint8, stageID uint16) error {
	return r.exec(openMezFesPlaySQL, charID, int64(eventID), int16(ticketKind), int16(stageID))
}

func (r *MezFesPlayRepository) ScorePlay(charID, points uint32, stageID uint16) error {
	return r.exec(scoreMezFesPlaySQL, charID, int64(points), int16(stageID), int64(mezFesPlayMaxAge/time.Second))
}

func (r *MezFesPlayRepository) CancelPlay(charID uint32, ticketKind uint8) error {
	return r.exec(cancelMezFesPlaySQL, charID, int16(ticketKind))
}

// RecordResult stores the score total and rank (-1 when unknown).
func (r *MezFesPlayRepository) RecordResult(charID, rawScore uint32, rank int16, stageID uint16) error {
	return r.exec(rawScoreMezFesPlaySQL, charID, int64(rawScore), int16(stageID), int64(mezFesPlayMaxAge/time.Second), rank)
}

// MSG_SYS_TERMINAL_LOG entries the client sends around a minigame (seen in
// production for real and practice plays): 146/140 on entry and 147/141 on the
// result. Unk0 is the game (stage - 462), Unk1 the result rank (0 C, 1 B,
// 2 A, 3 S; all confirmed by test plays), Unk3 the character, Unk4[0] the
// score total shown on the result screen and Unk4[1] the points earned.
const (
	mezFesResultLogType1 = 147
	mezFesResultLogType2 = 141
	mezFesStageBase      = 462
	mezFesRankS          = 3
)

type mezFesResult struct {
	Stage uint16
	Score uint32
	Rank  int16 // -1 when the client sent a value outside C..S
}

func mezFesResultFromLog(e mhfpacket.TerminalLogEntry, charID uint32) (mezFesResult, bool) {
	if e.Type1 != mezFesResultLogType1 || e.Type2 != mezFesResultLogType2 ||
		uint32(e.Unk3) != charID || len(e.Unk4) == 0 || e.Unk4[0] < 0 {
		return mezFesResult{}, false
	}
	stage := mezFesStageBase + int(e.Unk0)
	if stage < mezFesFirstGameStage || stage > mezFesLastGameStage {
		return mezFesResult{}, false
	}
	rank := int16(-1)
	if e.Unk1 >= 0 && e.Unk1 <= mezFesRankS {
		rank = int16(e.Unk1)
	}
	return mezFesResult{Stage: uint16(stage), Score: uint32(e.Unk4[0]), Rank: rank}, true
}

// recordMezFesResultLogs attaches the minigame score total to the open play.
func (s *Session) recordMezFesResultLogs(entries []mhfpacket.TerminalLogEntry) {
	repo := s.server.mezFesPlayRepo
	if repo == nil {
		return
	}
	for _, e := range entries {
		result, ok := mezFesResultFromLog(e, s.charID)
		if !ok {
			continue
		}
		if err := repo.RecordResult(s.charID, result.Score, result.Rank, result.Stage); err != nil {
			s.logger.Warn("MezFes play log: raw score failed", zap.Uint32("charID", s.charID), zap.Error(err))
		}
	}
}

// currentMezFesGameStage is the minigame stage the session is in, if any.
func (s *Session) currentMezFesGameStage() uint16 {
	s.Lock()
	stage := s.stage
	s.Unlock()
	if stage == nil {
		return 0
	}
	return mezFesGameStageID(stage.id)
}

// recordMezFesPlay logs a real play from the difference between the stored
// and the newly saved MezFes blob. Each hunter's own client spends its own
// ticket and adds its own points, so a group game yields one row per hunter.
func (s *Session) recordMezFesPlay(prevRaw, nextRaw []byte) {
	repo := s.server.mezFesPlayRepo
	if repo == nil {
		return
	}
	next, ok := decodeMezFesSave(nextRaw)
	if !ok {
		return
	}
	prev, prevOK := decodeMezFesSave(prevRaw)
	change := mezFesPlayChangeFor(prev, prevOK, next)
	stage := s.currentMezFesGameStage()
	warn := func(action string, err error) {
		if err != nil {
			s.logger.Warn("MezFes play log: "+action+" failed", zap.Uint32("charID", s.charID), zap.Error(err))
		}
	}
	if change.Scored {
		warn("score", repo.ScorePlay(s.charID, change.Score, stage))
	}
	for kind := uint8(0); kind < 2; kind++ {
		if change.Cancel[kind] {
			warn("cancel", repo.CancelPlay(s.charID, kind))
		}
	}
	for kind := uint8(0); kind < 2; kind++ {
		if !change.Open[kind] {
			continue
		}
		gameStage := stage
		if gameStage == 0 && kind == mezFesTicketGroup {
			// Group tickets may be spent before moving; the channel's
			// selected group game is the one being entered.
			switch s.effectiveMezFesMinigame() {
			case 2:
				gameStage = 463
			case 4:
				gameStage = 465
			}
		}
		warn("open", repo.OpenPlay(s.charID, next.EventID, kind, gameStage))
	}
}
