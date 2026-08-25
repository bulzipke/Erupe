package channelserver

import (
	"database/sql"
	"errors"
)

var errGuildScopedMutationRejected = errors.New("guild-scoped mutation rejected")

func requireGuildScopedMutation(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errGuildScopedMutationRejected
	}
	return nil
}
