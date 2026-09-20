-- Rewards are only consumed in the transaction which saves their client-side
-- inventory (item_type 7) or GP balance (26), never merely on an availability query.
CREATE TABLE diva_reward_receipts (
    id BIGSERIAL PRIMARY KEY CHECK (id BETWEEN 1 AND 4294967295),
    char_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    reward_type SMALLINT NOT NULL CHECK (reward_type BETWEEN 0 AND 7),
    catalog_key TEXT NOT NULL,
    item_type SMALLINT NOT NULL CHECK (item_type IN (7,26)),
    item_id INTEGER NOT NULL CHECK (item_id BETWEEN 0 AND 65535),
    quantity INTEGER NOT NULL CHECK (quantity BETWEEN 1 AND 65535),
    offered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at TIMESTAMPTZ,
    UNIQUE(char_id,event_id,reward_type,catalog_key)
);
CREATE INDEX diva_reward_receipts_pending ON diva_reward_receipts(char_id,event_id,reward_type) WHERE claimed_at IS NULL;
