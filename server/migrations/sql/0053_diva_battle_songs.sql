-- Prayer-song activations are character- and event-scoped. Do not fabricate
-- prayer participation or grant uses when forcing the battle phase.
CREATE SEQUENCE IF NOT EXISTS diva_battle_song_activation_ids AS BIGINT MINVALUE 1 MAXVALUE 4294967295 NO CYCLE;

CREATE TABLE IF NOT EXISTS diva_battle_songs (
    char_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    used_count SMALLINT NOT NULL CHECK (used_count BETWEEN 0 AND 64),
    activation_id BIGINT NOT NULL CHECK (activation_id BETWEEN 1 AND 4294967295),
    activated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (char_id,event_id)
);

-- Snapshot effect IDs at activation so changing the bead configuration cannot
-- reset finite-effect consumption. Counts reset only on a new paid activation.
CREATE TABLE IF NOT EXISTS diva_battle_song_effects (
    char_id INTEGER NOT NULL,
    event_id INTEGER NOT NULL,
    effect_id SMALLINT NOT NULL CHECK (effect_id BETWEEN 1 AND 25),
    used_count SMALLINT NOT NULL CHECK (used_count BETWEEN 0 AND 255),
    PRIMARY KEY (char_id,event_id,effect_id),
    FOREIGN KEY (char_id,event_id) REFERENCES diva_battle_songs(char_id,event_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS diva_battle_song_receipts (
    char_id INTEGER NOT NULL,
    event_id INTEGER NOT NULL,
    run_key UUID NOT NULL,
    activation_id BIGINT NOT NULL CHECK (activation_id BETWEEN 1 AND 4294967295),
    effects TEXT NOT NULL,
    quest_id INTEGER NOT NULL CHECK (quest_id BETWEEN 1 AND 65535),
    started_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (char_id,event_id,run_key),
    FOREIGN KEY (char_id,event_id) REFERENCES diva_battle_songs(char_id,event_id) ON DELETE CASCADE
);
