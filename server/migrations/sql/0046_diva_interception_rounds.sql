-- Legacy JSON has no round/date information. Preserve it without importing it
-- into the new reward ledger. Future, not-yet-started rounds remain eligible.
CREATE TABLE diva_interception_legacy_events (
    event_id INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE
);
INSERT INTO diva_interception_legacy_events(event_id)
SELECT id FROM events WHERE event_type='diva' AND start_time <= NOW();

-- Phase 1 already has its own persistent diva_debug_song_event anchor.
CREATE TABLE diva_event_lifecycle (
    mode SMALLINT PRIMARY KEY CHECK (mode IN (-1,2,3)),
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE
);
INSERT INTO diva_event_lifecycle(mode,event_id)
SELECT -1,e.id FROM events e WHERE e.event_type='diva'
AND NOT EXISTS (SELECT 1 FROM diva_debug_song_event d WHERE d.event_id=e.id)
ORDER BY e.start_time DESC,e.id DESC LIMIT 1;

-- Guild ID is a historical snapshot, not a FK: disbanding a guild must not
-- erase already-earned personal progress. A departure can be submitted once.
CREATE TABLE diva_interception_runs (
    char_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    run_key UUID NOT NULL,
    quest_id INTEGER NOT NULL CHECK (quest_id BETWEEN 1 AND 65535),
    guild_id BIGINT NOT NULL CHECK (guild_id > 0),
    points BIGINT NOT NULL CHECK (points BETWEEN 1 AND 4294967295),
    started_at TIMESTAMPTZ NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL CHECK (submitted_at >= started_at),
    PRIMARY KEY(char_id,event_id,run_key)
);
CREATE INDEX diva_interception_runs_event_character ON diva_interception_runs(event_id,char_id);
