-- A round is identified by the Monday noon on which its scores start.
CREATE INDEX mezfes_weekly_finished ON mezfes_plays(scored_at, stage_id, character_id)
    WHERE NOT cancelled AND points IS NOT NULL;

CREATE TABLE mezfes_settlements (
    week_start timestamptz PRIMARY KEY
);

CREATE TABLE mezfes_weekly_awards (
    week_start timestamptz NOT NULL REFERENCES mezfes_settlements,
    character_id integer NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    stage_id smallint NOT NULL CHECK (stage_id BETWEEN 463 AND 469),
    place bigint CHECK (place > 0),
    gp integer NOT NULL CHECK (gp > 0),
    paid_at timestamptz,
    PRIMARY KEY (character_id, week_start, stage_id)
);

-- Missing result-screen totals qualify for participation only. The festival
-- point award is a separate unit, and must never be compared to a game score.
CREATE FUNCTION mezfes_weekly_standings(begin_at timestamptz, end_at timestamptz)
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
        CASE WHEN r.stage_id IN (463,465) THEN
            CASE r.position WHEN 1 THEN 3000 WHEN 2 THEN 2000 WHEN 3 THEN 1000 ELSE 500 END
        ELSE CASE r.position WHEN 1 THEN 1000 WHEN 2 THEN 800 WHEN 3 THEN 500 ELSE 100 END END
    FROM ranked r
$$;
