-- Persistent state for the guild target system.
--
-- A guild may work on only one target at a time, while effects from multiple
-- completed targets may overlap until their individual expiry times.
CREATE TABLE IF NOT EXISTS guild_mission_runs (
    id BIGSERIAL PRIMARY KEY,
    guild_id BIGINT NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    mission_id BIGINT NOT NULL CHECK (mission_id BETWEEN 0 AND 4294967295),

    -- Snapshot the advertised definition so an in-progress target does not
    -- change if the server's daily mission list is updated later.
    target_type INTEGER NOT NULL CHECK (target_type BETWEEN 0 AND 65535),
    target_id INTEGER NOT NULL CHECK (target_id BETWEEN 0 AND 65535),
    required_count INTEGER NOT NULL CHECK (required_count BETWEEN 1 AND 65535),
    skip_tickets INTEGER NOT NULL CHECK (skip_tickets BETWEEN 0 AND 65535),
    progress_per_exchange INTEGER NOT NULL
        CHECK (progress_per_exchange BETWEEN 1 AND 65535),
    cancel_ticket_cost INTEGER NOT NULL
        CHECK (cancel_ticket_cost BETWEEN 0 AND 65535),
    is_gr BOOLEAN NOT NULL,
    reward_type INTEGER NOT NULL CHECK (reward_type BETWEEN 0 AND 65535),
    reward_level INTEGER NOT NULL CHECK (reward_level BETWEEN 0 AND 65535),

    progress INTEGER NOT NULL DEFAULT 0
        CHECK (progress >= 0 AND progress <= required_count),
    state TEXT NOT NULL DEFAULT 'active'
        CHECK (state IN ('active', 'completed', 'cancelled', 'expired')),

    set_by INTEGER REFERENCES characters(id) ON DELETE SET NULL,
    completed_by INTEGER REFERENCES characters(id) ON DELETE SET NULL,
    cancelled_by INTEGER REFERENCES characters(id) ON DELETE SET NULL,

    set_at TIMESTAMPTZ NOT NULL,
    target_expires_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    effect_expires_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS guild_mission_one_active_target
    ON guild_mission_runs (guild_id)
    WHERE state = 'active';

CREATE INDEX IF NOT EXISTS guild_mission_active_effects
    ON guild_mission_runs (guild_id, effect_expires_at)
    WHERE state = 'completed';

CREATE TABLE IF NOT EXISTS guild_mission_contributions (
    mission_run_id BIGINT NOT NULL
        REFERENCES guild_mission_runs(id) ON DELETE CASCADE,
    character_id INTEGER NOT NULL
        REFERENCES characters(id) ON DELETE CASCADE,
    amount INTEGER NOT NULL DEFAULT 0 CHECK (amount >= 0),
    first_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (mission_run_id, character_id)
);
