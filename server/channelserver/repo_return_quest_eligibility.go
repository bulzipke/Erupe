package channelserver

import (
	"context"
	"database/sql"
	"fmt"
)

// CASE keeps infinities away from sql.NullTime without enabling lib/pq's
// process-global infinity conversion. NULL/-infinity/finite/+infinity retain
// separate tags; only finite original values are projected as timestamptz.
// No writes, locks, maximum expiry, extra period, or derived entitlement.
const readReturnQuestExpiriesSQL = `SELECT id,
 CASE WHEN return_expires IS NULL THEN 0 WHEN return_expires='-infinity'::timestamptz THEN 1 WHEN return_expires='infinity'::timestamptz THEN 3 ELSE 2 END,
 CASE WHEN isfinite(return_expires) THEN return_expires ELSE NULL END,
 CASE WHEN newcomer_expires IS NULL THEN 0 WHEN newcomer_expires='-infinity'::timestamptz THEN 1 WHEN newcomer_expires='infinity'::timestamptz THEN 3 ELSE 2 END,
 CASE WHEN isfinite(newcomer_expires) THEN newcomer_expires ELSE NULL END
 FROM users WHERE id=$1`

var _ returnQuestExpiryReader = (*UserRepository)(nil)

func (r *UserRepository) ReadReturnQuestExpiries(ctx context.Context, userID uint32) (returnQuestExpiries, error) {
	var values returnQuestExpiries
	var returnKind, newcomerKind int
	var returnFinite, newcomerFinite sql.NullTime
	err := r.db.QueryRowContext(ctx, readReturnQuestExpiriesSQL, userID).Scan(
		&values.userID, &returnKind, &returnFinite, &newcomerKind, &newcomerFinite,
	)
	if err != nil {
		// Finite values outside the driver's timestamp parser remain explicit
		// reader failures; do not grant or collapse them into infinity.
		return returnQuestExpiries{}, err
	}
	decode := func(kind int, finite sql.NullTime) (returnQuestExpiry, error) {
		if kind < int(returnQuestExpiryNull) || kind > int(returnQuestExpiryPositiveInfinity) || (kind == int(returnQuestExpiryFinite)) != finite.Valid {
			return returnQuestExpiry{}, fmt.Errorf("inconsistent Return quest expiry projection")
		}
		value := returnQuestExpiry{kind: returnQuestExpiryKind(kind)}
		if finite.Valid {
			value.at = finite.Time.UTC()
		}
		if !value.valid() {
			return returnQuestExpiry{}, fmt.Errorf("invalid Return quest expiry precision")
		}
		return value, nil
	}
	if values.returnExpiry, err = decode(returnKind, returnFinite); err != nil {
		return returnQuestExpiries{}, err
	}
	if values.newcomerExpiry, err = decode(newcomerKind, newcomerFinite); err != nil {
		return returnQuestExpiries{}, err
	}
	if values.userID != userID {
		return returnQuestExpiries{}, fmt.Errorf("Return quest expiry account mismatch")
	}
	return values, nil
}
