-- Permanent all-user draw totals. Box/step progress is resettable and is not
-- historical play data, so existing progress is deliberately not backfilled.
-- No catalog FK: removing a shop must not erase its accumulated popularity.
CREATE TABLE gacha_play_totals (
    gacha_id integer PRIMARY KEY CHECK (gacha_id > 0),
    play_count bigint NOT NULL CHECK (play_count > 0),
    draw_count bigint NOT NULL CHECK (draw_count > 0),
    first_played_at timestamptz NOT NULL DEFAULT now(),
    last_played_at timestamptz NOT NULL DEFAULT now()
);
