-- Variant identity cannot be reconstructed reliably for records written by
-- older builds. Start this dashboard-only ranking over instead of keeping or
-- guessing old values.
DROP TABLE IF EXISTS public.monster_hunt_records;

CREATE TABLE public.monster_hunt_records (
    character_id INTEGER NOT NULL REFERENCES public.characters(id) ON DELETE CASCADE,
    monster_id INTEGER NOT NULL,
    variant_kind TEXT NOT NULL DEFAULT 'unknown',
    quest_id INTEGER NOT NULL DEFAULT 0,
    quest_name TEXT NOT NULL DEFAULT '',
    best_time_frames BIGINT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, monster_id, variant_kind),
    CONSTRAINT monster_hunt_records_monster_range
        CHECK (monster_id >= 0 AND monster_id < 176),
    CONSTRAINT monster_hunt_records_positive_time
        CHECK (best_time_frames > 0),
    CONSTRAINT monster_hunt_records_variant_kind_check CHECK (
        variant_kind IN (
            'normal',
            'hardcore',
            'zenith',
            'hardcore_optional',
            'ul_fixed',
            'unknown',
            'phantom_red_rajang',
            'phantom_doragyurosu',
            'violent_raviente',
            'extreme_zinogre',
            'extreme_guanzorumu',
            'extreme_deviljho',
            'extreme_elzelion'
        )
    )
);

CREATE INDEX monster_hunt_records_ranking_idx
    ON public.monster_hunt_records
       (monster_id, variant_kind, best_time_frames ASC, character_id ASC);
