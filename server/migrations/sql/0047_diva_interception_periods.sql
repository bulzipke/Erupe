-- Reward expiry follows the next scheduled interception, not the next prayer
-- round or the old whole-event lifetime. Forced prayer/welcome dates are not
-- evidence that another interception actually took place.
CREATE TABLE diva_interception_periods (
    event_id INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE RESTRICT,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL CHECK (ends_at > starts_at)
);
CREATE INDEX diva_interception_periods_start ON diva_interception_periods(starts_at DESC,event_id DESC);

-- Current normal/forced-interception schedules and existing round-scoped
-- submissions/receipts are trustworthy. Do not import unversioned legacy JSON.
INSERT INTO diva_interception_periods(event_id,starts_at,ends_at)
SELECT e.id,e.start_time+INTERVAL '605100 seconds',e.start_time+INTERVAL '1206000 seconds'
FROM events e WHERE e.event_type='diva'
AND e.start_time+INTERVAL '605100 seconds' <= NOW() AND (
    EXISTS(SELECT 1 FROM diva_event_lifecycle l WHERE l.event_id=e.id AND l.mode IN (-1,2))
    OR EXISTS(SELECT 1 FROM diva_interception_runs r WHERE r.event_id=e.id)
    OR EXISTS(SELECT 1 FROM diva_reward_receipts r WHERE r.event_id=e.id AND r.reward_type=6)
);
