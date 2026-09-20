-- Keep existing GR cursor metadata and receipt keys byte-for-byte unchanged.
-- The two HR rank tiers share one sequence; HR and GR use separate tracks.
ALTER TABLE diva_prayer_rotation_progress ADD COLUMN IF NOT EXISTS track TEXT NOT NULL DEFAULT 'gr';
ALTER TABLE diva_prayer_rotation_progress DROP CONSTRAINT IF EXISTS diva_prayer_rotation_progress_pkey;
ALTER TABLE diva_prayer_rotation_progress ADD PRIMARY KEY (char_id,event_id,track);
ALTER TABLE diva_prayer_rotation_progress DROP CONSTRAINT IF EXISTS diva_prayer_rotation_progress_track_check;
ALTER TABLE diva_prayer_rotation_progress ADD CONSTRAINT diva_prayer_rotation_progress_track_check CHECK(track IN ('gr','hr'));

-- Older GR repeats did not create milestone groups. Protect both claimed and
-- pending snapshots before HR rotations can overlap their score thresholds.
-- CASE limits the integer conversion even if a foreign/manual key is malformed.
WITH legacy AS (
    SELECT char_id,event_id,
        CASE WHEN catalog_key ~ '^rotation-gr-crystals-v1-[0-9]{1,7}$'
             THEN substring(catalog_key FROM '([0-9]+)$')::BIGINT END AS sequence_index
    FROM diva_reward_receipts WHERE reward_type=1
)
INSERT INTO diva_reward_groups(char_id,event_id,reward_type,group_key,variant)
SELECT DISTINCT char_id,event_id,1,'milestone-' || (103000+1000*sequence_index)::TEXT,'gr'
FROM legacy WHERE sequence_index BETWEEN 0 AND 4294864
ON CONFLICT(char_id,event_id,reward_type,group_key) DO NOTHING;
