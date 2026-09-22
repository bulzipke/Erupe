-- Random geometry is a new server-custom version, not a retail restoration.
-- Do not rewrite existing map JSON, points, departures, awards or receipts.
-- Actual interception starts (not activation overrides) after this permanent
-- cutover opt in. A running round with no map queries yet stays custom-v1.
CREATE TABLE IF NOT EXISTS diva_random_map_cutover (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    installed_at TIMESTAMPTZ NOT NULL
);
INSERT INTO diva_random_map_cutover(singleton,installed_at)
VALUES(TRUE,clock_timestamp()) ON CONFLICT(singleton) DO NOTHING;

ALTER TABLE diva_map_events
ADD COLUMN IF NOT EXISTS generation_seed BIGINT NOT NULL DEFAULT 0;
ALTER TABLE diva_map_events
DROP CONSTRAINT IF EXISTS diva_map_events_rules_version_check;
ALTER TABLE diva_map_events
ADD CONSTRAINT diva_map_events_rules_version_check
CHECK(rules_version IN ('custom-v1','custom-random-v2'));
ALTER TABLE diva_map_events
DROP CONSTRAINT IF EXISTS diva_map_events_generation_seed_check;
ALTER TABLE diva_map_events
ADD CONSTRAINT diva_map_events_generation_seed_check
CHECK((rules_version='custom-v1' AND generation_seed=0)
   OR (rules_version='custom-random-v2' AND generation_seed>0));

-- Historical branch membership is resolved from its immutable map snapshot.
CREATE INDEX IF NOT EXISTS diva_map_snapshots_map_history
ON diva_map_snapshots(event_id,guild_id,map_number,settled_at DESC);
