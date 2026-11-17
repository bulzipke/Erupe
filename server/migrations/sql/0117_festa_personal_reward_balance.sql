SELECT pg_advisory_xact_lock(1178940481::bigint);

ALTER TABLE festa_goal_rounds
    ADD COLUMN personal_goal_souls integer CHECK (personal_goal_souls > 0),
    ADD COLUMN personal_book_multiplier integer NOT NULL DEFAULT 1 CHECK (personal_book_multiplier IN (1,2)),
    ADD COLUMN fp_above_3500_rate integer NOT NULL DEFAULT 500 CHECK (fp_above_3500_rate >= 0);

-- The FP rate can change up to the first main prize preview. Published goals
-- and their prize receipts stay with the round they were issued in.
UPDATE festa_goal_rounds g
SET fp_above_3500_rate=150
FROM festa_event_lifecycle l JOIN events e ON e.id=l.event_id
WHERE l.singleton AND g.event_id=e.id AND e.festa_continuous AND g.rebalanced_fp
    AND clock_timestamp()>=e.start_time AND clock_timestamp()<e.start_time+interval '337 hours'
    AND NOT EXISTS (
        SELECT 1 FROM festa_prizes_accepted a WHERE a.event_id=e.id AND a.prize_id=0
    );
