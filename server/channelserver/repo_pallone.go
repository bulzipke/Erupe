package channelserver

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"time"

	"erupe-ce/common/mhfpallone"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type PalloneRepository struct{ db *sqlx.DB }

const palloneLifecycleLock int64 = 0x50414c4c4f4e45

func NewPalloneRepository(db *sqlx.DB) *PalloneRepository { return &PalloneRepository{db: db} }

var _ PalloneRepo = (*PalloneRepository)(nil)

func (r *PalloneRepository) GetPalloneEvent(id int32) (*PalloneEvent, error) {
	var e PalloneEvent
	err := r.db.Get(&e, `SELECT id,starts_at,ends_at,claim_until FROM pallone_events WHERE id=$1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func readPalloneRoutes(tx *sqlx.Tx, id int32) ([]PalloneRoute, error) {
	rows, err := tx.Query(`SELECT route_key,star,stage_quest_ids FROM pallone_event_routes WHERE event_id=$1 ORDER BY route_key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var routes []PalloneRoute
	for rows.Next() {
		var v PalloneRoute
		var ids pq.Int32Array
		if err := rows.Scan(&v.Key, &v.Star, &ids); err != nil {
			return nil, err
		}
		for _, id := range ids {
			v.QuestIDs = append(v.QuestIDs, uint16(id))
		}
		routes = append(routes, v)
	}
	return routes, rows.Err()
}

func (r *PalloneRepository) GetPalloneRoutes(id int32) ([]PalloneRoute, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return readPalloneRoutes(tx, id)
}

// RegisterPalloneEvent is an explicit operator operation, not an automatic
// lifecycle reset. Re-registering an identical plan is idempotent. Changing
// dates/routes under an existing ID is refused, preserving all receipts.
func (r *PalloneRepository) RegisterPalloneEvent(e PalloneEvent, routes []PalloneRoute) error {
	e.Start = e.Start.UTC().Truncate(time.Microsecond)
	e.End = e.End.UTC().Truncate(time.Microsecond)
	e.ClaimUntil = e.ClaimUntil.UTC().Truncate(time.Microsecond)
	if err := validatePalloneEvent(e, routes); err != nil {
		return err
	}
	routes = append([]PalloneRoute(nil), routes...)
	sort.Slice(routes, func(i, j int) bool { return routes[i].Key < routes[j].Key })
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock($1)`, palloneLifecycleLock); err != nil {
		return err
	}
	if err = registerPalloneEvent(tx, e, routes); err != nil {
		return err
	}
	return tx.Commit()
}

// Caller holds palloneLifecycleLock. Existing rounds are immutable, including
// the upcoming round prepublished for the native login calendar.
func registerPalloneEvent(tx *sqlx.Tx, e PalloneEvent, routes []PalloneRoute) error {
	var old PalloneEvent
	err := tx.Get(&old, `SELECT id,starts_at,ends_at,claim_until FROM pallone_events WHERE id=$1`, e.ID)
	if err == nil {
		oldRoutes, err := readPalloneRoutes(tx, e.ID)
		if err != nil {
			return err
		}
		if !old.Start.Equal(e.Start) || !old.End.Equal(e.End) || !old.ClaimUntil.Equal(e.ClaimUntil) || !reflect.DeepEqual(routes, oldRoutes) {
			return ErrPalloneConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO pallone_events(id,starts_at,ends_at,claim_until) VALUES($1,$2,$3,$4)`, e.ID, e.Start, e.End, e.ClaimUntil); err != nil {
		return err
	}
	for _, v := range routes {
		ids := make([]int32, len(v.QuestIDs))
		for i, q := range v.QuestIDs {
			ids[i] = int32(q)
		}
		if _, err = tx.Exec(`INSERT INTO pallone_event_routes(event_id,route_key,star,stage_quest_ids) VALUES($1,$2,$3,$4)`, e.ID, v.Key, v.Star, pq.Array(ids)); err != nil {
			return err
		}
	}
	_, err = palloneRewardProfileTx(tx, e.ID)
	return err
}

type palloneStoredResult struct {
	PalloneRunReceipt
	PalloneSubmission
	Multiplier uint32
}

func readPalloneResult(tx *sqlx.Tx, charID uint32, runID string) (*palloneStoredResult, error) {
	var v palloneStoredResult
	err := tx.QueryRow(`SELECT event_id,character_id,route_key,run_id,started_at,completed_at,final_quest_id,
		base_score,premium_bonus,item_bonus,submitted_guild_id,multiplier
		FROM pallone_results WHERE character_id=$1 AND run_id=$2`, charID, runID).Scan(
		&v.EventID, &v.CharID, &v.RouteKey, &v.RunID, &v.StartedAt, &v.CompletedAt, &v.FinalQuestID,
		&v.BaseScore, &v.PremiumBonus, &v.ItemBonus, &v.GuildID, &v.Multiplier)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &v, err
}

func (v palloneStoredResult) matches(receipt PalloneRunReceipt, submission PalloneSubmission) bool {
	return v.EventID == receipt.EventID && v.RouteKey == receipt.RouteKey && v.FinalQuestID == receipt.FinalQuestID &&
		v.StartedAt.Equal(receipt.StartedAt) && v.CompletedAt.Equal(receipt.CompletedAt) && v.PalloneSubmission == submission
}

func palloneRepeat(tx *sqlx.Tx, old *palloneStoredResult, receipt PalloneRunReceipt, v PalloneSubmission) (PalloneSettlement, error) {
	if !old.matches(receipt, v) {
		return PalloneSettlement{}, ErrPalloneConflict
	}
	return PalloneSettlement{Multiplier: old.Multiplier, Duplicate: true}, tx.Commit()
}

// SettlePalloneResult persists exactly one server-qualified result. Personal
// and guild caps are checked under event-scoped advisory locks. Guild membership
// is locked while recording its historical attribution. An exact retry returns
// the original ACK even after a transfer or event expiry; changed data cannot
// reuse that receipt, and a run cannot be replayed into a different event.
func (r *PalloneRepository) SettlePalloneResult(receipt PalloneRunReceipt, v PalloneSubmission, now time.Time) (PalloneSettlement, error) {
	receipt, err := normalizePalloneReceipt(receipt)
	if err != nil || !validPalloneSubmission(v) || now.Before(receipt.CompletedAt) {
		return PalloneSettlement{}, ErrPalloneInvalid
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return PalloneSettlement{}, err
	}
	defer tx.Rollback()
	lockKey := receipt.EventID ^ int32(0x50414c4c)
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock($1,$2)`, lockKey, int32(receipt.CharID)); err != nil {
		return PalloneSettlement{}, err
	}
	old, err := readPalloneResult(tx, receipt.CharID, receipt.RunID)
	if err != nil {
		return PalloneSettlement{}, err
	}
	if old != nil {
		return palloneRepeat(tx, old, receipt, v)
	}
	var e PalloneEvent
	err = tx.Get(&e, `SELECT id,starts_at,ends_at,claim_until FROM pallone_events WHERE id=$1 FOR SHARE`, receipt.EventID)
	if errors.Is(err, sql.ErrNoRows) {
		return PalloneSettlement{}, ErrPalloneIneligible
	}
	if err != nil {
		return PalloneSettlement{}, err
	}
	if !e.Active(receipt.StartedAt) || !e.Active(receipt.CompletedAt) {
		return PalloneSettlement{}, ErrPalloneIneligible
	}
	if !now.Before(e.End) {
		durable, matchErr := palloneDurableReportTx(tx, receipt, v)
		if matchErr != nil {
			return PalloneSettlement{}, matchErr
		}
		if !durable {
			return PalloneSettlement{}, ErrPalloneIneligible
		}
	}
	var finalized bool
	if err = tx.Get(&finalized, `SELECT EXISTS(SELECT 1 FROM pallone_event_finalizations WHERE event_id=$1)`, e.ID); err != nil {
		return PalloneSettlement{}, err
	}
	if finalized {
		return PalloneSettlement{}, ErrPalloneIneligible
	}
	var finalID int32
	err = tx.Get(&finalID, `SELECT stage_quest_ids[cardinality(stage_quest_ids)] FROM pallone_event_routes WHERE event_id=$1 AND route_key=$2`, e.ID, receipt.RouteKey)
	if errors.Is(err, sql.ErrNoRows) || err == nil && finalID != int32(receipt.FinalQuestID) {
		return PalloneSettlement{}, ErrPalloneInvalid
	}
	if err != nil {
		return PalloneSettlement{}, err
	}
	var cid uint32
	err = tx.Get(&cid, `SELECT id FROM characters WHERE id=$1 AND NOT deleted FOR SHARE`, receipt.CharID)
	if errors.Is(err, sql.ErrNoRows) {
		return PalloneSettlement{}, ErrPalloneInvalid
	}
	if err != nil {
		return PalloneSettlement{}, err
	}
	var credited int64
	if err = tx.Get(&credited, `SELECT COALESCE(SUM(total_score::bigint+premium_bonus),0) FROM pallone_results WHERE event_id=$1 AND character_id=$2`, e.ID, receipt.CharID); err != nil {
		return PalloneSettlement{}, err
	}
	total := int64(v.BaseScore) + int64(v.ItemBonus)
	if credited+total+int64(v.PremiumBonus) > math.MaxInt32 {
		return PalloneSettlement{}, ErrPalloneScoreLimit
	}
	var guildID interface{}
	if v.GuildID != 0 {
		// A separate sign-bit namespace keeps all uint32 guild IDs distinct
		// from positive int32 character IDs, without narrowing guild IDs.
		if _, err = tx.Exec(`SELECT pg_advisory_xact_lock($1,$2)`, lockKey^int32(math.MinInt32), int32(v.GuildID)); err != nil {
			return PalloneSettlement{}, err
		}
		var memberGuild uint32
		err = tx.Get(&memberGuild, `SELECT guild_id FROM guild_characters WHERE character_id=$1 AND guild_id=$2 FOR SHARE`, receipt.CharID, v.GuildID)
		if errors.Is(err, sql.ErrNoRows) {
			accepted, matchErr := palloneDurableReportTx(tx, receipt, v)
			if matchErr != nil {
				return PalloneSettlement{}, matchErr
			}
			if !accepted {
				return PalloneSettlement{}, ErrPalloneGuild
			}
			// Membership was validated when the durable score report arrived.
			// A later transfer cannot rewrite its historical attribution.
			err = tx.Get(&memberGuild, `SELECT id FROM guilds WHERE id=$1 FOR SHARE`, v.GuildID)
			if errors.Is(err, sql.ErrNoRows) {
				err = nil
				memberGuild = 0
			}
		}
		if err != nil {
			return PalloneSettlement{}, err
		}
		if err = tx.Get(&credited, `SELECT COALESCE(SUM(total_score::bigint),0) FROM pallone_results WHERE event_id=$1 AND guild_id=$2`, e.ID, memberGuild); err != nil {
			return PalloneSettlement{}, err
		}
		if credited+total > math.MaxInt32 {
			return PalloneSettlement{}, ErrPalloneScoreLimit
		}
		if memberGuild != 0 {
			guildID = memberGuild
		}
	}
	result, err := tx.Exec(`INSERT INTO pallone_results(character_id,run_id,event_id,route_key,started_at,completed_at,final_quest_id,
		base_score,premium_bonus,item_bonus,total_score,submitted_guild_id,guild_id,multiplier,recorded_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,0,$14) ON CONFLICT(character_id,run_id) DO NOTHING`,
		receipt.CharID, receipt.RunID, e.ID, receipt.RouteKey, receipt.StartedAt, receipt.CompletedAt, receipt.FinalQuestID,
		v.BaseScore, v.PremiumBonus, v.ItemBonus, total, v.GuildID, guildID, now.UTC())
	if err != nil {
		return PalloneSettlement{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return PalloneSettlement{}, err
	}
	if n == 0 {
		old, err = readPalloneResult(tx, receipt.CharID, receipt.RunID)
		if err != nil {
			return PalloneSettlement{}, err
		}
		if old == nil {
			return PalloneSettlement{}, ErrPalloneConflict
		}
		return palloneRepeat(tx, old, receipt, v)
	}
	if guildID != nil {
		catalog, err := palloneRewardProfileTx(tx, e.ID)
		if err != nil {
			return PalloneSettlement{}, err
		}
		var ids pq.Int32Array
		if err := tx.Get(&ids, `SELECT stage_quest_ids FROM pallone_event_routes WHERE event_id=$1 AND route_key=$2`, e.ID, receipt.RouteKey); err != nil {
			return PalloneSettlement{}, err
		}
		stages := int32(0)
		for _, id := range ids {
			if id != 27348 {
				stages++
			}
		}
		if stages > 0 {
			if _, err := tx.Exec(`INSERT INTO pallone_guild_contributions(event_id,character_id,run_id,guild_id,points) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, e.ID, receipt.CharID, receipt.RunID, v.GuildID, stages*catalog.OrbPointsPerStage); err != nil {
				return PalloneSettlement{}, err
			}
		}
	}
	return PalloneSettlement{}, tx.Commit()
}

func (r *PalloneRepository) GetPalloneScores(eventID int32, charID uint32) ([]mhfpallone.Score, error) {
	var rows []mhfpallone.Score
	err := r.db.Select(&rows, `SELECT route_key,SUM(total_score)::integer AS total_score,MAX(base_score) AS best_score,
		SUM(premium_bonus)::integer AS premium_score FROM pallone_results
		WHERE event_id=$1 AND character_id=$2 GROUP BY route_key ORDER BY route_key`, eventID, charID)
	return rows, err
}

func (r *PalloneRepository) GetPallonePersonalRanking(eventID int32, key *int32, guildID uint32, limit int) ([]mhfpallone.PersonalRanking, error) {
	if limit != mhfpallone.MaxRankingRows && limit != mhfpallone.MaxGuildContributors {
		return nil, ErrPalloneInvalid
	}
	var rows []mhfpallone.PersonalRanking
	err := r.db.Select(&rows, `SELECT (CASE WHEN $2::integer IS NULL THEN SUM(p.total_score) ELSE MAX(p.base_score) END)::integer AS score,c.name
		FROM pallone_results p JOIN characters c ON c.id=p.character_id
		WHERE p.event_id=$1 AND NOT c.deleted AND ($2::integer IS NULL OR p.route_key=$2) AND ($3::bigint=0 OR p.guild_id=$3)
		GROUP BY c.id,c.name ORDER BY score DESC,c.id ASC LIMIT $4`, eventID, key, guildID, limit)
	return rows, err
}

func (r *PalloneRepository) GetPalloneGuildRanking(eventID int32, key *int32) ([]mhfpallone.GuildRanking, error) {
	var rows []mhfpallone.GuildRanking
	err := r.db.Select(&rows, `SELECT SUM(p.total_score)::integer AS score,g.id AS guild_id,g.name
		FROM pallone_results p JOIN guilds g ON g.id=p.guild_id
		WHERE p.event_id=$1 AND ($2::integer IS NULL OR p.route_key=$2)
		GROUP BY g.id,g.name ORDER BY score DESC,g.id ASC LIMIT 100`, eventID, key)
	return rows, err
}

func (r *PalloneRepository) GetPalloneRanks(eventID int32, charID, guildID uint32) ([]mhfpallone.RankScore, error) {
	var rows []mhfpallone.RankScore
	var query string
	var subject uint32
	if guildID == 0 {
		query = `WITH points AS (
			SELECT p.route_key,p.character_id AS subject,MAX(p.base_score)::bigint AS score FROM pallone_results p
			JOIN characters c ON c.id=p.character_id WHERE p.event_id=$1 AND NOT c.deleted GROUP BY p.route_key,p.character_id
			UNION ALL SELECT 0,p.character_id,SUM(p.total_score)::bigint FROM pallone_results p
			JOIN characters c ON c.id=p.character_id WHERE p.event_id=$1 AND NOT c.deleted GROUP BY p.character_id
		), ranked AS (SELECT route_key,subject,score,RANK() OVER(PARTITION BY route_key ORDER BY score DESC) AS rank FROM points)
		SELECT route_key,rank,score FROM ranked WHERE subject=$2 ORDER BY route_key`
		subject = charID
	} else {
		query = `WITH points AS (
			SELECT p.route_key,p.guild_id AS subject,SUM(p.total_score)::bigint AS score FROM pallone_results p
			JOIN guilds g ON g.id=p.guild_id WHERE p.event_id=$1 GROUP BY p.route_key,p.guild_id
			UNION ALL SELECT 0,p.guild_id,SUM(p.total_score)::bigint FROM pallone_results p
			JOIN guilds g ON g.id=p.guild_id WHERE p.event_id=$1 GROUP BY p.guild_id
		), ranked AS (SELECT route_key,subject,score,RANK() OVER(PARTITION BY route_key ORDER BY score DESC) AS rank FROM points)
		SELECT route_key,rank,score FROM ranked WHERE subject=$2 ORDER BY route_key`
		subject = guildID
	}
	err := r.db.Select(&rows, query, eventID, subject)
	if err != nil {
		return nil, fmt.Errorf("Pallone ranks: %w", err)
	}
	return rows, nil
}
