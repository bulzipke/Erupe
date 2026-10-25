-- Native TINY_BIN 0/1/2 pending items and immutable per-mission award receipts.
-- No historical progress or completion flags are backfilled.
CREATE TABLE tower_daily_rewards (
    id bigserial PRIMARY KEY,
    earth_id integer NOT NULL,
    character_id integer NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    day_start timestamptz NOT NULL,
    mission_slot smallint NOT NULL CHECK (mission_slot BETWEEN 0 AND 5),
    item_slot smallint NOT NULL CHECK (item_slot BETWEEN 0 AND 1),
    item_id integer NOT NULL CHECK (item_id BETWEEN 1 AND 65535),
    quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 255),
    claimed_at timestamptz,
    UNIQUE (earth_id, character_id, day_start, mission_slot, item_slot)
);
CREATE INDEX tower_daily_rewards_pending
    ON tower_daily_rewards (character_id, earth_id, id) WHERE claimed_at IS NULL;
