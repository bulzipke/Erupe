-- Optional recovery of the exact old override rows, including their IDs.
-- Refuse a conflicting live row rather than overwrite a subsequent change.
-- Keep the backup for audit/recovery; do not reset the live sequence.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
LOCK TABLE public.shop_items IN SHARE ROW EXCLUSIVE MODE;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM public.road_shop_overrides_backup_20260921 b
    JOIN public.shop_items s ON s.id = b.id
    WHERE to_jsonb(s) IS DISTINCT FROM (to_jsonb(b) - 'backed_up_at')
  ) THEN
    RAISE EXCEPTION 'A backed-up shop ID is now occupied by a changed row; recovery aborted';
  END IF;
  IF EXISTS (
    SELECT 1
    FROM public.road_shop_overrides_backup_20260921 b
    JOIN public.shop_items s
      ON s.shop_type = b.shop_type AND s.shop_id = b.shop_id
     AND s.item_id = b.item_id
    WHERE NOT EXISTS (
      SELECT 1 FROM public.road_shop_overrides_backup_20260921 old WHERE old.id = s.id
    )
  ) THEN
    RAISE EXCEPTION 'A backed-up product now has a new shop ID; recovery aborted';
  END IF;
END $$;

INSERT INTO public.shop_items
    (shop_type, shop_id, id, item_id, cost, quantity, min_hr, min_sr,
     min_gr, store_level, max_quantity, road_floors, road_fatalis)
SELECT shop_type, shop_id, id, item_id, cost, quantity, min_hr, min_sr,
       min_gr, store_level, max_quantity, road_floors, road_fatalis
FROM public.road_shop_overrides_backup_20260921
ON CONFLICT (id) DO NOTHING;
COMMIT;
