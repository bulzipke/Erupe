-- One receipt per quest-stage instance, not per hunter. EarthID is the
-- manually selected Tower season; changing it starts independent counts.
CREATE TABLE IF NOT EXISTS tower_guardian_kills (
    earth_id INTEGER NOT NULL,
    block SMALLINT NOT NULL CHECK (block IN (1, 2)),
    run_id TEXT NOT NULL,
    character_id INTEGER NOT NULL,
    killed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (earth_id, block, run_id)
);
