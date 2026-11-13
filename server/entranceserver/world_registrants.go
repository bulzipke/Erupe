package entranceserver

import (
	"context"
	"encoding/binary"
	"erupe-ce/common/accountprivilege"
	"erupe-ce/common/stringsupport"
	"net"
	"time"
)

const (
	worldRosterMarker = "WLR1"
	maxWorldFriends   = 50
	maxWorldGuild     = 65 // Complete entries before the next native startup field at 0xA2C.
	worldRosterBudget = 600 * time.Millisecond
)

type worldRegistrant struct {
	ID    uint32 `db:"id"`
	Name  string `db:"name"`
	Guild bool   `db:"guild"`
}

type worldRosterRepo interface {
	LoadWorldRoster(context.Context, uint32) ([]worldRegistrant, error)
}

func (s *Server) sendWorldRoster(conn net.Conn, req accountprivilege.Request, token string) {
	repo, ok := s.sessionRepo.(worldRosterRepo)
	if !ok {
		return
	}
	now := time.Now()
	release, admitted := s.worldRosterLimits.enter(profileIP(conn.RemoteAddr()), now)
	if !admitted {
		return
	}
	defer release()
	deadline := now.Add(worldRosterBudget)
	if conn.SetDeadline(deadline) != nil {
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	rows, err := repo.LoadWorldRoster(ctx, req.CharacterID)
	if err != nil {
		return
	}
	// The SQL orders friends before guildmates, with each group independently capped.
	frame := make([]byte, 28, 28+len(rows)*20+32)
	copy(frame, worldRosterMarker)
	copy(frame[4:], req.Nonce[:])
	var friends, guildmates uint32
	for _, row := range rows {
		if row.Guild {
			guildmates++
		} else {
			friends++
		}
		frame = binary.BigEndian.AppendUint32(frame, row.ID)
		frame = append(frame, stringsupport.PaddedString(row.Name, 16, true)...)
	}
	if friends > maxWorldFriends || guildmates > maxWorldGuild {
		return
	}
	binary.BigEndian.PutUint32(frame[20:24], friends)
	binary.BigEndian.PutUint32(frame[24:28], guildmates)
	writeProfileResponse(conn, accountprivilege.WorldRosterResponse(frame, token))
}

func (r *EntranceSessionRepository) LoadWorldRoster(ctx context.Context, cid uint32) ([]worldRegistrant, error) {
	var rows []worldRegistrant
	err := r.db.SelectContext(ctx, &rows, `
		WITH friends AS (
			SELECT friend.id, friend.name, MIN(entry.position) AS position
			FROM characters owner
			CROSS JOIN LATERAL unnest(string_to_array(owner.friends, ',')) WITH ORDINALITY AS entry(value, position)
			JOIN characters friend ON friend.id = CASE
				WHEN btrim(entry.value) ~ '^[0-9]{1,10}$' THEN btrim(entry.value)::bigint END
			WHERE owner.id = $1 AND NOT owner.deleted
			  AND NOT friend.deleted AND NOT friend.is_new_character AND friend.id <> owner.id
			GROUP BY friend.id, friend.name
			ORDER BY position
			LIMIT $2
		), guildmates AS (
			SELECT c.id, c.name, member.order_index AS position
			FROM guild_characters owner
			JOIN guild_characters member ON member.guild_id = owner.guild_id
			JOIN characters c ON c.id = member.character_id
			WHERE owner.character_id = $1 AND member.character_id <> $1
			  AND NOT c.deleted AND NOT c.is_new_character
			ORDER BY position, c.id
			LIMIT $3
		)
		SELECT id, name, guild
		FROM (
			SELECT id, name, false AS guild, position FROM friends
			UNION ALL
			SELECT id, name, true AS guild, position FROM guildmates
		) roster
		ORDER BY guild, position, id
	`, cid, maxWorldFriends, maxWorldGuild)
	return rows, err
}
