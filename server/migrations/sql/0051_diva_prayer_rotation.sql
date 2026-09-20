-- Issued sequence, not claimed sequence. One cursor per character/round prevents
-- schedule changes silently reissuing rewards for the same score.
CREATE TABLE IF NOT EXISTS diva_prayer_rotation_progress (
    char_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    schedule_key TEXT NOT NULL,
    schedule_fingerprint TEXT NOT NULL,
    next_index BIGINT NOT NULL DEFAULT 0 CHECK (next_index BETWEEN 0 AND 4294967295),
    PRIMARY KEY (char_id, event_id)
);
