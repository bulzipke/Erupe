-- MSG_MHF_POST_TINY_BIN 0/1/1/1: the client's Tower daily-mission progress
-- (32 bytes: timetable end, six mission counters, cleared-mission bits). The
-- client resets it itself when the noon timetable changes; the server stores
-- it verbatim so the Tower Status daily reward page survives relogging.
CREATE TABLE IF NOT EXISTS tower_daily_bins (
    character_id integer PRIMARY KEY,
    data bytea NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
