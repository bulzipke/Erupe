-- Source: https://ferias.bulzipke.com/quest/ev_gr_l.htm
-- All 12 rows whose event heading contains the exact text LastAnniversary.
-- Production preflight (2026-10-13): all twelve absent from event_quests;
-- all current quest binaries verified against the decoded inventory.
-- Existing event captures give max_players=4, mark=1, quest_type=28/43.
-- 56097/56098 have no event capture; their quest bodies confirm Zenith.
-- Zero active/inactive days disable rotation in handleMsgMhfEnumerateQuest.
-- Flags=-1 preserves the time/season flags embedded in each quest file.

BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
LOCK TABLE public.event_quests IN SHARE ROW EXCLUSIVE MODE;

CREATE TEMP TABLE last_anniversary_requested (
    quest_id integer PRIMARY KEY,
    quest_type integer NOT NULL
) ON COMMIT DROP;

INSERT INTO last_anniversary_requested (quest_id, quest_type) VALUES
    (55729, 28), -- Rescue the esoteric research team
    (55730, 28), -- Swamp poison
    (55731, 43), -- Zenith Gasurabazura
    (55732, 28), -- Become Brachydios
    (56097, 43), -- Zenith Doragyurosu invitation
    (56098, 43), -- Zenith Daimyo Hermitaur invitation
    (56105, 43), -- Zenith Rathalos / fish wyvern spirit
    (56106, 28), -- Retake the barrette collection
    (56109, 28), -- Edel Halk
    (56110, 28), -- Urgent Espinas parts
    (56112, 43), -- Summer Bogabadorumu festival
    (56115, 28); -- Urgent Elzelion parts

INSERT INTO public.event_quests
    (max_players, quest_type, quest_id, mark, flags, start_time,
     active_days, inactive_days, collab_scope)
SELECT 4, w.quest_type, w.quest_id, 1, -1, CURRENT_TIMESTAMP, 0, 0, ''
FROM last_anniversary_requested w
WHERE NOT EXISTS (
    SELECT 1 FROM public.event_quests d WHERE d.quest_id = w.quest_id
)
ORDER BY w.quest_id
RETURNING id, quest_id, quest_type, start_time, active_days, inactive_days;

-- Do not silently overwrite someone else's differing registration.
DO $$
BEGIN
    IF (SELECT count(*) FROM public.event_quests d
        JOIN last_anniversary_requested w ON w.quest_id = d.quest_id) <> 12
       OR EXISTS (
           SELECT 1 FROM public.event_quests d
           JOIN last_anniversary_requested w ON w.quest_id = d.quest_id
           WHERE d.quest_type IS DISTINCT FROM w.quest_type
              OR d.max_players IS DISTINCT FROM 4
              OR d.mark IS DISTINCT FROM 1
              OR d.flags IS DISTINCT FROM -1
              OR d.active_days IS DISTINCT FROM 0
              OR d.inactive_days IS DISTINCT FROM 0
              OR d.collab_scope IS DISTINCT FROM ''
       ) THEN
        RAISE EXCEPTION 'LastAnniversary registration differs from verified permanent rows';
    END IF;
END $$;

COMMIT;

SELECT id, quest_id, quest_type, max_players, mark, flags,
       start_time, active_days, inactive_days, collab_scope
FROM public.event_quests
WHERE quest_id IN (55729,55730,55731,55732,56097,56098,
                  56105,56106,56109,56110,56112,56115)
ORDER BY quest_id;
