-- Per-character starter receipts are independent of account license purchases.
-- No live policy, item, ownership or legacy migration is seeded.
-- The high-range receipt sequence now has two owners in application terms;
-- dropping either ledger must not cascade into dropping the shared sequence.
ALTER SEQUENCE campaign_coin_reward_id_seq OWNED BY NONE;

CREATE TABLE quest_license_starter_policies (
    starter_key text PRIMARY KEY CHECK (starter_key ~ '^[a-z][a-z0-9_-]{0,63}$'),
    license_keys text[] NOT NULL CHECK (cardinality(license_keys) BETWEEN 1 AND 64),
    item_id integer NOT NULL CHECK (item_id BETWEEN 1 AND 65535),
    quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 65535),
    legacy_campaign_ids integer[] NOT NULL,
    policy_hash text NOT NULL CHECK (policy_hash ~ '^[0-9a-f]{64}$'),
    bound_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE quest_license_starters (
    -- Shared sequence prevents these receipts from colliding with existing
    -- paid campaign item receipts. No native handler routes them yet.
    id integer PRIMARY KEY DEFAULT nextval('campaign_coin_reward_id_seq'),
    user_id integer NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    character_id integer NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    starter_key text NOT NULL REFERENCES quest_license_starter_policies(starter_key),
    item_id integer NOT NULL CHECK (item_id BETWEEN 1 AND 65535),
    quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 65535),
    state text NOT NULL CHECK (state IN ('pending','claimed','legacy_claimed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    UNIQUE(character_id,starter_key),
    CHECK (id >= 1073741824)
);
CREATE INDEX quest_license_starters_pending ON quest_license_starters(user_id,character_id) WHERE state='pending';
