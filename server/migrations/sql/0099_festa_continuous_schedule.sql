SELECT pg_advisory_xact_lock(1178940481);
ALTER TABLE events ADD COLUMN festa_continuous boolean NOT NULL DEFAULT false;
CREATE TABLE festa_event_lifecycle (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    event_id integer NOT NULL REFERENCES events(id)
);

-- Backfill current records without dropping registrations, souls or receipts.
ALTER TABLE festa_registrations ADD COLUMN event_id integer NOT NULL DEFAULT 0;
ALTER TABLE festa_submissions ADD COLUMN event_id integer NOT NULL DEFAULT 0;
ALTER TABLE festa_prizes_accepted ADD COLUMN event_id integer NOT NULL DEFAULT 0;
UPDATE festa_registrations SET event_id=COALESCE((SELECT id FROM events WHERE event_type='festa' ORDER BY start_time DESC,id DESC LIMIT 1),0);
UPDATE festa_submissions SET event_id=COALESCE((SELECT id FROM events WHERE event_type='festa' ORDER BY start_time DESC,id DESC LIMIT 1),0);
UPDATE festa_prizes_accepted SET event_id=COALESCE((SELECT id FROM events WHERE event_type='festa' ORDER BY start_time DESC,id DESC LIMIT 1),0);
CREATE INDEX festa_registrations_round ON festa_registrations(event_id,guild_id);
CREATE INDEX festa_submissions_round ON festa_submissions(event_id,character_id);
CREATE INDEX festa_prizes_accepted_round ON festa_prizes_accepted(event_id,character_id,prize_id);

-- Keep the currently active chapter while moving boundaries to Monday noon.
-- A running soul week must not become an already-expired reward week.
WITH current_round AS (
    SELECT e.* FROM events e WHERE event_type='festa'
    AND start_time <= now() AND start_time+INTERVAL '2458800 seconds'>now()
    ORDER BY start_time DESC,id DESC LIMIT 1
), saved AS (
    INSERT INTO rotation_schedule_adjustments(event_type,event_id,previous_schedule)
    SELECT 'festa',id,to_jsonb(e) FROM current_round e RETURNING event_id
)
UPDATE events e SET festa_continuous=true,start_time=(
    date_trunc('week',(now() AT TIME ZONE 'Asia/Seoul')-INTERVAL '12 hours')+INTERVAL '12 hours'
    - CASE WHEN now()<e.start_time+INTERVAL '7 days' THEN INTERVAL '0 days'
           WHEN now()<e.start_time+INTERVAL '14 days' THEN INTERVAL '7 days'
           ELSE INTERVAL '14 days' END) AT TIME ZONE 'Asia/Seoul'
FROM saved s WHERE e.id=s.event_id;

INSERT INTO festa_event_lifecycle(singleton,event_id)
SELECT true,id FROM events WHERE event_type='festa' AND festa_continuous
ORDER BY start_time DESC,id DESC LIMIT 1;
