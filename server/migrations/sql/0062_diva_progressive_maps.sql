-- Operator-approved escalating server-custom map rules. Never convert existing
-- events or rewrite map JSON, scores, area awards, or prize receipts.
CREATE TABLE IF NOT EXISTS diva_progressive_map_cutover (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
    installed_at TIMESTAMPTZ NOT NULL
);
INSERT INTO diva_progressive_map_cutover VALUES(TRUE,clock_timestamp())
ON CONFLICT(singleton) DO NOTHING;

ALTER TABLE diva_map_events DROP CONSTRAINT IF EXISTS diva_map_events_rules_version_check;
ALTER TABLE diva_map_events ADD CONSTRAINT diva_map_events_rules_version_check
CHECK(rules_version IN ('custom-v1','custom-random-v2','custom-progressive-v3'));
ALTER TABLE diva_map_events DROP CONSTRAINT IF EXISTS diva_map_events_generation_seed_check;
ALTER TABLE diva_map_events ADD CONSTRAINT diva_map_events_generation_seed_check
CHECK((rules_version='custom-v1' AND generation_seed=0)
   OR (rules_version IN ('custom-random-v2','custom-progressive-v3') AND generation_seed>0));

-- v3's progressive color selection is part of its immutable rules, not a
-- runtime-config override. Preserve every pre-existing presentation choice.
ALTER TABLE diva_map_events DROP CONSTRAINT IF EXISTS diva_map_events_red_treasure_mode_check;
ALTER TABLE diva_map_events ADD CONSTRAINT diva_map_events_red_treasure_mode_check
CHECK((rules_version='custom-v1' AND red_treasure_mode='off')
   OR (rules_version='custom-random-v2' AND red_treasure_mode IN ('off','random-one','all'))
   OR (rules_version='custom-progressive-v3' AND red_treasure_mode='progressive'));

CREATE TABLE IF NOT EXISTS diva_map_invasions (
    event_id INTEGER NOT NULL,
    guild_id BIGINT NOT NULL,
    happened_at TIMESTAMPTZ NOT NULL,
    map_number INTEGER NOT NULL CHECK(map_number BETWEEN 1 AND 65535),
    page_tick INTEGER NOT NULL CHECK(page_tick BETWEEN 1 AND 168),
    changed_nodes INTEGER NOT NULL CHECK(changed_nodes BETWEEN 1 AND 20),
    PRIMARY KEY(event_id,guild_id,happened_at),
    UNIQUE(event_id,guild_id,map_number,page_tick),
    FOREIGN KEY(event_id,guild_id) REFERENCES diva_map_guilds(event_id,guild_id) ON DELETE RESTRICT
);
