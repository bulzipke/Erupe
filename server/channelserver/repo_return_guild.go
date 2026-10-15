package channelserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

const returnGuildLockNamespace = 0x5254474c // RTGL: rare automatic-guild transitions only.
const returnGuildMemberLimit = 60           // Preserve the existing Erupe allocation policy.

var errReturnGuildChanged = errors.New("guild membership or capacity changed; retry")

type returnGuildRow struct {
	ID         uint32 `db:"id"`
	LeaderID   uint32 `db:"leader_id"`
	ReturnType uint8  `db:"return_type"`
}

// JoinReturnGuild is the operation confirmed by the client's return-clan
// warning: resign if needed, leave/disband the ordinary clan, then join. None
// of the old clan's state is committed unless the destination insert succeeds.
func (r *GuildRepository) JoinReturnGuild(charID uint32, returnType uint8, nameTemplate string) (uint32, error) {
	if returnType != 1 && returnType != 2 {
		return 0, fmt.Errorf("invalid temporary guild type %d", returnType)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		var id uint32
		id, err = r.joinReturnGuildAttempt(ctx, charID, returnType, nameTemplate)
		if err == nil {
			return id, nil
		}
		var pgErr *pq.Error
		if !errors.Is(err, errReturnGuildChanged) && !(errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001")) {
			return 0, err
		}
	}
	return 0, err
}

func (r *GuildRepository) joinReturnGuildAttempt(ctx context.Context, charID uint32, returnType uint8, nameTemplate string) (uint32, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Serialize allocation/moves, including cross-type moves. Ordinary guild
	// operations remain unaffected. All member/application changes use the same
	// character lock as AcceptApplication; guilds precede membership-row locks.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1,$2)`, returnGuildLockNamespace, 0); err != nil {
		return 0, err
	}
	var locked uint32
	if err = tx.QueryRowContext(ctx, `SELECT id FROM characters WHERE id=$1 FOR UPDATE`, charID).Scan(&locked); err != nil {
		return 0, err
	}
	var oldID uint32
	if err = tx.QueryRowContext(ctx, `SELECT guild_id FROM guild_characters WHERE character_id=$1`, charID).Scan(&oldID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if oldID != 0 {
		var oldType uint8
		if err = tx.QueryRowContext(ctx, `SELECT return_type FROM guilds WHERE id=$1`, oldID).Scan(&oldType); errors.Is(err, sql.ErrNoRows) {
			return 0, errReturnGuildChanged
		} else if err != nil {
			return 0, err
		}
		if oldType == returnType {
			// No new guild or rewritten join date/order on duplicate packets.
			if err = tx.QueryRowContext(ctx, `SELECT id FROM guilds WHERE id=$1 FOR UPDATE`, oldID).Scan(&locked); err != nil {
				return 0, err
			}
			var currentID uint32
			if err = tx.QueryRowContext(ctx, `SELECT guild_id FROM guild_characters WHERE character_id=$1 FOR UPDATE`, charID).Scan(&currentID); errors.Is(err, sql.ErrNoRows) {
				return 0, errReturnGuildChanged
			} else if err != nil {
				return 0, err
			}
			if currentID != oldID {
				return 0, errReturnGuildChanged
			}
			return oldID, tx.Commit()
		}
	}
	newID, err := findOrCreateReturnGuildTx(ctx, tx, returnType, nameTemplate)
	if err != nil {
		return 0, err
	}
	// Fixed guild-ID order also protects alliance/mission mutations from a
	// reverse old/destination lock order. No membership rows are locked yet.
	var guilds []returnGuildRow
	if err = tx.SelectContext(ctx, &guilds, `SELECT id,leader_id,return_type FROM guilds WHERE id IN($1,$2) ORDER BY id FOR UPDATE`, oldID, newID); err != nil {
		return 0, err
	}
	var old *returnGuildRow
	foundNew := false
	for i := range guilds {
		if guilds[i].ID == oldID {
			old = &guilds[i]
		}
		if guilds[i].ID == newID {
			foundNew = true
		}
	}
	if !foundNew || (oldID != 0 && old == nil) {
		return 0, errReturnGuildChanged
	}
	var currentID uint32
	err = tx.QueryRowContext(ctx, `SELECT guild_id FROM guild_characters WHERE character_id=$1 FOR UPDATE`, charID).Scan(&currentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if currentID != oldID {
		return 0, errReturnGuildChanged
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM guild_characters WHERE guild_id=$1`, newID).Scan(&count); err != nil {
		return 0, err
	}
	if count >= returnGuildMemberLimit {
		return 0, errReturnGuildChanged
	}
	if old != nil {
		if err = leaveForReturnGuildTx(ctx, tx, charID, *old); err != nil {
			return 0, err
		}
	}
	// Pending applications must not shadow the committed temporary membership.
	if _, err = tx.ExecContext(ctx, `DELETE FROM guild_applications WHERE character_id=$1`, charID); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO guild_characters(guild_id,character_id,order_index)
		VALUES($1,$2,(SELECT COALESCE(MAX(order_index),0)+1 FROM guild_characters WHERE guild_id=$1))`, newID, charID); err != nil {
		return 0, err
	}
	return newID, tx.Commit()
}

func findOrCreateReturnGuildTx(ctx context.Context, tx *sqlx.Tx, returnType uint8, nameTemplate string) (uint32, error) {
	var id uint32
	err := tx.QueryRowContext(ctx, `SELECT g.id FROM guilds g WHERE g.return_type=$1
		AND (SELECT COUNT(*) FROM guild_characters gc WHERE gc.guild_id=g.id)<$2 ORDER BY g.id LIMIT 1`, returnType, returnGuildMemberLimit).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM guilds WHERE return_type=$1`, returnType).Scan(&count); err != nil {
		return 0, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO guilds(name,leader_id,return_type,rank_rp) VALUES($1,0,$2,1200) RETURNING id`, fmt.Sprintf(nameTemplate, count+1), returnType).Scan(&id)
	return id, err
}

func leaveForReturnGuildTx(ctx context.Context, tx *sqlx.Tx, charID uint32, old returnGuildRow) error {
	var members []struct {
		ID    uint32 `db:"character_id"`
		Avoid bool   `db:"avoid_leadership"`
	}
	if err := tx.SelectContext(ctx, &members, `SELECT character_id,avoid_leadership FROM guild_characters
		WHERE guild_id=$1 ORDER BY order_index,character_id FOR UPDATE`, old.ID); err != nil {
		return err
	}
	remaining := 0
	var successor uint32
	for _, member := range members {
		if member.ID != charID {
			remaining++
			if successor == 0 && !member.Avoid {
				successor = member.ID
			}
		}
	}
	if old.ReturnType == 0 && remaining > 0 && old.LeaderID == charID {
		if successor == 0 {
			return ErrNoEligibleLeader
		}
		if _, err := tx.ExecContext(ctx, `UPDATE guilds SET leader_id=$2 WHERE id=$1`, old.ID, successor); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE guild_characters SET order_index=1 WHERE guild_id=$1 AND character_id=$2`, old.ID, successor); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM guild_characters WHERE guild_id=$1 AND character_id=$2`, old.ID, charID); err != nil {
		return err
	}
	if old.ReturnType == 0 && remaining == 0 {
		return disbandGuildTx(ctx, tx, old.ID)
	}
	return nil // Empty automatic guilds remain available for the next allocation.
}

// Caller holds the guild lock. Use this both for ordinary disband and the
// last member's confirmed move; applications/invites otherwise RESTRICT delete.
func disbandGuildTx(ctx context.Context, tx *sqlx.Tx, guildID uint32) error {
	for _, stmt := range []string{
		`DELETE FROM guild_applications WHERE guild_id=$1`,
		`DELETE FROM guild_invites WHERE guild_id=$1`,
		`DELETE FROM guild_characters WHERE guild_id=$1`,
		`DELETE FROM guilds WHERE id=$1`,
		`DELETE FROM guild_alliances WHERE parent_id=$1`,
		`UPDATE guild_alliances SET sub1_id=sub2_id,sub2_id=NULL WHERE sub1_id=$1`,
		`UPDATE guild_alliances SET sub2_id=NULL WHERE sub2_id=$1`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, guildID); err != nil {
			return err
		}
	}
	return nil
}
