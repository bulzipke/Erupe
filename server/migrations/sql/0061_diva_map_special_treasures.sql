-- Optional presentation policy only. Existing maps/rewards remain unchanged.
CREATE TABLE IF NOT EXISTS diva_map_special_policy (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
    mode TEXT NOT NULL CHECK(mode IN ('off','random-one','all')),
    effective_at TIMESTAMPTZ NOT NULL
);
INSERT INTO diva_map_special_policy(singleton,mode,effective_at)
VALUES(TRUE,'off',clock_timestamp()) ON CONFLICT(singleton) DO NOTHING;

-- First map lookup may happen after a setting change. Preserve the policy that
-- was active at the real round start, not whichever setting is newest then.
CREATE TABLE IF NOT EXISTS diva_map_special_policy_history (
    id BIGSERIAL PRIMARY KEY,
    mode TEXT NOT NULL CHECK(mode IN ('off','random-one','all')),
    effective_at TIMESTAMPTZ NOT NULL
);
INSERT INTO diva_map_special_policy_history(mode,effective_at)
SELECT mode,effective_at FROM diva_map_special_policy
WHERE NOT EXISTS(SELECT 1 FROM diva_map_special_policy_history);
CREATE INDEX IF NOT EXISTS diva_map_special_policy_history_effective
ON diva_map_special_policy_history(effective_at DESC,id DESC);

ALTER TABLE diva_map_events ADD COLUMN IF NOT EXISTS red_treasure_mode TEXT NOT NULL DEFAULT 'off';
ALTER TABLE diva_map_events DROP CONSTRAINT IF EXISTS diva_map_events_red_treasure_mode_check;
ALTER TABLE diva_map_events ADD CONSTRAINT diva_map_events_red_treasure_mode_check
CHECK(red_treasure_mode IN ('off','random-one','all')
      AND (red_treasure_mode='off' OR rules_version='custom-random-v2'));

CREATE OR REPLACE FUNCTION diva_map_preserve_special_policy() RETURNS trigger AS $$
BEGIN
    IF NEW.red_treasure_mode IS DISTINCT FROM OLD.red_treasure_mode THEN
        RAISE EXCEPTION 'Diva round treasure presentation is immutable' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS diva_map_preserve_special_policy ON diva_map_events;
CREATE TRIGGER diva_map_preserve_special_policy
BEFORE UPDATE OF red_treasure_mode ON diva_map_events
FOR EACH ROW EXECUTE FUNCTION diva_map_preserve_special_policy();
