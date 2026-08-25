package channelserver

import (
	"context"
	"time"
)

// ListPosts returns active guild posts of the given type, ordered by newest first.
func (r *GuildRepository) ListPosts(guildID uint32, postType int) ([]*MessageBoardPost, error) {
	rows, err := r.db.Queryx(
		`SELECT id, stamp_id, title, body, author_id, created_at, liked_by
		 FROM guild_posts WHERE guild_id = $1 AND post_type = $2 AND deleted = false
		 ORDER BY created_at DESC`, guildID, postType)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var posts []*MessageBoardPost
	for rows.Next() {
		post := &MessageBoardPost{}
		if err := rows.StructScan(post); err != nil {
			continue
		}
		posts = append(posts, post)
	}
	return posts, nil
}

// CreatePost inserts a new guild post and soft-deletes excess posts beyond maxPosts.
func (r *GuildRepository) CreatePost(guildID, authorID, stampID uint32, postType int, title, body string, maxPosts int) error {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`INSERT INTO guild_posts (guild_id, author_id, stamp_id, post_type, title, body) VALUES ($1, $2, $3, $4, $5, $6)`,
		guildID, authorID, stampID, postType, title, body); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE guild_posts SET deleted = true WHERE id IN (
		SELECT id FROM guild_posts WHERE guild_id = $1 AND post_type = $2 AND deleted = false
		ORDER BY created_at DESC OFFSET $3
	)`, guildID, postType, maxPosts); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePost soft-deletes a post only when it belongs to the given guild.
func (r *GuildRepository) DeletePost(guildID, postID uint32) error {
	return requireGuildScopedMutation(r.db.Exec(
		"UPDATE guild_posts SET deleted = true WHERE guild_id = $1 AND id = $2",
		guildID, postID,
	))
}

// UpdatePost updates the title and body only when the post belongs to the given guild.
func (r *GuildRepository) UpdatePost(guildID, postID uint32, title, body string) error {
	return requireGuildScopedMutation(r.db.Exec(
		"UPDATE guild_posts SET title = $1, body = $2 WHERE guild_id = $3 AND id = $4",
		title, body, guildID, postID,
	))
}

// UpdatePostStamp updates the stamp only when the post belongs to the given guild.
func (r *GuildRepository) UpdatePostStamp(guildID, postID, stampID uint32) error {
	return requireGuildScopedMutation(r.db.Exec(
		"UPDATE guild_posts SET stamp_id = $1 WHERE guild_id = $2 AND id = $3",
		stampID, guildID, postID,
	))
}

// GetPostLikedBy returns the liked_by CSV string for a post in the given guild.
func (r *GuildRepository) GetPostLikedBy(guildID, postID uint32) (string, error) {
	var likedBy string
	err := r.db.QueryRow(
		"SELECT liked_by FROM guild_posts WHERE guild_id = $1 AND id = $2",
		guildID, postID,
	).Scan(&likedBy)
	return likedBy, err
}

// SetPostLikedBy updates likes only when the post belongs to the given guild.
func (r *GuildRepository) SetPostLikedBy(guildID, postID uint32, likedBy string) error {
	return requireGuildScopedMutation(r.db.Exec(
		"UPDATE guild_posts SET liked_by = $1 WHERE guild_id = $2 AND id = $3",
		likedBy, guildID, postID,
	))
}

// CountNewPosts returns the count of non-deleted posts created after the given time.
func (r *GuildRepository) CountNewPosts(guildID uint32, since time.Time) (int, error) {
	var count int
	err := r.db.QueryRow(
		`SELECT COUNT(*) FROM guild_posts WHERE guild_id = $1 AND deleted = false AND (EXTRACT(epoch FROM created_at)::int) > $2`,
		guildID, since.Unix()).Scan(&count)
	return count, err
}
