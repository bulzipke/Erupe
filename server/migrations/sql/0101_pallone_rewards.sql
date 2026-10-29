-- No events, balances or inventory are seeded or altered by this migration.
CREATE TABLE pallone_run_reports (
    character_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    run_id CHAR(32) NOT NULL CHECK (run_id ~ '^[0-9a-f]{32}$'),
    event_id INTEGER NOT NULL,
    route_key INTEGER NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    score_reported_at TIMESTAMPTZ,
    result_reported_at TIMESTAMPTZ,
    rejected_at TIMESTAMPTZ,
    rejection_reason TEXT,
    final_quest_id INTEGER NOT NULL,
    base_score INTEGER CHECK (base_score > 0),
    premium_bonus INTEGER CHECK (premium_bonus >= 0),
    item_bonus INTEGER CHECK (item_bonus >= 0),
    guild_id BIGINT CHECK (guild_id BETWEEN 0 AND 4294967295),
    PRIMARY KEY(character_id,run_id),
    FOREIGN KEY(event_id,route_key) REFERENCES pallone_event_routes(event_id,route_key),
    CHECK(completed_at IS NULL OR completed_at >= started_at),
    CHECK((base_score IS NULL AND premium_bonus IS NULL AND item_bonus IS NULL AND guild_id IS NULL)
       OR (base_score IS NOT NULL AND premium_bonus IS NOT NULL AND item_bonus IS NOT NULL AND guild_id IS NOT NULL))
);

CREATE TABLE pallone_reward_profiles (
    event_id INTEGER PRIMARY KEY REFERENCES pallone_events(id),
    profile JSONB NOT NULL
);
CREATE TABLE pallone_event_finalizations (
    event_id INTEGER PRIMARY KEY REFERENCES pallone_events(id),
    finalized_at TIMESTAMPTZ NOT NULL,
    reward_profile JSONB NOT NULL
);
CREATE TABLE pallone_final_ranks (
    event_id INTEGER NOT NULL REFERENCES pallone_event_finalizations(event_id),
    kind SMALLINT NOT NULL CHECK(kind BETWEEN 1 AND 4),
    route_key INTEGER NOT NULL,
    subject_id BIGINT NOT NULL,
    score INTEGER NOT NULL CHECK(score > 0),
    rank INTEGER NOT NULL CHECK(rank > 0),
    PRIMARY KEY(event_id,kind,route_key,subject_id)
);
CREATE TABLE pallone_final_guild_members (
    event_id INTEGER NOT NULL REFERENCES pallone_event_finalizations(event_id),
    character_id INTEGER NOT NULL,
    guild_id BIGINT NOT NULL,
    contribution INTEGER NOT NULL CHECK(contribution >= 0),
    guild_points BIGINT NOT NULL CHECK(guild_points >= 0),
    PRIMARY KEY(event_id,character_id)
);
-- Colour totals are separate from CP and festival score. Their reconstruction
-- policy is versioned in the reward profile, never inferred from CP balances.
CREATE TABLE pallone_guild_contributions (
    event_id INTEGER NOT NULL REFERENCES pallone_events(id),
    character_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    run_id CHAR(32) NOT NULL,
    guild_id BIGINT NOT NULL,
    points INTEGER NOT NULL CHECK(points > 0),
    PRIMARY KEY(character_id,run_id),
    FOREIGN KEY(character_id,run_id) REFERENCES pallone_results(character_id,run_id)
);

-- Tower round IDs use low words 1..49151. Reserve 49152..65535 for
-- Pallone across every round, including Tower IDs whose sign bit is set.
CREATE SEQUENCE pallone_reward_offer_id_seq AS BIGINT MINVALUE 1 MAXVALUE 1073741824 START 1 NO CYCLE;
CREATE FUNCTION pallone_next_reward_id() RETURNS BIGINT LANGUAGE SQL VOLATILE AS $$
    SELECT ((n / 16384) * 65536) + 49152 + (n % 16384)
    FROM (SELECT nextval('pallone_reward_offer_id_seq') - 1 AS n) ordinal
$$;
CREATE TABLE pallone_reward_offers (
    id BIGINT PRIMARY KEY DEFAULT pallone_next_reward_id(),
    event_id INTEGER NOT NULL REFERENCES pallone_events(id),
    character_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    reward_key TEXT NOT NULL,
    present_type INTEGER NOT NULL,
    item_type INTEGER NOT NULL CHECK(item_type IN (7201,7202,7203)),
    item_id INTEGER NOT NULL CHECK(item_id BETWEEN 0 AND 65535),
    quantity INTEGER NOT NULL CHECK(quantity > 0),
    threshold INTEGER NOT NULL DEFAULT 0,
    route_key INTEGER NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ NOT NULL,
    prepared_at TIMESTAMPTZ,
    claimed_at TIMESTAMPTZ,
    UNIQUE(event_id,character_id,reward_key),
    CHECK(id BETWEEN 49152 AND 4294967295 AND (id & 49152) = 49152)
);
CREATE INDEX pallone_reward_offers_pending ON pallone_reward_offers(character_id,event_id) WHERE claimed_at IS NULL;
CREATE TABLE pallone_auto_rp_claims (
    event_id INTEGER NOT NULL REFERENCES pallone_event_finalizations(event_id),
    character_id INTEGER NOT NULL,
    quantity INTEGER NOT NULL CHECK(quantity > 0),
    applied_at TIMESTAMPTZ,
    PRIMARY KEY(event_id,character_id)
);
