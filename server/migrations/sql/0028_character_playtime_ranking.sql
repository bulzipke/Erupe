-- Persist the cumulative playtime embedded in character savedata so the
-- dashboard can rank it efficiently without decompressing every save once per
-- request. NULL means an existing character has not been backfilled yet.
ALTER TABLE public.characters
    ADD COLUMN IF NOT EXISTS playtime_seconds BIGINT;

DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'characters_playtime_seconds_nonnegative'
    ) THEN
        ALTER TABLE public.characters
            ADD CONSTRAINT characters_playtime_seconds_nonnegative
            CHECK (playtime_seconds IS NULL OR playtime_seconds >= 0);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS characters_playtime_seconds_idx
    ON public.characters (playtime_seconds DESC, id ASC)
    WHERE playtime_seconds IS NOT NULL AND deleted = false;
