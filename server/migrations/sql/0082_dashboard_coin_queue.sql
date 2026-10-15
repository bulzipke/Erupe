-- A currency receipt is also the durable queue and one-time login notice.
ALTER TABLE dashboard_item_deliveries
    ADD COLUMN pending boolean NOT NULL DEFAULT false,
    ADD COLUMN credited_at timestamptz,
    ADD COLUMN notified_at timestamptz;

-- Historical grants must not be paid or announced again by this feature.
UPDATE dashboard_item_deliveries SET credited_at=created_at,notified_at=created_at
    WHERE delivery_kind='gacha_premium';

ALTER TABLE dashboard_item_deliveries
    DROP CONSTRAINT dashboard_delivery_payload_check,
    ADD CONSTRAINT dashboard_delivery_payload_check CHECK (
        (delivery_kind='item' AND item_id BETWEEN 1 AND 65535
            AND user_id IS NULL AND coin_balance IS NULL AND NOT pending
            AND credited_at IS NULL AND notified_at IS NULL)
        OR
        (delivery_kind='gacha_premium' AND item_id=0 AND item_name=''
            AND user_id IS NOT NULL AND user_id>0 AND cardinality(mail_ids)=0
            AND ((pending AND coin_balance IS NULL AND credited_at IS NULL AND notified_at IS NULL)
                OR (NOT pending AND coin_balance IS NOT NULL AND coin_balance>=0 AND credited_at IS NOT NULL)))
    );
CREATE INDEX dashboard_coin_pending ON dashboard_item_deliveries(user_id,created_at,request_id)
    WHERE delivery_kind='gacha_premium' AND pending;
CREATE INDEX dashboard_coin_unread ON dashboard_item_deliveries(character_id,created_at,request_id)
    WHERE delivery_kind='gacha_premium' AND NOT pending AND notified_at IS NULL;

-- A channel transfer can briefly have two connections sharing one token.
-- Retain each connection until its final save; a failed save blocks payment.
CREATE TABLE dashboard_coin_sessions (
    id uuid PRIMARY KEY,
    user_id integer NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    character_id integer NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    server_id integer NOT NULL,
    token text NOT NULL,
    save_failed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX dashboard_coin_session_user ON dashboard_coin_sessions(user_id);
CREATE INDEX dashboard_coin_session_server ON dashboard_coin_sessions(server_id);
