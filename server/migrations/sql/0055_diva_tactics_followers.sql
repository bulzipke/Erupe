-- One character contract, not one contract per channel/guild. Moving guilds or
-- starting another event must not bypass the native 24-hour change lock.
CREATE TABLE IF NOT EXISTS diva_tactics_followers (
    char_id INTEGER PRIMARY KEY REFERENCES characters(id) ON DELETE CASCADE,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    -- Historical guild snapshot: disbanding the guild must not erase the lock.
    guild_id INTEGER NOT NULL CHECK (guild_id > 0),
    name_index SMALLINT NOT NULL CHECK (name_index BETWEEN 0 AND 39),
    voice SMALLINT NOT NULL CHECK (voice BETWEEN 0 AND 3),
    weapon SMALLINT NOT NULL CHECK (weapon BETWEEN 0 AND 1),
    strength SMALLINT NOT NULL CHECK (strength BETWEEN 0 AND 2),
    cost INTEGER NOT NULL CHECK (cost = CASE strength WHEN 0 THEN 800 WHEN 1 THEN 1300 WHEN 2 THEN 2300 END),
    hired_at TIMESTAMPTZ NOT NULL,
    available_at TIMESTAMPTZ NOT NULL,
    CHECK (EXTRACT(epoch FROM hired_at) BETWEEN 1 AND 4294880894),
    CHECK (available_at = hired_at + INTERVAL '24 hours')
);
