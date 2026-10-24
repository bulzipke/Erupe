-- Separate event-scoped ledger; no imports from CP or Ryoudama balances.
-- No event or route is seeded, and no Earth configuration is changed.
CREATE TABLE pallone_events (
    id INTEGER PRIMARY KEY CHECK (id > 0),
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    claim_until TIMESTAMPTZ NOT NULL,
    CHECK (starts_at < ends_at AND ends_at < claim_until)
);

CREATE TABLE pallone_event_routes (
    event_id INTEGER NOT NULL REFERENCES pallone_events(id),
    route_key INTEGER NOT NULL CHECK (route_key BETWEEN 1 AND 32767),
    star SMALLINT NOT NULL CHECK (star BETWEEN 0 AND 10),
    stage_quest_ids INTEGER[] NOT NULL,
    PRIMARY KEY (event_id, route_key),
    CHECK (cardinality(stage_quest_ids) BETWEEN 1 AND 11),
    CHECK (array_ndims(stage_quest_ids) = 1 AND array_lower(stage_quest_ids,1) = 1),
    CHECK (0 < ALL(stage_quest_ids) AND 65536 > ALL(stage_quest_ids)),
    CHECK (array_position(stage_quest_ids,NULL) IS NULL)
);

CREATE TABLE pallone_results (
    character_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    run_id CHAR(32) NOT NULL CHECK (run_id ~ '^[0-9a-f]{32}$'),
    event_id INTEGER NOT NULL,
    route_key INTEGER NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL,
    final_quest_id INTEGER NOT NULL CHECK (final_quest_id BETWEEN 1 AND 65535),
    base_score INTEGER NOT NULL CHECK (base_score > 0),
    premium_bonus INTEGER NOT NULL CHECK (premium_bonus >= 0),
    item_bonus INTEGER NOT NULL CHECK (item_bonus >= 0),
    total_score INTEGER NOT NULL CHECK (total_score > 0),
    submitted_guild_id BIGINT NOT NULL CHECK (submitted_guild_id BETWEEN 0 AND 4294967295),
    guild_id BIGINT REFERENCES guilds(id) ON DELETE SET NULL,
    multiplier INTEGER NOT NULL DEFAULT 0 CHECK (multiplier = 0),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id,run_id),
    FOREIGN KEY (event_id,route_key) REFERENCES pallone_event_routes(event_id,route_key),
    CHECK (started_at <= completed_at),
    CHECK (total_score::bigint = base_score::bigint + item_bonus::bigint),
    CHECK (total_score::bigint + premium_bonus::bigint <= 2147483647)
);
CREATE INDEX pallone_results_personal ON pallone_results(event_id,character_id,route_key);
CREATE INDEX pallone_results_guild ON pallone_results(event_id,guild_id,route_key) WHERE guild_id IS NOT NULL;
