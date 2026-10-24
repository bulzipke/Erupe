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

// ErrQuestAccessCharacter means that the supplied active character does not
// belong to the authenticated account in this read's consistent snapshot.
var ErrQuestAccessCharacter = errors.New("quest access character is not active or not owned by account")

// QuestAccessRepository reads account ownership and all matching legacy
// campaigns together. It never grants, consumes, completes, or purchases
// anything. Call Load outside all Server/Stage/Session gameplay locks.
type QuestAccessRepository struct {
	db *sqlx.DB
}

func NewQuestAccessRepository(db *sqlx.DB) *QuestAccessRepository {
	return &QuestAccessRepository{db: db}
}

// Load uses one repeatable-read, read-only PostgreSQL transaction. Missing or
// failed identity/legacy reads return no snapshot. An account-read error may
// degrade to verified legacy rights only after savepoint recovery succeeds;
// AccountReadError then lets the caller report and retry the unavailable branch.
func (r *QuestAccessRepository) Load(ctx context.Context, request QuestAccessLoadRequest) (*QuestAccessSnapshot, error) {
	ids, mappings, keys, err := prepareQuestAccessRequest(request)
	if err != nil {
		return nil, err
	}
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("quest access repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var readAt time.Time
	var ownsCharacter bool
	err = tx.QueryRowContext(ctx, `/* quest_access_identity */
		SELECT clock_timestamp(), EXISTS (
			SELECT 1 FROM characters c JOIN users u ON u.id=c.user_id
			WHERE c.id=$1 AND c.user_id=$2 AND NOT c.deleted
		)`, request.CharacterID, request.UserID).Scan(&readAt, &ownsCharacter)
	if err != nil {
		return nil, err
	}
	if !ownsCharacter {
		return nil, ErrQuestAccessCharacter
	}
	grants := make(map[int]questAccessGrant, len(ids))
	for _, id := range ids {
		grants[id] = questAccessGrant{}
	}
	if len(ids) != 0 {
		var legacy []struct {
			QuestID   int       `db:"quest_id"`
			ExpiresAt time.Time `db:"expires_at"`
		}
		// campaign_quest is completion state, not code authentication. Preserve
		// the existing stamp minimum and end-time rule, including its lack of a
		// campaign start-time check. Every matching campaign is considered;
		// duplicate reward rows cannot multiply a campaign's stamp count.
		err = tx.SelectContext(ctx, &legacy, `/* quest_access_legacy */
			SELECT cr.item_id AS quest_id, MAX(c.end_time) AS expires_at
			FROM campaign_rewards cr JOIN campaigns c ON c.id=cr.campaign_id
			WHERE cr.item_type=9 AND cr.item_id=ANY($1) AND c.end_time>$2
			AND EXISTS (
				SELECT 1 FROM campaign_state cs
				WHERE cs.campaign_id=c.id AND cs.character_id=$3
				GROUP BY cs.campaign_id
				HAVING COUNT(*)>=GREATEST(COALESCE(c.stamps, 1), 1)
			)
			GROUP BY cr.item_id`, pq.Array(ids), readAt, request.CharacterID)
		if err != nil {
			return nil, err
		}
		for _, row := range legacy {
			grant := grants[row.QuestID]
			grant.legacyExpiry = row.ExpiresAt.UTC()
			grants[row.QuestID] = grant
		}
	}
	var accountReadError error
	if len(keys) != 0 {
		if _, err := tx.ExecContext(ctx, `SAVEPOINT quest_access_account_read`); err != nil {
			return nil, err
		}
		var owned []string
		err = tx.SelectContext(ctx, &owned, `/* quest_access_account */
			SELECT license_key FROM quest_licenses
			WHERE user_id=$1 AND license_key=ANY($2)`, request.UserID, pq.Array(keys))
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			accountReadError = fmt.Errorf("account quest access read failed: %w", err)
			if _, recoveryErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT quest_access_account_read`); recoveryErr != nil {
				return nil, errors.Join(accountReadError, recoveryErr)
			}
		} else {
			ownedKeys := make(map[string]bool, len(owned))
			for _, key := range owned {
				ownedKeys[key] = true
			}
			for _, id := range ids {
				for _, key := range mappings[id] {
					if ownedKeys[key] {
						grant := grants[id]
						grant.accountPermanent = true
						grants[id] = grant
						break
					}
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT quest_access_account_read`); err != nil {
			return nil, errors.Join(accountReadError, err)
		}
	}
	// BEGIN time can precede the actual repeatable-read snapshot, and reads can
	// wait behind another transaction. Issue authority only after all evidence
	// has been read; it cannot authorize an admission that preceded issuance.
	var observedAt time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&observedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return newQuestAccessSnapshot(request.UserID, request.CharacterID, observedAt, grants, accountReadError), nil
}
