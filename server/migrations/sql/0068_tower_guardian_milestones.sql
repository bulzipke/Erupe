-- D444: G10 POST_TOWER_INFO 6 consumes the poster's milestone on departure.
-- No kill-based backfill: defeats (including guest/rare encounters) do not prove
-- that the hunter posted a milestone quest. Existing floor records are retained.
ALTER TABLE tower ADD COLUMN IF NOT EXISTS guardian1 integer NOT NULL DEFAULT 0
    CHECK (guardian1 BETWEEN 0 AND 1000);
ALTER TABLE tower ADD COLUMN IF NOT EXISTS guardian2 integer NOT NULL DEFAULT 0
    CHECK (guardian2 BETWEEN 0 AND 1000);
