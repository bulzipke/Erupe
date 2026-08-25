package channelserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// GuildRepository centralizes all database access for guild-related tables
// (guilds, guild_characters, guild_applications).
type GuildRepository struct {
	db *sqlx.DB
}

// NewGuildRepository creates a new GuildRepository.
func NewGuildRepository(db *sqlx.DB) *GuildRepository {
	return &GuildRepository{db: db}
}

const guildInfoSelectSQL = `
SELECT
	g.id,
	g.name,
	rank_rp,
	event_rp,
	room_rp,
	COALESCE(room_expiry, '1970-01-01') AS room_expiry,
	main_motto,
	sub_motto,
	created_at,
	leader_id,
	c.name AS leader_name,
	comment,
	return_type,
	COALESCE(pugi_name_1, '') AS pugi_name_1,
	COALESCE(pugi_name_2, '') AS pugi_name_2,
	COALESCE(pugi_name_3, '') AS pugi_name_3,
	pugi_outfit_1,
	pugi_outfit_2,
	pugi_outfit_3,
	pugi_outfits,
	recruiting,
	COALESCE((SELECT team FROM festa_registrations fr WHERE fr.guild_id = g.id), 'none') AS festival_color,
	COALESCE((SELECT SUM(fs.souls) FROM festa_submissions fs WHERE fs.guild_id=g.id), 0) AS souls,
	COALESCE((
		SELECT id FROM guild_alliances ga WHERE
	 	ga.parent_id = g.id OR
	 	ga.sub1_id = g.id OR
	 	ga.sub2_id = g.id
	), 0) AS alliance_id,
	icon,
	COALESCE(rp_reset_at, '2000-01-01'::timestamptz) AS rp_reset_at,
	(SELECT count(1) FROM guild_characters gc WHERE gc.guild_id = g.id) AS member_count
	FROM guilds g
	JOIN guild_characters gc ON gc.character_id = leader_id
	JOIN characters c on leader_id = c.id
`

const guildMembersSelectSQL = `
SELECT
	character.guild_id AS guild_id,
	gc.joined_at,
	COALESCE((SELECT SUM(souls) FROM festa_submissions fs WHERE fs.character_id=c.id), 0) AS souls,
	COALESCE(gc.rp_today, 0) AS rp_today,
	COALESCE(gc.rp_yesterday, 0) AS rp_yesterday,
	c.name,
	c.id AS character_id,
	COALESCE(gc.order_index, 0) AS order_index,
	c.last_login,
	COALESCE(gc.recruiter, false) AS recruiter,
	COALESCE(gc.avoid_leadership, false) AS avoid_leadership,
	c.hr,
	c.gr,
	c.weapon_id,
	c.weapon_type,
	CASE WHEN NOT character.is_applicant AND g.leader_id = c.id THEN true ELSE false END AS is_leader,
	character.is_applicant
	FROM (
		SELECT character_id, true as is_applicant, guild_id
		FROM guild_applications ga
		WHERE ga.application_type = 'applied'
		UNION
		SELECT character_id, false as is_applicant, guild_id
		FROM guild_characters gc
	) character
	JOIN characters c on character.character_id = c.id
	LEFT JOIN guild_characters gc
		ON NOT character.is_applicant
		AND gc.character_id = character.character_id
		AND gc.guild_id = character.guild_id
	LEFT JOIN guilds g ON g.id = character.guild_id
`

func scanGuild(rows *sqlx.Rows) (*Guild, error) {
	guild := &Guild{}
	if err := rows.StructScan(guild); err != nil {
		return nil, err
	}
	return guild, nil
}

func scanGuildMember(rows *sqlx.Rows) (*GuildMember, error) {
	member := &GuildMember{}
	if err := rows.StructScan(member); err != nil {
		return nil, err
	}
	return member, nil
}

// GetByID retrieves guild info by guild ID, returning nil if not found.
func (r *GuildRepository) GetByID(guildID uint32) (*Guild, error) {
	rows, err := r.db.Queryx(fmt.Sprintf(`%s WHERE g.id = $1 LIMIT 1`, guildInfoSelectSQL), guildID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, nil
	}
	return scanGuild(rows)
}

// GetByCharID retrieves guild info for a character, including applied guilds.
func (r *GuildRepository) GetByCharID(charID uint32) (*Guild, error) {
	rows, err := r.db.Queryx(fmt.Sprintf(`
		%s
		WHERE EXISTS(
				SELECT 1
				FROM guild_characters gc1
				WHERE gc1.character_id = $1
				  AND gc1.guild_id = g.id
			)
		   OR EXISTS(
				SELECT 1
				FROM guild_applications ga
				WHERE ga.character_id = $1
				  AND ga.guild_id = g.id
				  AND ga.application_type = 'applied'
			)
		LIMIT 1
	`, guildInfoSelectSQL), charID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, nil
	}
	return scanGuild(rows)
}

// ListAll returns all guilds. Used for guild enumeration/search.
func (r *GuildRepository) ListAll() ([]*Guild, error) {
	rows, err := r.db.Queryx(guildInfoSelectSQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var guilds []*Guild
	for rows.Next() {
		guild, err := scanGuild(rows)
		if err != nil {
			continue
		}
		guilds = append(guilds, guild)
	}
	return guilds, nil
}

// Create creates a new guild and adds the leader as its first member.
func (r *GuildRepository) Create(leaderCharID uint32, guildName string) (int32, error) {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var guildID int32
	err = tx.QueryRow(
		"INSERT INTO guilds (name, leader_id) VALUES ($1, $2) RETURNING id",
		guildName, leaderCharID,
	).Scan(&guildID)
	if err != nil {
		return 0, err
	}

	_, err = tx.Exec(`INSERT INTO guild_characters (guild_id, character_id) VALUES ($1, $2)`, guildID, leaderCharID)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return guildID, nil
}

// FindOrCreateReturnGuild finds an existing return guild of the given type with fewer
// than 60 members, or creates a new one. The name template receives the guild count+1
// as its single %d argument. Returns the guild ID.
func (r *GuildRepository) FindOrCreateReturnGuild(returnType uint8, nameTemplate string) (uint32, error) {
	var guildID uint32
	err := r.db.QueryRow(`
		SELECT g.id FROM guilds g
		WHERE g.return_type = $1
		AND (SELECT COUNT(1) FROM guild_characters gc WHERE gc.guild_id = g.id) < 60
		LIMIT 1
	`, returnType).Scan(&guildID)
	if err == nil {
		return guildID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	// No suitable guild — count existing ones and create a new one.
	var count int
	if err := r.db.QueryRow(
		`SELECT COUNT(1) FROM guilds WHERE return_type = $1`, returnType,
	).Scan(&count); err != nil {
		return 0, err
	}

	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	name := fmt.Sprintf(nameTemplate, count+1)
	if err := tx.QueryRow(
		`INSERT INTO guilds (name, leader_id, return_type, rank_rp) VALUES ($1, 0, $2, 1200) RETURNING id`,
		name, returnType,
	).Scan(&guildID); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return guildID, nil
}

// AddMember inserts a character into a guild's member list.
func (r *GuildRepository) AddMember(guildID, charID uint32) error {
	_, err := r.db.Exec(`
		INSERT INTO guild_characters (guild_id, character_id, order_index)
		VALUES ($1, $2, (SELECT COALESCE(MAX(order_index), 0) + 1 FROM guild_characters WHERE guild_id = $1))
		ON CONFLICT (guild_id, character_id) DO NOTHING
	`, guildID, charID)
	return err
}

// Save persists guild metadata changes.
func (r *GuildRepository) Save(guild *Guild) error {
	_, err := r.db.Exec(`
		UPDATE guilds SET main_motto=$2, sub_motto=$3, comment=$4, pugi_name_1=$5, pugi_name_2=$6, pugi_name_3=$7,
		pugi_outfit_1=$8, pugi_outfit_2=$9, pugi_outfit_3=$10, pugi_outfits=$11, icon=$12, leader_id=$13 WHERE id=$1
	`, guild.ID, guild.MainMotto, guild.SubMotto, guild.Comment, guild.PugiName1, guild.PugiName2, guild.PugiName3,
		guild.PugiOutfit1, guild.PugiOutfit2, guild.PugiOutfit3, guild.PugiOutfits, guild.Icon, guild.LeaderCharID)
	return err
}

// Disband removes a guild, its members, and cleans up alliance references.
func (r *GuildRepository) Disband(guildID uint32) error {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Mission mutations lock the guild before its membership rows. Keep the
	// same order here so disbanding cannot deadlock with a concurrent mission
	// report, target change, or cancellation.
	var lockedGuildID uint32
	if err := tx.QueryRow(
		"SELECT id FROM guilds WHERE id = $1 FOR UPDATE",
		guildID,
	).Scan(&lockedGuildID); err != nil {
		return err
	}

	stmts := []string{
		"DELETE FROM guild_characters WHERE guild_id = $1",
		"DELETE FROM guilds WHERE id = $1",
		"DELETE FROM guild_alliances WHERE parent_id=$1",
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt, guildID); err != nil {
			return err
		}
	}

	if _, err := tx.Exec("UPDATE guild_alliances SET sub1_id=sub2_id, sub2_id=NULL WHERE sub1_id=$1", guildID); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE guild_alliances SET sub2_id=NULL WHERE sub2_id=$1", guildID); err != nil {
		return err
	}

	return tx.Commit()
}

// RemoveCharacter removes a character only from the specified guild.
func (r *GuildRepository) RemoveCharacter(guildID, charID uint32) error {
	result, err := r.db.Exec(
		"DELETE FROM guild_characters WHERE guild_id=$1 AND character_id=$2",
		guildID, charID,
	)
	if err != nil {
		return err
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if removed == 0 {
		return ErrGuildMemberMissing
	}
	return nil
}

// AcceptApplication deletes the application and adds the character to the guild.
func (r *GuildRepository) AcceptApplication(guildID, charID uint32) error {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize application and acceptance decisions for this character. Without
	// this lock, two guilds could both observe that no membership exists before
	// either one inserts it.
	var lockedCharID uint32
	if err := tx.QueryRow(
		`SELECT id FROM characters WHERE id = $1 FOR UPDATE`,
		charID,
	).Scan(&lockedCharID); err != nil {
		return err
	}

	var applicationID int
	if err := tx.QueryRow(`
		SELECT id
		FROM guild_applications
		WHERE guild_id = $1 AND character_id = $2 AND application_type = 'applied'
		FOR UPDATE
	`, guildID, charID).Scan(&applicationID); errors.Is(err, sql.ErrNoRows) {
		return ErrApplicationMissing
	} else if err != nil {
		return err
	}

	var alreadyMember bool
	if err := tx.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM guild_characters WHERE character_id = $1)`,
		charID,
	).Scan(&alreadyMember); err != nil {
		return err
	}
	if alreadyMember {
		return ErrAlreadyGuildMember
	}

	// Acceptance historically clears every pending application for the
	// character. Keep that behavior after first proving that the requested
	// guild owns the application being accepted.
	if _, err := tx.Exec(`DELETE FROM guild_applications WHERE character_id = $1`, charID); err != nil {
		return err
	}

	if _, err := tx.Exec(`
		INSERT INTO guild_characters (guild_id, character_id, order_index)
		VALUES ($1, $2, (SELECT COALESCE(MAX(order_index), 0) + 1 FROM guild_characters WHERE guild_id = $1))
	`, guildID, charID); err != nil {
		return err
	}

	return tx.Commit()
}

// CreateApplication inserts a guild application or invitation.
func (r *GuildRepository) CreateApplication(guildID, charID, actorID uint32, appType GuildApplicationType) error {
	_, err := r.db.Exec(
		`INSERT INTO guild_applications (guild_id, character_id, actor_id, application_type) VALUES ($1, $2, $3, $4)`,
		guildID, charID, actorID, appType)
	return err
}

// CreateInviteWithMail atomically inserts a scout invitation into guild_invites
// and sends a notification mail to the target character.
func (r *GuildRepository) CreateInviteWithMail(guildID, charID, actorID uint32, mailSenderID, mailRecipientID uint32, mailSubject, mailBody string) error {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`INSERT INTO guild_invites (guild_id, character_id, actor_id) VALUES ($1, $2, $3)`,
		guildID, charID, actorID); err != nil {
		return err
	}
	if _, err := tx.Exec(mailInsertQuery, mailSenderID, mailRecipientID, mailSubject, mailBody, 0, 0, true, false); err != nil {
		return err
	}
	return tx.Commit()
}

// HasInvite reports whether a pending scout invitation exists for the character in the guild.
func (r *GuildRepository) HasInvite(guildID, charID uint32) (bool, error) {
	var n int
	err := r.db.QueryRow(
		`SELECT 1 FROM guild_invites WHERE guild_id = $1 AND character_id = $2`,
		guildID, charID,
	).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// CancelInvite removes a scout invitation only when it belongs to the specified guild.
func (r *GuildRepository) CancelInvite(guildID, inviteID uint32) error {
	result, err := r.db.Exec(
		`DELETE FROM guild_invites WHERE id = $1 AND guild_id = $2`,
		inviteID, guildID,
	)
	if err != nil {
		return err
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if removed == 0 {
		return ErrGuildInviteMissing
	}
	return nil
}

// AcceptInvite removes the scout invitation and adds the character to the guild atomically.
func (r *GuildRepository) AcceptInvite(guildID, charID uint32) error {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`DELETE FROM guild_invites WHERE guild_id = $1 AND character_id = $2`,
		guildID, charID); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO guild_characters (guild_id, character_id, order_index)
		VALUES ($1, $2, (SELECT MAX(order_index) + 1 FROM guild_characters WHERE guild_id = $1))
	`, guildID, charID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeclineInvite removes a scout invitation without joining the guild.
func (r *GuildRepository) DeclineInvite(guildID, charID uint32) error {
	_, err := r.db.Exec(
		`DELETE FROM guild_invites WHERE guild_id = $1 AND character_id = $2`,
		guildID, charID,
	)
	return err
}

// RejectApplication removes an applied application for a character.
func (r *GuildRepository) RejectApplication(guildID, charID uint32) error {
	result, err := r.db.Exec(
		`DELETE FROM guild_applications WHERE character_id = $1 AND guild_id = $2 AND application_type = 'applied'`,
		charID, guildID,
	)
	if err != nil {
		return err
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if removed == 0 {
		return ErrApplicationMissing
	}
	return nil
}

// ArrangeCharacters atomically reorders members of the specified guild. If any
// character is not a member of that guild, none of the order changes are kept.
func (r *GuildRepository) ArrangeCharacters(guildID uint32, charIDs []uint32) error {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for i, id := range charIDs {
		result, err := tx.Exec(
			"UPDATE guild_characters SET order_index = $1 WHERE guild_id = $2 AND character_id = $3",
			2+i, guildID, id,
		)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return ErrGuildMemberMissing
		}
	}

	return tx.Commit()
}

// GetApplication retrieves a specific application by character, guild, and type.
// Returns nil, nil if not found.
func (r *GuildRepository) GetApplication(guildID, charID uint32, appType GuildApplicationType) (*GuildApplication, error) {
	app := &GuildApplication{}
	err := r.db.QueryRowx(`
		SELECT * from guild_applications WHERE character_id = $1 AND guild_id = $2 AND application_type = $3
	`, charID, guildID, appType).StructScan(app)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return app, nil
}

// HasApplication checks whether any application exists for the character in the guild.
func (r *GuildRepository) HasApplication(guildID, charID uint32) (bool, error) {
	var n int
	err := r.db.QueryRow(`SELECT 1 from guild_applications WHERE character_id = $1 AND guild_id = $2`, charID, guildID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// GetItemBox returns the raw item_box bytes for a guild.
func (r *GuildRepository) GetItemBox(guildID uint32) ([]byte, error) {
	var data []byte
	err := r.db.QueryRow(`SELECT item_box FROM guilds WHERE id=$1`, guildID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return data, err
}

// SaveItemBox writes the serialized item box data for a guild.
func (r *GuildRepository) SaveItemBox(guildID uint32, data []byte) error {
	_, err := r.db.Exec(`UPDATE guilds SET item_box=$1 WHERE id=$2`, data, guildID)
	return err
}

// UpdateItemBox applies mutate to the guild's item box inside a single transaction
// that holds a row lock for the duration. A guild box is shared across accounts, so
// unlike the per-account box it races whenever two members use it at once — no
// multi-boxing required. See UserRepository.UpdateItemBox for the failure mode.
func (r *GuildRepository) UpdateItemBox(guildID uint32, mutate func(current []byte) []byte) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return fmt.Errorf("begin guild item box tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback is no-op after commit

	var current []byte
	err = tx.QueryRow(`SELECT item_box FROM guilds WHERE id=$1 FOR UPDATE`, guildID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("update guild item box: guild %d not found", guildID)
	} else if err != nil {
		return fmt.Errorf("lock guild item box: %w", err)
	}

	if _, err := tx.Exec(`UPDATE guilds SET item_box=$1 WHERE id=$2`, mutate(current), guildID); err != nil {
		return fmt.Errorf("write guild item box: %w", err)
	}
	return tx.Commit()
}

// GetMembers loads all members (or applicants) of a guild.
func (r *GuildRepository) GetMembers(guildID uint32, applicants bool) ([]*GuildMember, error) {
	rows, err := r.db.Queryx(fmt.Sprintf(`
		%s
		WHERE character.guild_id = $1 AND is_applicant = $2
	`, guildMembersSelectSQL), guildID, applicants)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	members := make([]*GuildMember, 0)
	for rows.Next() {
		member, err := scanGuildMember(rows)
		if err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, nil
}

// GetCharacterMembership loads a character's guild membership data.
// Returns nil, nil if the character is not in any guild.
func (r *GuildRepository) GetCharacterMembership(charID uint32) (*GuildMember, error) {
	rows, err := r.db.Queryx(fmt.Sprintf(`
		%s
		WHERE character.character_id = $1
		ORDER BY character.is_applicant ASC, character.guild_id ASC
		LIMIT 1
	`, guildMembersSelectSQL), charID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		return nil, nil
	}
	return scanGuildMember(rows)
}

// SaveMember persists guild member changes (avoid_leadership and order_index).
func (r *GuildRepository) SaveMember(member *GuildMember) error {
	_, err := r.db.Exec(
		"UPDATE guild_characters SET avoid_leadership=$1, order_index=$2 WHERE character_id=$3",
		member.AvoidLeadership, member.OrderIndex, member.CharID,
	)
	return err
}

// SetRecruiting updates whether a guild is accepting applications.
func (r *GuildRepository) SetRecruiting(guildID uint32, recruiting bool) error {
	_, err := r.db.Exec("UPDATE guilds SET recruiting=$1 WHERE id=$2", recruiting, guildID)
	return err
}

// SetPugiOutfits updates the unlocked pugi outfit bitmask.
func (r *GuildRepository) SetPugiOutfits(guildID uint32, outfits uint32) error {
	_, err := r.db.Exec(`UPDATE guilds SET pugi_outfits=$1 WHERE id=$2`, outfits, guildID)
	return err
}

// SetRecruiter updates a target member only while both actor and target still
// belong to the specified guild. Role policy remains client/original behavior.
func (r *GuildRepository) SetRecruiter(guildID, actorCharID, targetCharID uint32, allowed bool) error {
	result, err := r.db.Exec(
		`UPDATE guild_characters AS target
		 SET recruiter=$1
		 WHERE target.guild_id=$2
		   AND target.character_id=$4
		   AND EXISTS (
		       SELECT 1 FROM guild_characters AS actor
		       WHERE actor.guild_id=$2 AND actor.character_id=$3
		   )`,
		allowed, guildID, actorCharID, targetCharID,
	)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return ErrGuildMemberMissing
	}
	return nil
}

// GuildInvite represents a pending scout invitation with the target character's info.
type GuildInvite struct {
	ID        uint32    `db:"id"`
	GuildID   uint32    `db:"guild_id"`
	CharID    uint32    `db:"character_id"`
	ActorID   uint32    `db:"actor_id"`
	InvitedAt time.Time `db:"created_at"`
	HR        uint16    `db:"hr"`
	GR        uint16    `db:"gr"`
	Name      string    `db:"name"`
}

// ListInvites returns all pending scout invitations for a guild, including
// the target character's HR, GR, and name.
func (r *GuildRepository) ListInvites(guildID uint32) ([]*GuildInvite, error) {
	rows, err := r.db.Queryx(`
		SELECT gi.id, gi.guild_id, gi.character_id, gi.actor_id, gi.created_at,
		       c.hr, c.gr, c.name
		FROM guild_invites gi
		JOIN characters c ON c.id = gi.character_id
		WHERE gi.guild_id = $1
	`, guildID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var invites []*GuildInvite
	for rows.Next() {
		inv := &GuildInvite{}
		if err := rows.StructScan(inv); err != nil {
			continue
		}
		invites = append(invites, inv)
	}
	return invites, nil
}
