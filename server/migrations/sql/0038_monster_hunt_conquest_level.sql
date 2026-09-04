-- Preserve the runtime level selected for Conquest (極征) hunts. Level zero
-- means that an older record predates runtime-level tracking or that the
-- client setup did not provide a trustworthy value.
ALTER TABLE public.monster_hunt_records
    ADD COLUMN conquest_level INTEGER NOT NULL DEFAULT 0;

ALTER TABLE public.monster_hunt_records
    ADD CONSTRAINT monster_hunt_records_conquest_level_range
        CHECK (conquest_level >= 0 AND conquest_level <= 9999);

ALTER TABLE public.monster_hunt_records
    ADD CONSTRAINT monster_hunt_records_conquest_level_variant
        CHECK (variant_kind = 'conquest' OR conquest_level = 0);

-- A single Conquest quest binary can serve multiple runtime levels, so each
-- level needs its own personal best instead of competing for one quest row.
ALTER TABLE public.monster_hunt_records
    DROP CONSTRAINT monster_hunt_records_pkey;

ALTER TABLE public.monster_hunt_records
    ADD CONSTRAINT monster_hunt_records_pkey PRIMARY KEY
        (character_id, monster_id, quest_id, rank_kind, variant_kind, conquest_level);

DROP INDEX public.monster_hunt_records_ranking_idx;

CREATE INDEX monster_hunt_records_ranking_idx
    ON public.monster_hunt_records
       (monster_id, rank_kind, variant_kind, conquest_level,
        best_time_frames ASC, character_id ASC);
