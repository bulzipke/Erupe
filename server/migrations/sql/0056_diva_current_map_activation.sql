-- Opt exactly one currently running legacy interception into map-only progress.
-- Nothing is copied from personal totals, old area JSON, or earlier quests.
-- Persist even a no-op decision so replaying this migration cannot opt a later
-- round in. Normal post-cutover rounds keep their original eligibility.
CREATE TABLE IF NOT EXISTS diva_map_activation_decision (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
    decided_at TIMESTAMPTZ NOT NULL,
    event_id INTEGER REFERENCES events(id) ON DELETE RESTRICT
);
CREATE TABLE IF NOT EXISTS diva_map_activations (
    event_id INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE RESTRICT,
    activated_at TIMESTAMPTZ NOT NULL
);

WITH decision_clock AS (SELECT clock_timestamp() AS decided_at),
current_period AS (
    SELECT p.event_id,p.starts_at FROM diva_interception_periods p
    JOIN events e ON e.id=p.event_id AND e.event_type='diva'
    CROSS JOIN decision_clock d
    WHERE p.starts_at<=d.decided_at AND d.decided_at<p.ends_at
    ORDER BY p.starts_at DESC,p.event_id DESC LIMIT 1
)
INSERT INTO diva_map_activation_decision(singleton,decided_at,event_id)
SELECT TRUE,d.decided_at,
    (SELECT p.event_id FROM current_period p CROSS JOIN diva_map_cutover c
     WHERE NOT EXISTS(SELECT 1 FROM diva_map_events m WHERE m.event_id=p.event_id)
       AND (p.starts_at<=c.installed_at
            OR EXISTS(SELECT 1 FROM diva_map_legacy_events l WHERE l.event_id=p.event_id)
            OR EXISTS(SELECT 1 FROM diva_interception_legacy_events l WHERE l.event_id=p.event_id)))
FROM decision_clock d
ON CONFLICT(singleton) DO NOTHING;

INSERT INTO diva_map_activations(event_id,activated_at)
SELECT event_id,decided_at FROM diva_map_activation_decision WHERE event_id IS NOT NULL
ON CONFLICT(event_id) DO NOTHING;
