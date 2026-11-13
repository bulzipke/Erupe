ALTER TABLE characters ADD COLUMN mercenary_contract_at timestamptz,
                       ADD COLUMN mercenary_escort boolean NOT NULL DEFAULT true;

-- Existing contracts have no recorded start date. Make them eligible for cancellation.
UPDATE characters AS borrower
SET mercenary_contract_at = CURRENT_TIMESTAMP - interval '7 days'
WHERE borrower.pact_id > 0 AND EXISTS (
    SELECT 1 FROM characters AS owner
    WHERE owner.rasta_id = borrower.pact_id AND owner.id <> borrower.id
);

UPDATE characters SET pact_id = 0 WHERE pact_id > 0 AND mercenary_contract_at IS NULL;

CREATE INDEX characters_mercenary_pact_idx ON characters (pact_id) WHERE pact_id > 0;

CREATE TABLE mercenary_contract_logs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_id integer NOT NULL,
    borrower_id integer NOT NULL,
    actor_id integer NOT NULL,
    occurred_at timestamptz NOT NULL,
    event smallint NOT NULL
);
CREATE INDEX mercenary_contract_logs_owner_idx ON mercenary_contract_logs (owner_id, id DESC);
CREATE INDEX mercenary_contract_logs_borrower_idx ON mercenary_contract_logs (borrower_id, id DESC);

CREATE TABLE mercenary_hunts (
    owner_id integer NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    monster integer NOT NULL,
    hunt_id integer GENERATED ALWAYS AS IDENTITY,
    PRIMARY KEY (owner_id, monster)
);

CREATE TABLE mercenary_hunt_claims (
    owner_id integer PRIMARY KEY REFERENCES characters(id) ON DELETE CASCADE,
    hunt_ids integer[] NOT NULL DEFAULT '{}',
    monsters integer[] NOT NULL DEFAULT '{}',
    claimed_at timestamptz,
    CHECK (cardinality(hunt_ids) = cardinality(monsters) AND cardinality(hunt_ids) <= 5)
);
