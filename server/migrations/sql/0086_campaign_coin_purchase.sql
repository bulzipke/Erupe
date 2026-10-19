-- Premium kits: account-wide gold G coins, with independent repeat purchases.
CREATE TABLE campaign_coin_prices (
    campaign_id integer PRIMARY KEY REFERENCES campaigns(id) ON DELETE CASCADE,
    price integer NOT NULL CHECK (price BETWEEN 1 AND 65535)
);
INSERT INTO campaign_coin_prices(campaign_id, price)
SELECT DISTINCT campaign_id, 50 FROM campaign_category_links WHERE category_id IN (6,7);

CREATE TABLE campaign_coin_purchases (
    id bigserial PRIMARY KEY,
    user_id integer NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    character_id integer NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    campaign_id integer NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    request_code text NOT NULL,
    price integer NOT NULL CHECK(price > 0),
    balance_after integer NOT NULL CHECK(balance_after >= 0),
    purchased_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(user_id, request_code)
);
-- Native reward IDs are signed positive DWORDs. Keep purchase receipts in a
-- separate range so late acknowledgements cannot consume a later purchase.
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM campaign_rewards WHERE id >= 1073741824) THEN
        RAISE EXCEPTION 'campaign reward IDs overlap purchase receipt range';
    END IF;
END $$;
ALTER TABLE campaign_rewards ADD CONSTRAINT campaign_reward_catalog_id_range
    CHECK (id > 0 AND id < 1073741824);
CREATE SEQUENCE campaign_coin_reward_id_seq AS integer
    MINVALUE 1073741824 MAXVALUE 2147483647 START 1073741824 NO CYCLE;
CREATE TABLE campaign_coin_rewards (
    id integer PRIMARY KEY DEFAULT nextval('campaign_coin_reward_id_seq'),
    purchase_id bigint NOT NULL REFERENCES campaign_coin_purchases(id) ON DELETE CASCADE,
    item_type integer NOT NULL,
    quantity integer NOT NULL,
    item_id integer NOT NULL,
    claimed boolean NOT NULL DEFAULT false,
    CHECK (id >= 1073741824)
);
ALTER SEQUENCE campaign_coin_reward_id_seq OWNED BY campaign_coin_rewards.id;
CREATE INDEX campaign_coin_purchases_character ON campaign_coin_purchases(character_id, campaign_id);
CREATE INDEX campaign_coin_rewards_pending ON campaign_coin_rewards(purchase_id) WHERE NOT claimed;
