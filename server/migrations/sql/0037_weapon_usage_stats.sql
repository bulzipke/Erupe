-- Count weapon-class usage from authenticated hunters entering quest stages.
-- One departure is recorded per character/session, independently of the quest
-- result.  The fixed rows keep unused weapon classes visible to the dashboard.
CREATE TABLE public.weapon_usage_stats (
    weapon_type SMALLINT PRIMARY KEY,
    usage_count BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT weapon_usage_stats_weapon_type_range
        CHECK (weapon_type >= 0 AND weapon_type < 14),
    CONSTRAINT weapon_usage_stats_count_nonnegative
        CHECK (usage_count >= 0)
);

INSERT INTO public.weapon_usage_stats (weapon_type, usage_count)
SELECT weapon_type, 0
FROM generate_series(0, 13) AS weapon_type;

-- Historical personal-best rows cannot be assigned a weapon reliably.  Leave
-- them NULL and populate the field only when a new best is actually observed.
ALTER TABLE public.monster_hunt_records
    ADD COLUMN weapon_type SMALLINT;

ALTER TABLE public.monster_hunt_records
    ADD CONSTRAINT monster_hunt_records_weapon_type_range
        CHECK (weapon_type IS NULL OR (weapon_type >= 0 AND weapon_type < 14));
