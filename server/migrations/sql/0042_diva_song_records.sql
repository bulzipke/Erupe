-- Record each submission immediately; rankings can apply a publication cutoff.
CREATE TABLE diva_song_records (
    id BIGSERIAL PRIMARY KEY,
    char_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    day_start TIMESTAMPTZ NOT NULL,
    bead_index INTEGER NOT NULL CHECK (bead_index BETWEEN 0 AND 4),
    quest_points BIGINT NOT NULL CHECK (quest_points >= 0),
    bonus_points BIGINT NOT NULL CHECK (bonus_points >= 0),
    submitted_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX diva_song_records_event_time ON diva_song_records(event_id, submitted_at);
CREATE INDEX diva_song_records_character_day ON diva_song_records(char_id, event_id, day_start);
CREATE INDEX diva_beads_assignment_character_expiry ON diva_beads_assignment(character_id, expiry);

-- Anchor forced phase 1 across reconnects, server restarts and midnight.
CREATE TABLE diva_debug_song_event (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE
);

-- Repair known one-byte colors misread as u16. Do not invent any points.
UPDATE diva_beads_assignment SET bead_index = bead_index / 256
WHERE bead_index IN (256, 512, 768, 1024);
-- Legacy expiry = selection time + 24h. Convert it to the next UTC+9 noon.
UPDATE diva_beads_assignment SET expiry =
    (date_trunc('day', (expiry AT TIME ZONE 'Asia/Seoul') - interval '36 hours')
     + interval '36 hours') AT TIME ZONE 'Asia/Seoul';
