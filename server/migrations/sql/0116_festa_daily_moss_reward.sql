-- Retire the older pass quest while keeping its row identity. A negative
-- inactive_days with positive active_days is an inactive legacy schedule.
UPDATE event_quests SET active_days=1,inactive_days=-1 WHERE quest_type=26 AND quest_id=53034;
UPDATE festa_trials SET reward=0 WHERE objective=5 AND goal_id=53034 AND reward<>0;
