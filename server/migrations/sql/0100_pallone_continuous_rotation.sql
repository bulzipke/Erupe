-- Opt-in only: no round is seeded and existing festival results are retained.
CREATE TABLE pallone_event_lifecycle (
    singleton BOOLEAN PRIMARY KEY CHECK (singleton),
    event_id INTEGER NOT NULL REFERENCES pallone_events(id),
    first_event_id INTEGER NOT NULL REFERENCES pallone_events(id),
    anchor TIMESTAMPTZ NOT NULL,
    active_days INTEGER NOT NULL CHECK (active_days BETWEEN 7 AND 364 AND active_days % 7 = 0),
    claim_days INTEGER NOT NULL CHECK (claim_days > 0 AND claim_days <= active_days),
    profile_sha256 CHAR(64) NOT NULL CHECK (profile_sha256 ~ '^[0-9a-f]{64}$'),
    CHECK (event_id >= first_event_id)
);
