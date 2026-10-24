-- Stable account-right definitions, independent of product sale windows and
-- quest publication. No legacy ownership or definition is inferred or seeded.
CREATE TABLE quest_license_definitions (
    license_key text PRIMARY KEY CHECK (license_key ~ '^[a-z][a-z0-9_-]{0,63}$'),
    quest_ids integer[] NOT NULL CHECK (
        cardinality(quest_ids) BETWEEN 1 AND 32768
        AND 0 < ALL(quest_ids) AND 65535 >= ALL(quest_ids)
        AND array_position(quest_ids, NULL) IS NULL
    ),
    mapping_hash text NOT NULL CHECK (mapping_hash ~ '^[0-9a-f]{64}$'),
    bound_at timestamptz NOT NULL DEFAULT now()
);
