-- This migration alone does not reset any progress. Rotation is opt-in.
CREATE TABLE tower_events (
    earth_id integer PRIMARY KEY CHECK (earth_id BETWEEN 1 AND 65535),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    claim_until timestamptz NOT NULL,
    CHECK (starts_at < ends_at AND ends_at < claim_until)
);
CREATE TABLE tower_event_lifecycle (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    earth_id integer NOT NULL REFERENCES tower_events(earth_id),
    anchor timestamptz NOT NULL,
    active_days integer NOT NULL CHECK (active_days > 0),
    cycle_days integer NOT NULL CHECK (cycle_days > active_days AND cycle_days <= 365)
);
CREATE TABLE tower_round_character_state (
    earth_id integer NOT NULL REFERENCES tower_events(earth_id),
    character_id integer NOT NULL,
    block1 integer NOT NULL,
    block2 integer NOT NULL,
    guardian1 integer NOT NULL,
    guardian2 integer NOT NULL,
    gems text NOT NULL,
    PRIMARY KEY (earth_id, character_id)
);
CREATE TABLE tower_round_snapshots (
    earth_id integer NOT NULL REFERENCES tower_events(earth_id),
    kind text NOT NULL,
    owner_id integer NOT NULL,
    data jsonb NOT NULL,
    PRIMARY KEY (earth_id, kind, owner_id)
);
ALTER TABLE tower_settlements ADD COLUMN earth_id integer NOT NULL DEFAULT 0;
ALTER TABLE tower DROP CONSTRAINT IF EXISTS tower_guardian1_check;
ALTER TABLE tower DROP CONSTRAINT IF EXISTS tower_guardian2_check;
ALTER TABLE tower ADD CONSTRAINT tower_guardian1_check CHECK (guardian1 BETWEEN 0 AND 9999);
ALTER TABLE tower ADD CONSTRAINT tower_guardian2_check CHECK (guardian2 BETWEEN 0 AND 9999);
