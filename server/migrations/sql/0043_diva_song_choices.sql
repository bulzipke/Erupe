-- Native daily response: initial/carried color, then changed color.
CREATE TABLE diva_song_choices (
    char_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    day_start TIMESTAMPTZ NOT NULL,
    first_color SMALLINT NOT NULL CHECK (first_color BETWEEN 1 AND 4),
    second_color SMALLINT NOT NULL DEFAULT 0 CHECK (second_color BETWEEN 0 AND 4),
    PRIMARY KEY (char_id, event_id, day_start),
    CHECK (first_color <> second_color)
);

-- Legacy records did not distinguish initial selection from a daily change.
-- Preserve the last selection, without inventing a consumed change right.
INSERT INTO diva_song_choices(char_id,event_id,day_start,first_color)
SELECT DISTINCT ON (a.character_id,e.id,a.expiry)
    a.character_id,e.id,a.expiry-interval '1 day',a.bead_index
FROM diva_beads_assignment a
JOIN events e ON e.event_type='diva'
    AND a.expiry > e.start_time AND a.expiry <= e.start_time+interval '8 days'
WHERE a.bead_index BETWEEN 1 AND 4
ORDER BY a.character_id,e.id,a.expiry,a.id DESC;
