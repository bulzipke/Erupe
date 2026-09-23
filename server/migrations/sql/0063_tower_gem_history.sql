-- Sky Corridor ancient-treasure gifts. This is an append-only receipt and
-- does not alter existing tower inventory or historical character records.
CREATE TABLE IF NOT EXISTS tower_gem_history (
    id bigserial PRIMARY KEY,
    receiver_id integer NOT NULL,
    sender_id integer NOT NULL,
    gem_id integer NOT NULL,
    message integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS tower_gem_history_receiver_idx
    ON tower_gem_history (receiver_id, created_at DESC, id DESC);
