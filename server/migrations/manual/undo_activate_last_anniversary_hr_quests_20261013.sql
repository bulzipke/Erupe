-- Undo the HR LastAnniversary production change of 2026-10-13.
-- Restore the 8 existing Series registrations to their original type and
-- NULL rotation fields; remove only the 17 newly inserted registrations.
-- Quest assets/player records remain intact. Do not rewind the ID sequence.
-- Refuse to overwrite later operator changes.

BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
LOCK TABLE public.event_quests IN SHARE ROW EXCLUSIVE MODE;

CREATE TEMP TABLE last_anniversary_hr_original (
    id integer PRIMARY KEY,
    quest_id integer NOT NULL,
    max_players integer NOT NULL
) ON COMMIT DROP;
INSERT INTO last_anniversary_hr_original VALUES
    (32,51125,4), (33,51126,1), (34,51127,4), (44,53315,4),
    (45,53316,4), (46,53317,4), (47,53318,4), (48,53319,4);

CREATE TEMP TABLE last_anniversary_hr_inserted (
    id integer PRIMARY KEY,
    quest_id integer NOT NULL,
    quest_type integer NOT NULL
) ON COMMIT DROP;
INSERT INTO last_anniversary_hr_inserted VALUES
    (315,51060,18), (316,51121,18), (317,51122,38),
    (318,53208,18), (319,53209,18), (320,53220,18), (321,53247,18),
    (322,53272,18), (323,53275,18), (324,53278,18), (325,53308,18),
    (326,53310,18), (327,53311,18), (328,53312,18), (329,53313,18),
    (330,53323,18), (331,53325,18);

DO $$
BEGIN
    IF (SELECT count(*) FROM public.event_quests d
        JOIN last_anniversary_hr_original w ON w.id=d.id) <> 8
       OR EXISTS (
           SELECT 1 FROM public.event_quests d
           JOIN last_anniversary_hr_original w ON w.id=d.id
           WHERE d.quest_id IS DISTINCT FROM w.quest_id
              OR d.max_players IS DISTINCT FROM w.max_players
              OR d.mark IS DISTINCT FROM 0 OR d.flags IS NOT NULL
              OR d.collab_scope IS DISTINCT FROM ''
              OR d.start_time IS DISTINCT FROM
                 TIMESTAMPTZ '2026-05-28 01:32:22.830588+09'
              OR ((d.quest_type=18 AND d.active_days=0 AND d.inactive_days=0)
                  OR (d.quest_type=24 AND d.active_days IS NULL AND d.inactive_days IS NULL)) IS NOT TRUE
       )
       OR EXISTS (
           SELECT 1 FROM public.event_quests d
           JOIN last_anniversary_hr_inserted w ON w.id=d.id
           WHERE d.quest_id IS DISTINCT FROM w.quest_id
              OR d.quest_type IS DISTINCT FROM w.quest_type
              OR d.max_players IS DISTINCT FROM 4
              OR d.mark IS DISTINCT FROM 1 OR d.flags IS DISTINCT FROM -1
              OR d.active_days IS DISTINCT FROM 0
              OR d.inactive_days IS DISTINCT FROM 0
              OR d.collab_scope IS DISTINCT FROM ''
              OR d.start_time IS DISTINCT FROM
                 TIMESTAMPTZ '2026-10-13 23:49:41.596586+09'
       ) THEN
        RAISE EXCEPTION 'HR LastAnniversary rows changed after activation; review before undo';
    END IF;
END $$;

UPDATE public.event_quests d
SET quest_type=24, active_days=NULL, inactive_days=NULL
FROM last_anniversary_hr_original w
WHERE d.id=w.id AND d.quest_id=w.quest_id
  AND (d.quest_type IS DISTINCT FROM 24
       OR d.active_days IS NOT NULL OR d.inactive_days IS NOT NULL)
RETURNING d.id,d.quest_id,d.quest_type;

DELETE FROM public.event_quests d
USING last_anniversary_hr_inserted w
WHERE d.id=w.id AND d.quest_id=w.quest_id
RETURNING d.id,d.quest_id;

COMMIT;
