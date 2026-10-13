-- Undo only the 12 registrations inserted by the 2026-10-13 production run.
-- They did not exist before activation. Quest files and player records stay.
-- Refuse to remove a registration subsequently changed by another operator.
-- Do not rewind event_quests_id_seq.

BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
LOCK TABLE public.event_quests IN SHARE ROW EXCLUSIVE MODE;

CREATE TEMP TABLE last_anniversary_inserted (
    id integer PRIMARY KEY,
    quest_id integer NOT NULL,
    quest_type integer NOT NULL
) ON COMMIT DROP;

INSERT INTO last_anniversary_inserted VALUES
    (303,55729,28), (304,55730,28), (305,55731,43), (306,55732,28),
    (307,56097,43), (308,56098,43), (309,56105,43), (310,56106,28),
    (311,56109,28), (312,56110,28), (313,56112,43), (314,56115,28);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.event_quests d
        JOIN last_anniversary_inserted w ON w.id = d.id
        WHERE d.quest_id IS DISTINCT FROM w.quest_id
           OR d.quest_type IS DISTINCT FROM w.quest_type
           OR d.max_players IS DISTINCT FROM 4
           OR d.mark IS DISTINCT FROM 1
           OR d.flags IS DISTINCT FROM -1
           OR d.active_days IS DISTINCT FROM 0
           OR d.inactive_days IS DISTINCT FROM 0
           OR d.collab_scope IS DISTINCT FROM ''
           OR d.start_time IS DISTINCT FROM
              TIMESTAMPTZ '2026-10-13 23:43:53.810104+09'
    ) THEN
        RAISE EXCEPTION 'LastAnniversary registration changed after activation; review before undo';
    END IF;
END $$;

DELETE FROM public.event_quests d
USING last_anniversary_inserted w
WHERE d.id = w.id AND d.quest_id = w.quest_id
RETURNING d.id, d.quest_id;

COMMIT;
