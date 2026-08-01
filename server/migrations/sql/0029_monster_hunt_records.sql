-- Store one personal-best quest time per character and large-monster species.
-- The ZZ client reports quest elapsed time in 30 Hz frames in
-- MSG_SYS_RECORD_LOG, alongside the per-species kill counters.
CREATE TABLE IF NOT EXISTS public.monster_hunt_records (
    character_id INTEGER NOT NULL REFERENCES public.characters(id) ON DELETE CASCADE,
    monster_id INTEGER NOT NULL,
    quest_id INTEGER NOT NULL DEFAULT 0,
    quest_name TEXT NOT NULL DEFAULT '',
    best_time_frames BIGINT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, monster_id),
    CONSTRAINT monster_hunt_records_monster_range
        CHECK (monster_id >= 0 AND monster_id < 176),
    CONSTRAINT monster_hunt_records_positive_time
        CHECK (best_time_frames > 0)
);

-- Keep the migration safe when a development build created the first version
-- of the table before quest metadata was added.
ALTER TABLE public.monster_hunt_records
    ADD COLUMN IF NOT EXISTS quest_id INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS quest_name TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS monster_hunt_records_ranking_idx
    ON public.monster_hunt_records (monster_id, best_time_frames ASC, character_id ASC);
