-- Source: https://ferias.bulzipke.com/quest/ev_hr_l.htm
-- All 25 LastAnniversary entries. Production preflight: 17 absent, 8 already
-- registered as Series (type 24), with no rotation. Publish those 8 as HR
-- events too, keeping their original player limits and marks.
-- Current quest binary hashes, embedded IDs and event frame lengths verified.
-- Native event captures give type 18, except HR transition quest 51122 (38).
-- No event captures exist for 51121/53308/53312/53313; their HR event quest
-- titles and the Ferias HR listing establish standard type 18.
-- Quest assets, rewards, rank/equipment restrictions and season flags stay.

BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
LOCK TABLE public.event_quests IN SHARE ROW EXCLUSIVE MODE;

CREATE TEMP TABLE last_anniversary_hr_requested (
    quest_id integer PRIMARY KEY,
    quest_type integer NOT NULL,
    max_players integer NOT NULL,
    mark integer NOT NULL,
    was_series boolean NOT NULL
) ON COMMIT DROP;

INSERT INTO last_anniversary_hr_requested VALUES
    (51060,18,4,1,false), -- Battle of Ganos
    (51121,18,4,1,false), -- Special fish festival
    (51122,38,4,1,false), -- Gore Magala / barrels
    (51125,18,4,0,true),  -- Which medicine is right?
    (51126,18,1,0,true),  -- Old fashion 3: preserve solo-only limit
    (51127,18,4,0,true),  -- UNKNOWN assault
    (53208,18,4,1,false), -- Escape the jungle
    (53209,18,4,1,false), -- MHF-Q
    (53220,18,4,1,false), -- Sound of Halk
    (53247,18,4,1,false), -- Blacksmith / Plesioth fishing
    (53272,18,4,1,false), -- Find the thief
    (53275,18,4,1,false), -- Find the phantom fruit
    (53278,18,4,1,false), -- Mosswine / well-done steak
    (53308,18,4,1,false), -- Ultimate weapon Gook
    (53310,18,4,1,false), -- Return the barrette
    (53311,18,4,1,false), -- Orugaron equipment / Kushala
    (53312,18,4,1,false), -- Glutton / Congalala
    (53313,18,4,1,false), -- Do-re-mi hunt
    (53315,18,4,0,true),  -- Winning team and losing team
    (53316,18,4,0,true),  -- Old fashion 1
    (53317,18,4,0,true),  -- Eating contest
    (53318,18,4,0,true),  -- Old fashion 2
    (53319,18,4,0,true),  -- Hypnoc session
    (53323,18,4,1,false), -- Block tower
    (53325,18,4,1,false); -- Halloween speedstar

-- Fail safely if another operator changed the rows since preflight.
DO $$
BEGIN
    IF (SELECT count(*) FROM public.event_quests d
        JOIN last_anniversary_hr_requested w ON w.quest_id=d.quest_id
        WHERE w.was_series) <> 8
       OR EXISTS (
           SELECT 1 FROM public.event_quests d
           JOIN last_anniversary_hr_requested w ON w.quest_id=d.quest_id
           WHERE NOT (d.quest_type=w.quest_type OR (w.was_series AND d.quest_type=24))
              OR d.max_players IS DISTINCT FROM w.max_players
              OR d.mark IS DISTINCT FROM w.mark
              OR COALESCE(d.flags,-1) <> -1
              OR COALESCE(d.active_days,0) <> 0
              OR COALESCE(d.inactive_days,0) <> 0
              OR d.collab_scope IS DISTINCT FROM ''
       ) THEN
        RAISE EXCEPTION 'HR LastAnniversary preflight changed; review before activation';
    END IF;
END $$;

UPDATE public.event_quests d
SET quest_type=w.quest_type, active_days=0, inactive_days=0
FROM last_anniversary_hr_requested w
WHERE d.quest_id=w.quest_id AND w.was_series
  AND (d.quest_type IS DISTINCT FROM w.quest_type
       OR d.active_days IS DISTINCT FROM 0
       OR d.inactive_days IS DISTINCT FROM 0)
RETURNING d.id, d.quest_id, d.quest_type, d.max_players, d.start_time;

INSERT INTO public.event_quests
    (max_players, quest_type, quest_id, mark, flags, start_time,
     active_days, inactive_days, collab_scope)
SELECT w.max_players, w.quest_type, w.quest_id, w.mark, -1,
       CURRENT_TIMESTAMP, 0, 0, ''
FROM last_anniversary_hr_requested w
WHERE NOT EXISTS (SELECT 1 FROM public.event_quests d WHERE d.quest_id=w.quest_id)
ORDER BY w.quest_id
RETURNING id, quest_id, quest_type, max_players, start_time;

DO $$
BEGIN
    IF (SELECT count(*) FROM public.event_quests d
        JOIN last_anniversary_hr_requested w ON w.quest_id=d.quest_id) <> 25
       OR EXISTS (
           SELECT 1 FROM public.event_quests d
           JOIN last_anniversary_hr_requested w ON w.quest_id=d.quest_id
           WHERE d.quest_type IS DISTINCT FROM w.quest_type
              OR d.max_players IS DISTINCT FROM w.max_players
              OR d.mark IS DISTINCT FROM w.mark
              OR COALESCE(d.flags,-1) <> -1
              OR d.active_days IS DISTINCT FROM 0
              OR d.inactive_days IS DISTINCT FROM 0
              OR d.collab_scope IS DISTINCT FROM ''
       ) THEN
        RAISE EXCEPTION 'HR LastAnniversary permanent registration verification failed';
    END IF;
END $$;

COMMIT;

SELECT id,quest_id,quest_type,max_players,mark,flags,start_time,
       active_days,inactive_days,collab_scope
FROM public.event_quests
WHERE quest_id IN (51060,51121,51122,51125,51126,51127,53208,53209,53220,
                  53247,53272,53275,53278,53308,53310,53311,53312,53313,
                  53315,53316,53317,53318,53319,53323,53325)
ORDER BY quest_id;
