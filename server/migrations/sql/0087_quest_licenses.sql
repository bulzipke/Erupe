-- Account-owned quest licenses. No products, prices, legacy grants or balances
-- are initialized here. The purchase backend is not wired to a packet handler.
CREATE TABLE quest_license_purchases (
    id bigserial PRIMARY KEY,
    user_id integer NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Historical identifiers are immutable snapshots, not ownership links.
    -- Deleting a character or campaign must not remove an account's license.
    character_id integer NOT NULL CHECK (character_id > 0),
    campaign_id integer NOT NULL CHECK (campaign_id > 0),
    request_code text NOT NULL CHECK (length(request_code) BETWEEN 1 AND 128),
    price integer NOT NULL CHECK (price BETWEEN 1 AND 65535),
    license_keys text[] NOT NULL CHECK (cardinality(license_keys) BETWEEN 1 AND 512),
    license_hash text NOT NULL CHECK (license_hash ~ '^[0-9a-f]{64}$'),
    balance_after integer NOT NULL CHECK (balance_after >= 0),
    purchased_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, request_code)
);

CREATE TABLE quest_licenses (
    user_id integer NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    license_key text NOT NULL CHECK (license_key ~ '^[a-z0-9][a-z0-9._:-]{0,95}$'),
    -- Account licenses survive receipt maintenance. Ordinary purchase code
    -- never deletes or updates receipts; account deletion follows user CASCADE.
    purchase_id bigint REFERENCES quest_license_purchases(id) ON DELETE SET NULL,
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, license_key)
);
CREATE INDEX quest_licenses_purchase ON quest_licenses(purchase_id);
