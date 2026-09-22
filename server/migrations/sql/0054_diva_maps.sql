-- Server-custom map v1, not a restored retail map. This cutover is permanent:
-- reapplying the migration must not exclude rounds that earned map progress.
CREATE TABLE IF NOT EXISTS diva_map_cutover (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    installed_at TIMESTAMPTZ NOT NULL
);
INSERT INTO diva_map_cutover(singleton,installed_at) VALUES(TRUE,clock_timestamp())
ON CONFLICT(singleton) DO NOTHING;

CREATE TABLE IF NOT EXISTS diva_map_legacy_events (
    event_id INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE RESTRICT
);
INSERT INTO diva_map_legacy_events(event_id)
SELECT p.event_id FROM diva_interception_periods p CROSS JOIN diva_map_cutover c
WHERE p.starts_at <= c.installed_at
ON CONFLICT(event_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS diva_map_events (
    event_id INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE RESTRICT,
    rules_version TEXT NOT NULL CHECK(rules_version='custom-v1'),
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL CHECK(ends_at>starts_at)
);

-- Guild identity and name are historical snapshots, not cascading guild FKs.
CREATE TABLE IF NOT EXISTS diva_map_guilds (
    event_id INTEGER NOT NULL REFERENCES diva_map_events(event_id) ON DELETE RESTRICT,
    guild_id BIGINT NOT NULL CHECK(guild_id>0),
    guild_name TEXT NOT NULL,
    current_number INTEGER NOT NULL CHECK(current_number BETWEEN 1 AND 65535),
    acquired_areas INTEGER NOT NULL DEFAULT 0 CHECK(acquired_areas>=0),
    last_settled_at TIMESTAMPTZ NOT NULL,
    map_data JSONB NOT NULL CHECK(jsonb_typeof(map_data)='object'),
    PRIMARY KEY(event_id,guild_id)
);

CREATE TABLE IF NOT EXISTS diva_map_departures (
    char_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    event_id INTEGER NOT NULL,
    run_key UUID NOT NULL,
    guild_id BIGINT NOT NULL,
    map_number INTEGER NOT NULL CHECK(map_number BETWEEN 1 AND 65535),
    quest_id INTEGER NOT NULL CHECK(quest_id BETWEEN 1 AND 65535),
    route INTEGER NOT NULL CHECK(route BETWEEN 0 AND 65535),
    eligible BOOLEAN NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(char_id,event_id,run_key),
    FOREIGN KEY(event_id,guild_id) REFERENCES diva_map_guilds(event_id,guild_id) ON DELETE RESTRICT
);

-- Preserve the state that actually existed at departure, even when binding is
-- delayed across an hourly unlock or transition. Unchanged hours need no copy.
CREATE TABLE IF NOT EXISTS diva_map_snapshots (
    event_id INTEGER NOT NULL,
    guild_id BIGINT NOT NULL,
    settled_at TIMESTAMPTZ NOT NULL,
    map_number INTEGER NOT NULL CHECK(map_number BETWEEN 1 AND 65535),
    map_data JSONB NOT NULL CHECK(jsonb_typeof(map_data)='object'),
    PRIMARY KEY(event_id,guild_id,settled_at),
    FOREIGN KEY(event_id,guild_id) REFERENCES diva_map_guilds(event_id,guild_id) ON DELETE RESTRICT
);

-- Guild reward queries pin a character to one guild per round; changing guild
-- cannot claim another guild's threshold prizes again in that same round.
CREATE TABLE IF NOT EXISTS diva_guild_reward_memberships (
    char_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    guild_id BIGINT NOT NULL CHECK(guild_id>0),
    PRIMARY KEY(char_id,event_id)
);

CREATE TABLE IF NOT EXISTS diva_map_contributions (
    char_id INTEGER NOT NULL,
    event_id INTEGER NOT NULL,
    run_key UUID NOT NULL,
    guild_id BIGINT NOT NULL,
    map_number INTEGER NOT NULL CHECK(map_number BETWEEN 1 AND 65535),
    route INTEGER NOT NULL CHECK(route BETWEEN 0 AND 65535),
    points BIGINT NOT NULL CHECK(points BETWEEN 1 AND 4294967295),
    credited_points BIGINT NOT NULL DEFAULT 0 CHECK(credited_points>=0 AND credited_points<=points),
    treasure_participation BOOLEAN NOT NULL DEFAULT FALSE,
    submitted_at TIMESTAMPTZ NOT NULL,
    eligible_at TIMESTAMPTZ NOT NULL CHECK(eligible_at>=submitted_at),
    settled_at TIMESTAMPTZ,
    PRIMARY KEY(char_id,event_id,run_key),
    FOREIGN KEY(char_id,event_id,run_key) REFERENCES diva_map_departures(char_id,event_id,run_key) ON DELETE CASCADE,
    FOREIGN KEY(event_id,guild_id) REFERENCES diva_map_guilds(event_id,guild_id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS diva_map_contributions_pending
ON diva_map_contributions(event_id,guild_id,eligible_at) WHERE settled_at IS NULL;

CREATE TABLE IF NOT EXISTS diva_map_area_awards (
    event_id INTEGER NOT NULL,
    guild_id BIGINT NOT NULL,
    map_number INTEGER NOT NULL CHECK(map_number BETWEEN 1 AND 65535),
    coordinate INTEGER NOT NULL CHECK(coordinate BETWEEN 1 AND 65535),
    is_branch BOOLEAN NOT NULL,
    awarded_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(event_id,guild_id,map_number,coordinate),
    FOREIGN KEY(event_id,guild_id) REFERENCES diva_map_guilds(event_id,guild_id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS diva_map_area_awards_publication
ON diva_map_area_awards(event_id,awarded_at,guild_id);

-- Even an empty publication is frozen. No future query may backfill it.
CREATE TABLE IF NOT EXISTS diva_map_publications (
    event_id INTEGER NOT NULL REFERENCES diva_map_events(event_id) ON DELETE RESTRICT,
    published_at TIMESTAMPTZ NOT NULL,
    is_final BOOLEAN NOT NULL,
    PRIMARY KEY(event_id,published_at)
);
CREATE TABLE IF NOT EXISTS diva_map_ranks (
    event_id INTEGER NOT NULL,
    published_at TIMESTAMPTZ NOT NULL,
    guild_id BIGINT NOT NULL CHECK(guild_id>0),
    name TEXT NOT NULL,
    areas INTEGER NOT NULL CHECK(areas>0),
    rank BIGINT NOT NULL CHECK(rank BETWEEN 1 AND 2147483647),
    PRIMARY KEY(event_id,published_at,guild_id),
    FOREIGN KEY(event_id,published_at) REFERENCES diva_map_publications(event_id,published_at) ON DELETE RESTRICT
);
