-- Optional-HC quests were previously stored without knowing whether the player
-- selected the normal or hardcore form. Runtime quest data now resolves that
-- choice, so discard the ambiguous rows and prevent them from returning.
DELETE FROM public.monster_hunt_records
WHERE variant_kind = 'hardcore_optional';

ALTER TABLE public.monster_hunt_records
    DROP CONSTRAINT IF EXISTS monster_hunt_records_variant_kind_resolved;

ALTER TABLE public.monster_hunt_records
    ADD CONSTRAINT monster_hunt_records_variant_kind_resolved
        CHECK (variant_kind <> 'hardcore_optional');
