-- Extend the existing Korean ZZ special gacha. This script is opt-in and
-- does not create an additional shop or change existing prizes or prices.
BEGIN;

DO $catalog$
DECLARE
    shop CONSTANT integer := 65;
    coin CONSTANT integer := 15231;  -- 0x3B7F, 두근두근 코인
    bundle_name CONSTANT text := '비각인 14종 묶음';
    bundle integer;
    prize_weight integer;
    different_weights integer;
BEGIN
    PERFORM id FROM gacha_shop WHERE id = shop AND name = '특전 뽑기' AND gacha_type = 0 FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Expected normal special gacha at shop %', shop;
    END IF;

    IF (SELECT count(*) FROM gacha_entries WHERE gacha_id = shop AND entry_type = 0
          AND item_type = 19 AND item_number = 1 AND rolls = 1) <> 1
       OR (SELECT count(*) FROM gacha_entries WHERE gacha_id = shop AND entry_type = 1
          AND item_type = 19 AND item_number = 10 AND rolls = 11) <> 1 THEN
        RAISE EXCEPTION 'Unexpected gold coin options in special gacha %', shop;
    END IF;

    IF EXISTS (SELECT 1 FROM gacha_entries WHERE gacha_id = shop AND entry_type = 2) THEN
        IF (SELECT count(*) FROM gacha_entries WHERE gacha_id = shop AND entry_type = 2
              AND item_type = 7 AND item_number = coin AND item_quantity = 1 AND rolls = 1) <> 1 THEN
            RAISE EXCEPTION 'Occupied special gacha option 2';
        END IF;
    ELSE
        INSERT INTO gacha_entries
            (gacha_id, entry_type, item_type, item_number, item_quantity, weight, rarity,
             rolls, frontier_points, daily_limit, name)
        VALUES (shop, 2, 7, coin, 1, 0, 0, 1, 0, 0, '');
    END IF;
    IF (SELECT count(*) FROM gacha_entries WHERE gacha_id = shop AND entry_type <> 100) <> 3 THEN
        RAISE EXCEPTION 'Expected exactly three options in special gacha %', shop;
    END IF;

    SELECT min(e.weight), count(DISTINCT e.weight) INTO prize_weight, different_weights
    FROM gacha_entries e WHERE e.gacha_id = shop AND e.entry_type = 100
        AND e.name IS DISTINCT FROM bundle_name
        AND EXISTS (SELECT 1 FROM gacha_items i WHERE i.entry_id = e.id);
    IF different_weights <> 1 OR prize_weight <= 0 THEN
        RAISE EXCEPTION 'Existing special gacha prizes do not have one positive weight';
    END IF;

    SELECT id INTO bundle FROM gacha_entries
    WHERE gacha_id = shop AND entry_type = 100 AND name = bundle_name;
    IF FOUND THEN
        IF (SELECT count(*) FROM gacha_entries WHERE gacha_id = shop AND entry_type = 100 AND name = bundle_name) <> 1
           OR NOT EXISTS (SELECT 1 FROM gacha_entries WHERE id = bundle AND weight = prize_weight)
           OR (SELECT array_agg(item_id ORDER BY item_id) FROM gacha_items WHERE entry_id = bundle
               AND item_type = 7 AND quantity = 1) IS DISTINCT FROM ARRAY(SELECT generate_series(15267, 15280))
           OR (SELECT count(*) FROM gacha_items WHERE entry_id = bundle) <> 14 THEN
            RAISE EXCEPTION 'Existing special gacha seal bundle does not match the catalog';
        END IF;
        RETURN;
    END IF;

    INSERT INTO gacha_entries
        (gacha_id, entry_type, item_type, item_number, item_quantity, weight, rarity,
         rolls, frontier_points, daily_limit, name)
    VALUES (shop, 100, 0, 0, 0, prize_weight, 0, 0, 0, 0, bundle_name)
    RETURNING id INTO bundle;

    -- 0x3BA3..0x3BB0: one seal for each of the fourteen weapon types.
    INSERT INTO gacha_items (entry_id, item_type, item_id, quantity)
    SELECT bundle, 7, item_id, 1 FROM generate_series(15267, 15280) AS item_id;
END;
$catalog$;

COMMIT;
