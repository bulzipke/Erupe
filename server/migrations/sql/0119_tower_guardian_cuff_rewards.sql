CREATE TABLE tower_guardian_cuff_rewards (
    earth_id integer NOT NULL,
    character_id integer NOT NULL,
    block smallint NOT NULL CHECK (block IN (1, 2)),
    weapon_type smallint NOT NULL CHECK (weapon_type BETWEEN 0 AND 13),
    PRIMARY KEY (earth_id, character_id, block)
);
