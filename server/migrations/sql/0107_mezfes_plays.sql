-- One row per MezFes minigame started with a real ticket (practice plays use
-- no ticket and are never recorded). raw_score is the minigame's score total
-- and result_rank its rank (0 C, 1 B, 2 A, 3 S) from the client's result log;
-- points (festival points earned) stays NULL
-- until the result save arrives. Each hunter of a group game has their own row.
CREATE TABLE mezfes_plays (
    id bigserial PRIMARY KEY,
    character_id integer NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    event_id bigint NOT NULL CHECK (event_id >= 0),
    ticket_kind smallint NOT NULL CHECK (ticket_kind IN (0, 1)), -- 0 solo, 1 group
    stage_id smallint NOT NULL DEFAULT 0 CHECK (stage_id = 0 OR stage_id BETWEEN 463 AND 469),
    started_at timestamptz NOT NULL DEFAULT now(),
    raw_score integer CHECK (raw_score >= 0),
    result_rank smallint CHECK (result_rank BETWEEN 0 AND 3),
    points integer CHECK (points >= 0),
    scored_at timestamptz,
    cancelled boolean NOT NULL DEFAULT false
);

CREATE INDEX mezfes_plays_open_idx ON mezfes_plays (character_id, started_at DESC)
    WHERE points IS NULL AND NOT cancelled;
CREATE INDEX mezfes_plays_scored_idx ON mezfes_plays (stage_id, raw_score DESC NULLS LAST)
    WHERE points IS NOT NULL AND NOT cancelled;
