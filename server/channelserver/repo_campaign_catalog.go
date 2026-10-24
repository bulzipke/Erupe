package channelserver

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// CampaignCatalogRepository reads all three native sections from one read-only
// snapshot. It does not filter expired or unlinked events, or modify any code,
// reward, purchase or claim history.
type CampaignCatalogRepository struct{ db *sqlx.DB }

func NewCampaignCatalogRepository(db *sqlx.DB) *CampaignCatalogRepository {
	return &CampaignCatalogRepository{db: db}
}

func (r *CampaignCatalogRepository) Load(ctx context.Context) (CampaignCatalogSnapshot, error) {
	var result CampaignCatalogSnapshot
	if r == nil || r.db == nil {
		return result, fmt.Errorf("event house catalog repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.SelectContext(ctx, &result.Events, `SELECT c.*, COALESCE((SELECT p.price FROM campaign_coin_prices p
		WHERE p.campaign_id=c.id AND EXISTS(SELECT 1 FROM campaign_category_links l
		JOIN campaign_categories cat ON cat.id=l.category_id
		WHERE l.campaign_id=c.id AND cat.type=1 AND cat.id IN(6,7))),0) AS coin_price FROM campaigns c`)
	if err != nil {
		return CampaignCatalogSnapshot{}, err
	}
	// Keep the same visible categories as the legacy response. Hidden category
	// links are omitted too; their campaign and redemption records are retained.
	err = tx.SelectContext(ctx, &result.Categories, `SELECT id, type, title, description FROM campaign_categories
		WHERE type = 1 AND id NOT IN (8,9,10,11) ORDER BY id`)
	if err != nil {
		return CampaignCatalogSnapshot{}, err
	}
	err = tx.SelectContext(ctx, &result.Links, `SELECT l.campaign_id, l.category_id
		FROM campaign_category_links l JOIN campaign_categories c ON c.id = l.category_id
		WHERE c.type = 1 AND c.id NOT IN (8,9,10,11) ORDER BY l.id`)
	if err != nil {
		return CampaignCatalogSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return CampaignCatalogSnapshot{}, err
	}
	return result, nil
}
