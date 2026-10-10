-- System mail has no player sender. Preserve the FK without fake characters.
ALTER TABLE mail ALTER COLUMN sender_id DROP NOT NULL;

-- Snapshots and idempotency survive lost responses and server restarts.
-- Deliberately no cascading FK: deleting mail/characters must not erase receipts.
CREATE TABLE dashboard_item_deliveries (
    request_id uuid PRIMARY KEY,
    character_id integer NOT NULL,
    character_name text NOT NULL,
    character_code varchar(6) NOT NULL,
    item_id integer NOT NULL CHECK (item_id BETWEEN 1 AND 65535),
    item_name text NOT NULL,
    quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 3168),
    mail_ids integer[] NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
