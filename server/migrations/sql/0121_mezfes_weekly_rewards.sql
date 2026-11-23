-- Freeze the old rounds with their original rewards and notice policy.
ALTER TABLE mezfes_settlements ADD COLUMN notify_nonparticipants boolean NOT NULL DEFAULT false;
ALTER TABLE mezfes_settlements ALTER COLUMN notify_nonparticipants SET DEFAULT true;

CREATE TABLE mezfes_weekly_notices (
    week_start timestamptz NOT NULL REFERENCES mezfes_settlements(week_start) ON DELETE CASCADE,
    character_id integer NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    PRIMARY KEY (character_id, week_start)
);

-- A null place is a completed game without a usable raw score. Both it
-- and ranks below third use the same participation rate shown in the booth.
CREATE FUNCTION mezfes_weekly_reward(game_stage smallint, game_place bigint)
RETURNS integer LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN game_stage IN (463,465) THEN
        CASE game_place WHEN 1 THEN 5000 WHEN 2 THEN 4000 WHEN 3 THEN 3000 ELSE 1000 END
    ELSE
        CASE game_place WHEN 1 THEN 3000 WHEN 2 THEN 2000 WHEN 3 THEN 1000 ELSE 500 END
    END
$$;

CREATE OR REPLACE FUNCTION mezfes_weekly_standings(begin_at timestamptz, end_at timestamptz)
RETURNS TABLE(stage_id smallint, character_id integer, hunter_name varchar,
              score integer, place bigint, gp integer)
LANGUAGE sql STABLE AS $$
    WITH best AS (
        SELECT DISTINCT ON (p.stage_id, p.character_id)
            p.stage_id, p.character_id, c.name, p.raw_score, p.scored_at
        FROM mezfes_plays p JOIN characters c ON c.id=p.character_id
        WHERE NOT p.cancelled AND p.points IS NOT NULL AND p.stage_id BETWEEN 463 AND 469
          AND p.scored_at >= begin_at AND p.scored_at < end_at
          AND NOT c.deleted AND NOT COALESCE(c.is_new_character,false) AND c.name <> ''
        ORDER BY p.stage_id, p.character_id, p.raw_score DESC NULLS LAST, p.scored_at, p.id
    ), ranked AS (
        SELECT b.*, CASE WHEN raw_score IS NOT NULL THEN row_number() OVER (
            PARTITION BY b.stage_id ORDER BY raw_score DESC NULLS LAST, scored_at, b.character_id
        ) END AS position FROM best b
    )
    SELECT r.stage_id, r.character_id, r.name::varchar, r.raw_score, r.position,
        mezfes_weekly_reward(r.stage_id, r.position)
    FROM ranked r
$$;
