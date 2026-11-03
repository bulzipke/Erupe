-- Immutable per-event selection: four UI colors, drawn only at rollover.
CREATE TABLE IF NOT EXISTS public.diva_bead_rounds (
    event_id integer PRIMARY KEY REFERENCES public.events(id) ON DELETE CASCADE,
    mode integer NOT NULL CHECK(mode IN (-1,1,2,3)),
    effects integer[] NOT NULL CHECK(
        array_ndims(effects)=1 AND array_lower(effects,1)=1
        AND cardinality(effects) BETWEEN 1 AND 4
        AND array_position(effects,NULL) IS NULL
        AND effects <@ ARRAY[1,3,4,8,9,10,11,14,15,17,18,19,20,21,22,23,24,25]
        AND effects[1]<>effects[2] AND effects[1]<>effects[3] AND effects[1]<>effects[4]
        AND effects[2]<>effects[3] AND effects[2]<>effects[4] AND effects[3]<>effects[4]),
    support_absence integer[] NOT NULL CHECK(
        array_ndims(support_absence)=1 AND array_lower(support_absence,1)=1
        AND cardinality(support_absence)=13 AND array_position(support_absence,NULL) IS NULL
        AND 0<=ALL(support_absence) AND 36>=ALL(support_absence)),
    same_count smallint NOT NULL CHECK(same_count BETWEEN 1 AND 2),
    rotation_version smallint NOT NULL CHECK(rotation_version IN (0,1)),
    seed bigint,
    previous_event_id integer REFERENCES public.events(id) ON DELETE SET NULL,
    CHECK((rotation_version=0 AND seed IS NULL)
       OR (rotation_version=1 AND cardinality(effects)=4 AND seed IS NOT NULL AND seed>=0))
);

-- Freeze pre-upgrade events, including a currently running or scheduled one.
-- A saved watermark prevents re-executing this SQL from freezing later events
-- with the legacy configuration. Existing snapshots are never overwritten.
CREATE TABLE IF NOT EXISTS public.diva_bead_rotation_bootstrap (
    singleton boolean PRIMARY KEY CHECK(singleton),
    max_event_id integer NOT NULL
);
INSERT INTO public.diva_bead_rotation_bootstrap(singleton,max_event_id)
SELECT TRUE,COALESCE(MAX(id),0) FROM public.events WHERE event_type='diva'
ON CONFLICT(singleton) DO NOTHING;

WITH legacy AS (
    SELECT COALESCE(array_agg(type ORDER BY id),ARRAY[1,3,4,8]) effects
    FROM (SELECT id,type FROM public.diva_beads ORDER BY id LIMIT 4) b
)
INSERT INTO public.diva_bead_rounds(event_id,mode,effects,support_absence,same_count,rotation_version)
SELECT e.id,CASE WHEN EXISTS(SELECT 1 FROM public.diva_debug_song_event d WHERE d.event_id=e.id)
    THEN 1 ELSE COALESCE((SELECT l.mode FROM public.diva_event_lifecycle l
    WHERE l.event_id=e.id ORDER BY l.mode LIMIT 1),-1) END,
    b.effects,array_fill(0,ARRAY[13]),1,0
FROM public.events e CROSS JOIN legacy b
WHERE e.event_type='diva' AND e.id <= (SELECT max_event_id FROM public.diva_bead_rotation_bootstrap WHERE singleton)
ON CONFLICT(event_id) DO NOTHING;
