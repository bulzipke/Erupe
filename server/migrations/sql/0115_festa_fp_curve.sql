SELECT pg_advisory_xact_lock(1178940481::bigint);

ALTER TABLE festa_goal_rounds ADD COLUMN rebalanced_fp boolean NOT NULL DEFAULT false;

-- The current round can adopt the FP curve until main prize previews open.
-- Goal cutoffs and previously claimed goal prizes are independent of this flag.

UPDATE festa_goal_rounds g
SET rebalanced_fp=true
FROM festa_event_lifecycle l JOIN events e ON e.id=l.event_id
WHERE l.singleton AND g.event_id=e.id AND e.festa_continuous
    AND clock_timestamp()>=e.start_time AND clock_timestamp()<e.start_time+interval '337 hours'
    AND NOT EXISTS (
        SELECT 1 FROM festa_prizes_accepted a WHERE a.event_id=e.id AND a.prize_id=0
    );
