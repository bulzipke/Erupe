-- Undo ONLY rows added by the October 3 catalog restoration. Keep backups,
-- currency, inventory, historical purchases and the sequence unchanged.
-- Refuse changed or purchased products rather than discard subsequent work.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
LOCK TABLE public.shop_items IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE public.shop_items_bought IN SHARE ROW EXCLUSIVE MODE;
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM public.road_shop_catalog_added_20261003 b
    JOIN public.shop_items s USING(id)
    WHERE to_jsonb(s) IS DISTINCT FROM to_jsonb(b)
  ) THEN
    RAISE EXCEPTION 'A restored Road product was changed; undo aborted';
  END IF;
  IF EXISTS (
    SELECT 1 FROM public.shop_items_bought p
    JOIN public.road_shop_catalog_added_20261003 b ON b.id=p.shop_item_id
  ) THEN
    RAISE EXCEPTION 'A restored Road product has purchase history; review before undo';
  END IF;
END $$;
DELETE FROM public.shop_items s
USING public.road_shop_catalog_added_20261003 b WHERE s.id=b.id;
COMMIT;
