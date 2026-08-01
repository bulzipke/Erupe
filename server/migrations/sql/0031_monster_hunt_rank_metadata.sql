-- Version 0030 may already be applied on a running server. Reset the ranking
-- table under a new version so every install receives the quest-scoped schema.
-- Existing ranking rows are intentionally discarded: their rank and raw quest
-- metadata cannot be reconstructed reliably.
DROP TABLE IF EXISTS public.monster_hunt_records;

CREATE TABLE public.monster_hunt_records (
    character_id INTEGER NOT NULL REFERENCES public.characters(id) ON DELETE CASCADE,
    monster_id INTEGER NOT NULL,
    quest_id INTEGER NOT NULL,
    quest_name TEXT NOT NULL DEFAULT '',
    rank_kind TEXT NOT NULL,
    variant_kind TEXT NOT NULL,
    quest_variant1 SMALLINT NOT NULL DEFAULT 0,
    quest_variant2 SMALLINT NOT NULL DEFAULT 0,
    quest_variant3 SMALLINT NOT NULL DEFAULT 0,
    quest_variant4 SMALLINT NOT NULL DEFAULT 0,
    rank_band INTEGER NOT NULL DEFAULT 0,
    stat_table1 BIGINT NOT NULL DEFAULT 0,
    stat_table2 SMALLINT NOT NULL DEFAULT 0,
    best_time_frames BIGINT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, monster_id, quest_id, rank_kind, variant_kind),
    CONSTRAINT monster_hunt_records_monster_range
        CHECK (monster_id >= 0 AND monster_id < 176),
    CONSTRAINT monster_hunt_records_quest_range
        CHECK (quest_id >= 0 AND quest_id <= 65535),
    CONSTRAINT monster_hunt_records_rank_kind_check
        CHECK (rank_kind IN ('hr', 'g', 'unknown')),
    CONSTRAINT monster_hunt_records_variant_kind_nonempty
        CHECK (btrim(variant_kind) <> ''),
    CONSTRAINT monster_hunt_records_quest_variant1_range
        CHECK (quest_variant1 >= 0 AND quest_variant1 <= 255),
    CONSTRAINT monster_hunt_records_quest_variant2_range
        CHECK (quest_variant2 >= 0 AND quest_variant2 <= 255),
    CONSTRAINT monster_hunt_records_quest_variant3_range
        CHECK (quest_variant3 >= 0 AND quest_variant3 <= 255),
    CONSTRAINT monster_hunt_records_quest_variant4_range
        CHECK (quest_variant4 >= 0 AND quest_variant4 <= 255),
    CONSTRAINT monster_hunt_records_rank_band_range
        CHECK (rank_band >= 0 AND rank_band <= 65535),
    CONSTRAINT monster_hunt_records_stat_table1_range
        CHECK (stat_table1 >= 0 AND stat_table1 <= 4294967295),
    CONSTRAINT monster_hunt_records_stat_table2_range
        CHECK (stat_table2 >= 0 AND stat_table2 <= 255),
    CONSTRAINT monster_hunt_records_positive_time
        CHECK (best_time_frames > 0)
);

CREATE INDEX monster_hunt_records_ranking_idx
    ON public.monster_hunt_records
       (monster_id, rank_kind, variant_kind, best_time_frames ASC, character_id ASC);
