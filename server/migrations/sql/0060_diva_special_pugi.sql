-- The special guild hall has its own three Poogies. Never copy, reset, or
-- replace the ordinary guild hall's clothing when a Diva round changes.
ALTER TABLE guilds
    ADD COLUMN IF NOT EXISTS diva_pugi_outfit_1 SMALLINT NOT NULL DEFAULT 0 CHECK (diva_pugi_outfit_1 BETWEEN 0 AND 9),
    ADD COLUMN IF NOT EXISTS diva_pugi_outfit_2 SMALLINT NOT NULL DEFAULT 0 CHECK (diva_pugi_outfit_2 BETWEEN 0 AND 9),
    ADD COLUMN IF NOT EXISTS diva_pugi_outfit_3 SMALLINT NOT NULL DEFAULT 0 CHECK (diva_pugi_outfit_3 BETWEEN 0 AND 9);
