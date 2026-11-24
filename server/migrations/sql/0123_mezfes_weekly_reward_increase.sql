-- Change upcoming settlements and booth previews through their shared lookup.
-- Settled rounds retain the amounts already recorded in mezfes_weekly_awards.
CREATE OR REPLACE FUNCTION mezfes_weekly_reward(game_stage smallint, game_place bigint)
RETURNS integer LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN game_stage IN (463,465) THEN
        CASE game_place WHEN 1 THEN 10000 WHEN 2 THEN 7000 WHEN 3 THEN 5000 ELSE 3000 END
    ELSE
        CASE game_place WHEN 1 THEN 5000 WHEN 2 THEN 4000 WHEN 3 THEN 3000 ELSE 100 END
    END
$$;
