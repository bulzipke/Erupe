-- Melody is a maximum earned entitlement shared by an account, not a sum of
-- character awards. Expired rows remain as audit history and cannot be spent.
CREATE TABLE IF NOT EXISTS diva_melody_wallets (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    earned SMALLINT NOT NULL CHECK (earned BETWEEN 1 AND 10),
    spent SMALLINT NOT NULL DEFAULT 0 CHECK (spent BETWEEN 0 AND earned),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id,event_id)
);
ALTER TABLE diva_reward_receipts DROP CONSTRAINT IF EXISTS diva_reward_receipts_item_type_check;
ALTER TABLE diva_reward_receipts ADD CONSTRAINT diva_reward_receipts_item_type_check CHECK (
    item_type IN (7,26) OR
    (item_type=29 AND reward_type=6 AND item_id=0 AND quantity BETWEEN 1 AND 10)
);
