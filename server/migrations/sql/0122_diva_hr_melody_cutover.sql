-- Rounds that have reached their prayer phase retain their existing HR caps.
-- Retrying the migration preserves the first installation timestamp.
CREATE TABLE IF NOT EXISTS diva_hr_melody_cutover (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
    installed_at TIMESTAMPTZ NOT NULL
);
INSERT INTO diva_hr_melody_cutover VALUES(TRUE,clock_timestamp())
ON CONFLICT(singleton) DO NOTHING;
