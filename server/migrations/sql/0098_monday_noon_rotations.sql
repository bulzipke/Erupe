-- One-time schedule correction. Retain IDs, points, progress and receipts;
-- store the previous dates so an operator can inspect/reverse the correction.
SELECT pg_advisory_xact_lock(1146507841);
SELECT pg_advisory_xact_lock(1414485829);
-- Allow exploration to fill the complete cycle without a rest week.
ALTER TABLE tower_event_lifecycle DROP CONSTRAINT IF EXISTS tower_event_lifecycle_check;
ALTER TABLE tower_event_lifecycle ADD CONSTRAINT tower_event_lifecycle_check
    CHECK(cycle_days >= active_days AND cycle_days <= 365);
CREATE TABLE rotation_schedule_adjustments (
    event_type text NOT NULL,
    event_id integer NOT NULL,
    previous_schedule jsonb NOT NULL,
    adjusted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(event_type,event_id)
);

WITH candidate AS (
    SELECT e.* FROM events e
    WHERE e.event_type='diva'
      AND NOT EXISTS(SELECT 1 FROM diva_debug_song_event d WHERE d.event_id=e.id)
      AND NOT EXISTS(SELECT 1 FROM diva_event_lifecycle l WHERE l.event_id=e.id AND l.mode IN (2,3))
      AND (e.id=(SELECT event_id FROM diva_event_lifecycle WHERE mode=-1)
           OR NOT EXISTS(SELECT 1 FROM diva_event_lifecycle WHERE mode=-1))
      AND e.start_time <= now()
      AND e.start_time + CASE WHEN e.diva_continuous THEN INTERVAL '21 days' ELSE INTERVAL '1810800 seconds' END > now()
    ORDER BY e.start_time DESC,e.id DESC LIMIT 1
), saved AS (
    INSERT INTO rotation_schedule_adjustments(event_type,event_id,previous_schedule)
    SELECT 'diva',e.id,jsonb_build_object('event',to_jsonb(e),'interception',
        (SELECT to_jsonb(p) FROM diva_interception_periods p WHERE p.event_id=e.id)) FROM candidate e
    RETURNING event_id
)
UPDATE events e SET start_time=(
    date_trunc('week',(e.start_time AT TIME ZONE 'Asia/Seoul') - INTERVAL '12 hours')
    + INTERVAL '12 hours') AT TIME ZONE 'Asia/Seoul', diva_continuous=true
FROM saved s WHERE e.id=s.event_id;

UPDATE diva_interception_periods p SET starts_at=e.start_time+INTERVAL '7 days',ends_at=e.start_time+INTERVAL '14 days'
FROM events e JOIN rotation_schedule_adjustments a ON a.event_type='diva' AND a.event_id=e.id
WHERE p.event_id=e.id;

INSERT INTO rotation_schedule_adjustments(event_type,event_id,previous_schedule)
SELECT 'tower',e.earth_id,jsonb_build_object('event',to_jsonb(e),'lifecycle',to_jsonb(l))
FROM tower_event_lifecycle l JOIN tower_events e USING(earth_id);

UPDATE tower_events e SET
    starts_at=(date_trunc('week',(now() AT TIME ZONE 'Asia/Seoul')-INTERVAL '12 hours')+INTERVAL '12 hours') AT TIME ZONE 'Asia/Seoul',
    ends_at=((date_trunc('week',(now() AT TIME ZONE 'Asia/Seoul')-INTERVAL '12 hours')+INTERVAL '12 hours') AT TIME ZONE 'Asia/Seoul')+INTERVAL '21 days',
    claim_until=((date_trunc('week',(now() AT TIME ZONE 'Asia/Seoul')-INTERVAL '12 hours')+INTERVAL '12 hours') AT TIME ZONE 'Asia/Seoul')+INTERVAL '42 days'
FROM tower_event_lifecycle l WHERE e.earth_id=l.earth_id;

UPDATE tower_event_lifecycle l SET anchor=e.starts_at,active_days=21,cycle_days=21
FROM tower_events e WHERE e.earth_id=l.earth_id;
