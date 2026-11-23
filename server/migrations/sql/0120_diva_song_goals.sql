CREATE TABLE diva_song_goal_rounds (
    event_id integer PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    previous_event_id integer REFERENCES events(id) ON DELETE SET NULL,
    previous_accounts bigint NOT NULL CHECK (previous_accounts >= 0),
    previous_points bigint NOT NULL CHECK (previous_points >= 0),
    targets bigint[] NOT NULL CHECK (
        array_ndims(targets) = 1 AND array_lower(targets, 1) = 1 AND cardinality(targets) = 25
        AND array_position(targets, NULL) IS NULL AND 0 < ALL(targets)
    )
);
