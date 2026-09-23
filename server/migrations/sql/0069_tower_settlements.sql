-- D463: one atomic Tower result plus a durable receipt for ACK/commit retries.
CREATE TABLE IF NOT EXISTS tower_settlements (
 character_id integer NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
 run_id varchar(32) NOT NULL,
 block smallint NOT NULL,
 tr integer NOT NULL,
 trp integer NOT NULL,
 tsp integer NOT NULL,
 floors integer NOT NULL,
 credited_floors integer NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(character_id,run_id)
);
