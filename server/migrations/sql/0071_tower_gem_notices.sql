CREATE TABLE IF NOT EXISTS tower_gem_notices (
 character_id integer PRIMARY KEY REFERENCES characters(id) ON DELETE CASCADE,
 last_read_id bigint NOT NULL DEFAULT 0 CHECK(last_read_id >= 0)
);
