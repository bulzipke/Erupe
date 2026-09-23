-- Sky Corridor progress belongs to one EarthID. Character tower rank itself
-- remains in the legacy tower table and is never reset by this migration.
CREATE TABLE IF NOT EXISTS tower_event_progress (
    earth_id integer NOT NULL,
    character_id integer NOT NULL,
    block1_floors integer NOT NULL DEFAULT 0 CHECK (block1_floors >= 0),
    block2_floors integer NOT NULL DEFAULT 0 CHECK (block2_floors >= 0),
    PRIMARY KEY (earth_id, character_id)
);

-- Rotates at 12:00 JST. Each accepted completed Tower run contributes once.
CREATE TABLE IF NOT EXISTS tower_daily_progress (
    earth_id integer NOT NULL,
    character_id integer NOT NULL,
    day_start timestamptz NOT NULL,
    floors integer NOT NULL DEFAULT 0 CHECK (floors >= 0),
    antiques integer NOT NULL DEFAULT 0 CHECK (antiques >= 0),
    chests integer NOT NULL DEFAULT 0 CHECK (chests >= 0),
    cats integer NOT NULL DEFAULT 0 CHECK (cats >= 0),
    trp integer NOT NULL DEFAULT 0 CHECK (trp >= 0),
    slays integer NOT NULL DEFAULT 0 CHECK (slays >= 0),
    PRIMARY KEY (earth_id, character_id, day_start)
);

-- The global guardian kill table counts a party stage once. This companion
-- table tracks each participating hunter for early-contribution eligibility.
ALTER TABLE tower_guardian_kills
    ADD COLUMN IF NOT EXISTS before_casual boolean NOT NULL DEFAULT true;
CREATE TABLE IF NOT EXISTS tower_guardian_participants (
    earth_id integer NOT NULL,
    block smallint NOT NULL CHECK (block IN (1, 2)),
    run_id text NOT NULL,
    character_id integer NOT NULL,
    before_casual boolean NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (earth_id, block, run_id, character_id)
);
CREATE INDEX IF NOT EXISTS tower_guardian_participants_eligibility
    ON tower_guardian_participants (earth_id, character_id)
    WHERE before_casual;

-- A unique receipt and warehouse update commit together when a reward is
-- claimed; repeated or parallel requests cannot issue the item twice.
CREATE TABLE IF NOT EXISTS tower_reward_claims (
    earth_id integer NOT NULL,
    character_id integer NOT NULL,
    reward_kind smallint NOT NULL CHECK (reward_kind IN (1, 2, 3)),
    reward_index integer NOT NULL,
    item_id integer NOT NULL,
    quantity integer NOT NULL,
    claimed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (earth_id, character_id, reward_kind, reward_index)
);
