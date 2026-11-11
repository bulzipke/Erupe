CREATE TABLE guild_alliance_applications (
    guild_id integer PRIMARY KEY REFERENCES guilds (id) ON DELETE CASCADE,
    alliance_id integer NOT NULL REFERENCES guild_alliances (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX guild_alliance_applications_alliance_idx
    ON guild_alliance_applications (alliance_id, created_at, guild_id);
