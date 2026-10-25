-- Optional versioned settings from the same native quest result as the time.
-- NULL means an older client / old record; never backfill guessed OFF values.
ALTER TABLE public.monster_hunt_records
    ADD COLUMN IF NOT EXISTS client_settings jsonb;
COMMENT ON COLUMN public.monster_hunt_records.client_settings IS
    'VUI1 settings frozen at quest end, replaced atomically only by a faster personal best. Raviente unchanged.';
