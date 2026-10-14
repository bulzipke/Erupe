-- Preserve old item receipts and share their UUID namespace with currency
-- grants. A lost response must never credit the account twice on retry.
ALTER TABLE dashboard_item_deliveries
    ADD COLUMN delivery_kind text NOT NULL DEFAULT 'item'
        CHECK (delivery_kind IN ('item','gacha_premium')),
    ADD COLUMN user_id integer,
    ADD COLUMN coin_balance integer;

ALTER TABLE dashboard_item_deliveries
    DROP CONSTRAINT dashboard_item_deliveries_item_id_check,
    ADD CONSTRAINT dashboard_delivery_payload_check CHECK (
        (delivery_kind='item' AND item_id BETWEEN 1 AND 65535
            AND user_id IS NULL AND coin_balance IS NULL)
        OR
        (delivery_kind='gacha_premium' AND item_id=0 AND item_name=''
            AND user_id IS NOT NULL AND user_id>0
            AND coin_balance IS NOT NULL AND coin_balance>=0
            AND cardinality(mail_ids)=0)
    );
