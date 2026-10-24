-- ISOLATED TEST FIXTURE ONLY. NOT A MIGRATION OR RUNTIME SCHEMA LOADER.
-- Applied only to a uniquely named localhost:5433/erupe_test schema.
-- Does not define native receipt IDs, inventory surfaces or a delivery state.
CREATE TABLE users(id integer PRIMARY KEY,gacha_premium bigint CHECK(gacha_premium>=0));
CREATE TABLE characters(id integer PRIMARY KEY,user_id integer NOT NULL REFERENCES users(id),deleted boolean NOT NULL DEFAULT false);
CREATE TABLE quest_supply_definitions(
  supply_key text PRIMARY KEY,grant_kind text NOT NULL,item_type integer NOT NULL,
  item_id integer NOT NULL,quantity integer NOT NULL,price_gold_g integer NOT NULL,
  policy_hash text NOT NULL CHECK(policy_hash ~ '^[0-9a-f]{64}$'),
  CHECK((supply_key='gw_ticket' AND grant_kind='free_character_once_per_window' AND item_type=7 AND item_id=6831 AND quantity=4 AND price_gold_g=0)
     OR (supply_key='hunter_dice_bag' AND grant_kind='paid_repeatable' AND item_type=7 AND item_id=3638 AND quantity=1 AND price_gold_g=1))
);
CREATE TABLE quest_supply_windows(
  window_key text PRIMARY KEY,supply_key text NOT NULL REFERENCES quest_supply_definitions,
  occurrence_key text NOT NULL,UNIQUE(supply_key,occurrence_key),UNIQUE(window_key,supply_key)
);
CREATE TABLE quest_supply_window_revisions(
  window_key text NOT NULL REFERENCES quest_supply_windows,revision integer NOT NULL CHECK(revision>0),
  starts_at timestamptz NOT NULL,ends_at timestamptz NOT NULL,CHECK(starts_at<ends_at),PRIMARY KEY(window_key,revision)
);
CREATE TABLE quest_supply_active_windows(
  window_key text PRIMARY KEY,revision integer NOT NULL,
  FOREIGN KEY(window_key,revision) REFERENCES quest_supply_window_revisions(window_key,revision)
);
CREATE TABLE quest_supply_reservations(
  id bigserial PRIMARY KEY,
  user_id integer NOT NULL REFERENCES users ON DELETE RESTRICT,
  character_id integer NOT NULL REFERENCES characters ON DELETE RESTRICT,
  supply_key text NOT NULL REFERENCES quest_supply_definitions,window_key text NOT NULL,
  grant_kind text NOT NULL,policy_hash text NOT NULL CHECK(policy_hash ~ '^[0-9a-f]{64}$'),
  request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),nonce bytea,
  item_type integer NOT NULL,item_id integer NOT NULL,quantity integer NOT NULL,price_gold_g integer NOT NULL,
  balance_after bigint,reserved_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  FOREIGN KEY(window_key,supply_key) REFERENCES quest_supply_windows(window_key,supply_key),
  UNIQUE(user_id,nonce),UNIQUE(id,character_id,supply_key,grant_kind),
  CHECK((supply_key='gw_ticket' AND grant_kind='free_character_once_per_window' AND item_type=7 AND item_id=6831 AND quantity=4 AND price_gold_g=0 AND nonce IS NULL AND balance_after IS NULL)
     OR (supply_key='hunter_dice_bag' AND grant_kind='paid_repeatable' AND item_type=7 AND item_id=3638 AND quantity=1 AND price_gold_g=1
       AND nonce IS NOT NULL AND octet_length(nonce)=16 AND nonce<>decode(repeat('00',16),'hex') AND balance_after IS NOT NULL AND balance_after>=0))
);
CREATE UNIQUE INDEX quest_supply_free_character_window ON quest_supply_reservations(character_id,supply_key,window_key)
 WHERE grant_kind='free_character_once_per_window';
CREATE TABLE quest_supply_pending_holds(
  reservation_id bigint PRIMARY KEY,character_id integer NOT NULL,supply_key text NOT NULL,grant_kind text NOT NULL,
  FOREIGN KEY(reservation_id,character_id,supply_key,grant_kind) REFERENCES quest_supply_reservations(id,character_id,supply_key,grant_kind)
);
CREATE UNIQUE INDEX quest_supply_one_paid_pending ON quest_supply_pending_holds(character_id,supply_key)
 WHERE grant_kind='paid_repeatable';
CREATE FUNCTION quest_supply_reject_rewrite() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'seasonal reservation history is append-only'; END $$;
CREATE TRIGGER quest_supply_definitions_immutable BEFORE UPDATE OR DELETE ON quest_supply_definitions FOR EACH ROW EXECUTE FUNCTION quest_supply_reject_rewrite();
CREATE TRIGGER quest_supply_windows_immutable BEFORE UPDATE OR DELETE ON quest_supply_windows FOR EACH ROW EXECUTE FUNCTION quest_supply_reject_rewrite();
CREATE TRIGGER quest_supply_revisions_immutable BEFORE UPDATE OR DELETE ON quest_supply_window_revisions FOR EACH ROW EXECUTE FUNCTION quest_supply_reject_rewrite();
CREATE TRIGGER quest_supply_reservations_immutable BEFORE UPDATE OR DELETE ON quest_supply_reservations FOR EACH ROW EXECUTE FUNCTION quest_supply_reject_rewrite();
-- A future verified native inventory transaction must replace this interlock
-- with a real atomic settlement ledger. There is no completion API at present.
CREATE TRIGGER quest_supply_pending_immutable BEFORE UPDATE OR DELETE ON quest_supply_pending_holds FOR EACH ROW EXECUTE FUNCTION quest_supply_reject_rewrite();
CREATE FUNCTION quest_supply_guard_active_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR NEW.window_key<>OLD.window_key OR NEW.revision<=OLD.revision THEN
   RAISE EXCEPTION 'seasonal active revision cannot reset';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER quest_supply_active_forward_only BEFORE UPDATE OR DELETE ON quest_supply_active_windows FOR EACH ROW EXECUTE FUNCTION quest_supply_guard_active_revision();
CREATE FUNCTION quest_supply_guard_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM quest_supply_definitions d WHERE d.supply_key=NEW.supply_key
   AND d.grant_kind=NEW.grant_kind AND d.item_type=NEW.item_type AND d.item_id=NEW.item_id
   AND d.quantity=NEW.quantity AND d.price_gold_g=NEW.price_gold_g AND d.policy_hash=NEW.policy_hash) THEN
   RAISE EXCEPTION 'seasonal reservation differs from immutable definition';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM characters c WHERE c.id=NEW.character_id AND c.user_id=NEW.user_id AND NOT c.deleted) THEN
   RAISE EXCEPTION 'seasonal reservation ownership mismatch';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER quest_supply_reservations_validate BEFORE INSERT ON quest_supply_reservations FOR EACH ROW EXECUTE FUNCTION quest_supply_guard_insert();
