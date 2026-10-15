package channelserver

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

const guildLeaderInactivePeriod = 14 * 24 * time.Hour

type guildLeadershipMember struct {
	ID       uint32 `db:"character_id"`
	Order    int    `db:"order_index"`
	Avoid    bool   `db:"avoid_leadership"`
	Eligible bool   `db:"eligible"`
}

type GuildLeadershipChange struct {
	GuildID        uint32
	FormerLeaderID uint32
	NewLeaderID    uint32
}

func guildLeadershipWeekStart(now time.Time) time.Time {
	local := now.In(time.FixedZone("UTC+9", 9*60*60))
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	days := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -days)
}

func loadGuildLeadershipMembers(ctx context.Context, tx *sqlx.Tx, guildID uint32) ([]guildLeadershipMember, error) {
	var members []guildLeadershipMember
	err := tx.SelectContext(ctx, &members, `SELECT gc.character_id,gc.order_index,gc.avoid_leadership,
		(NOT c.deleted AND NOT EXISTS(SELECT 1 FROM bans b WHERE b.user_id=c.user_id AND b.expires IS NULL)) AS eligible
		FROM guild_characters gc JOIN characters c ON c.id=gc.character_id
		WHERE gc.guild_id=$1 ORDER BY gc.order_index,gc.character_id FOR UPDATE OF gc`, guildID)
	return members, err
}

// Retail auto-promotion follows list order, even for an inactive deputy.
// Deputy slots 2/3 override the refusal setting for automatic promotion only.
// Explicit resignation retains the existing policy of respecting every refusal.
func guildLeaderSuccessor(members []guildLeadershipMember, former uint32, automatic bool) int {
	for i, m := range members {
		if m.ID != former && m.Eligible && (!m.Avoid || (automatic && m.Order >= 2 && m.Order <= 3)) {
			return i
		}
	}
	return -1
}

// RotateInactiveGuildLeaders processes at most one promotion per ordinary guild
// for the latest Monday boundary. It never recursively evaluates the successor
// or replays historical weeks after downtime. The cursor and every promotion
// commit together; another channel/process cannot advance the same week twice.
// completed=false means another guild transition owns the lock; retry later.
func (r *GuildRepository) RotateInactiveGuildLeaders(ctx context.Context, now time.Time) (completed bool, changes []GuildLeadershipChange, err error) {
	boundary := guildLeadershipWeekStart(now)
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, nil, err
	}
	defer tx.Rollback()
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1,$2)`, returnGuildLockNamespace, 0).Scan(&locked); err != nil || !locked {
		return false, nil, err
	}
	var checked time.Time
	err = tx.QueryRowContext(ctx, `SELECT checked_week FROM guild_leadership_maintenance WHERE singleton FOR UPDATE`).Scan(&checked)
	if errors.Is(err, sql.ErrNoRows) {
		// Also safe if a fresh/empty database has not established a cursor yet.
		if _, err = tx.ExecContext(ctx, `INSERT INTO guild_leadership_maintenance(singleton,checked_week) VALUES(true,$1)`, boundary); err != nil {
			return false, nil, err
		}
		return true, nil, tx.Commit()
	}
	if err != nil {
		return false, nil, err
	}
	if !checked.Before(boundary) {
		return true, nil, tx.Commit()
	}
	var candidates []struct {
		ID       uint32 `db:"id"`
		LeaderID uint32 `db:"leader_id"`
	}
	cutoff := boundary.Add(-guildLeaderInactivePeriod).Unix()
	if err = tx.SelectContext(ctx, &candidates, `SELECT g.id,g.leader_id FROM guilds g JOIN characters c ON c.id=g.leader_id
		WHERE g.return_type=0 AND g.leader_changed_at<$1 AND c.last_login>0 AND c.last_login<=$2
		ORDER BY g.id`, boundary, cutoff); err != nil {
		return false, nil, err
	}
	for _, candidate := range candidates {
		// Character first, then guild, then membership rows, as in return-guild
		// moves. A concurrent login updates this same character row; recheck the
		// current login and recent DB session binding, including other processes.
		// Legacy stale bindings alone must not block rotation forever. Live
		// connection leases protect sessions lasting longer than fourteen days.
		var inactive bool
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(c.last_login>0 AND c.last_login<=$2,false) AND NOT c.deleted
			AND NOT EXISTS(SELECT 1 FROM bans b WHERE b.user_id=c.user_id AND b.expires IS NULL)
			AND NOT EXISTS(SELECT 1 FROM sign_sessions ss WHERE ss.char_id=c.id AND ss.server_id IS NOT NULL AND ss.last_used_at>to_timestamp($2))
			AND NOT EXISTS(SELECT 1 FROM dashboard_coin_sessions live WHERE live.character_id=c.id AND NOT live.save_failed)
			FROM characters c WHERE c.id=$1 FOR UPDATE OF c`, candidate.LeaderID, cutoff).Scan(&inactive)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !inactive) {
			continue
		}
		if err != nil {
			return false, nil, err
		}
		var guildID uint32
		err = tx.QueryRowContext(ctx, `SELECT id FROM guilds WHERE id=$1 AND leader_id=$2
			AND return_type=0 AND leader_changed_at<$3 FOR UPDATE`, candidate.ID, candidate.LeaderID, boundary).Scan(&guildID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, nil, err
		}
		members, memberErr := loadGuildLeadershipMembers(ctx, tx, guildID)
		if memberErr != nil {
			return false, nil, memberErr
		}
		formerIndex := -1
		for i, member := range members {
			if member.ID == candidate.LeaderID {
				formerIndex = i
				break
			}
		}
		next := guildLeaderSuccessor(members, candidate.LeaderID, true)
		if formerIndex < 0 || next < 0 {
			continue // Invalid/solo guilds are not disbanded by this feature.
		}
		newLeader := members[next].ID
		ordered := []uint32{newLeader}
		for _, member := range members {
			if member.ID != candidate.LeaderID && member.ID != newLeader {
				ordered = append(ordered, member.ID)
			}
		}
		ordered = append(ordered, candidate.LeaderID)
		for i, id := range ordered {
			if _, err = tx.ExecContext(ctx, `UPDATE guild_characters SET order_index=$3 WHERE guild_id=$1 AND character_id=$2`, guildID, id, i+1); err != nil {
				return false, nil, err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE guilds SET leader_id=$2 WHERE id=$1`, guildID, newLeader); err != nil {
			return false, nil, err
		}
		changes = append(changes, GuildLeadershipChange{guildID, candidate.LeaderID, newLeader})
	}
	if _, err = tx.ExecContext(ctx, `UPDATE guild_leadership_maintenance SET checked_week=$1 WHERE singleton`, boundary); err != nil {
		return false, nil, err
	}
	if err = tx.Commit(); err != nil {
		return false, nil, err
	}
	return true, changes, nil
}

// ResignLeadership changes leader and both order indices atomically. Metadata
// Save must not write leader_id, or stale UI snapshots could undo maintenance.
func (r *GuildRepository) ResignLeadership(guildID, actorCharID uint32) (uint32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1,$2)`, returnGuildLockNamespace, 0); err != nil {
		return 0, err
	}
	var locked uint32
	if err = tx.QueryRowContext(ctx, `SELECT id FROM characters WHERE id=$1 FOR UPDATE`, actorCharID).Scan(&locked); err != nil {
		return 0, err
	}
	var leaderID uint32
	if err = tx.QueryRowContext(ctx, `SELECT leader_id FROM guilds WHERE id=$1 AND return_type=0 FOR UPDATE`, guildID).Scan(&leaderID); err != nil {
		return 0, err
	}
	if leaderID != actorCharID {
		return 0, ErrUnauthorized
	}
	members, err := loadGuildLeadershipMembers(ctx, tx, guildID)
	if err != nil {
		return 0, err
	}
	formerIndex := -1
	for i, member := range members {
		if member.ID == actorCharID {
			formerIndex = i
		}
	}
	if formerIndex < 0 {
		return 0, ErrGuildMemberMissing
	}
	next := guildLeaderSuccessor(members, actorCharID, false)
	if next < 0 {
		return 0, nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE guild_characters SET order_index=$3 WHERE guild_id=$1 AND character_id=$2`, guildID, actorCharID, members[next].Order); err != nil {
		return 0, err
	}
	newLeader := members[next].ID
	if _, err = tx.ExecContext(ctx, `UPDATE guild_characters SET order_index=1 WHERE guild_id=$1 AND character_id=$2`, guildID, newLeader); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE guilds SET leader_id=$2 WHERE id=$1`, guildID, newLeader); err != nil {
		return 0, err
	}
	return newLeader, tx.Commit()
}

// Only change the requested flag, never a stale member-list order snapshot.
func (r *GuildRepository) SetAvoidLeadership(guildID, charID uint32, avoid bool) error {
	result, err := r.db.Exec(`UPDATE guild_characters SET avoid_leadership=$3 WHERE guild_id=$1 AND character_id=$2`, guildID, charID, avoid)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrGuildMemberMissing
	}
	return nil
}
