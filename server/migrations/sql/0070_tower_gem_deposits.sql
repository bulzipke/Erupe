-- D463: antique deposit retries use the committed result's server-owned key.
CREATE TABLE IF NOT EXISTS tower_gem_deposits (
 character_id integer NOT NULL,
 run_id varchar(32) NOT NULL,
 slot integer NOT NULL CHECK(slot>=0 AND slot<24),
 gem integer NOT NULL,
 quantity integer NOT NULL CHECK(quantity BETWEEN 1 AND 10),
 PRIMARY KEY(character_id,run_id,slot),
 FOREIGN KEY(character_id,run_id) REFERENCES tower_settlements(character_id,run_id) ON DELETE CASCADE
);
