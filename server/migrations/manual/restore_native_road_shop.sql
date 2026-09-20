-- Operator action, NOT an automatic migration.
-- Removes the server-side Hunting Road overrides so the ZZ client uses its
-- built-in catalog. No reconstructed third-party catalog is inserted.
-- Run only after deciding to remove ALL custom type-10 categories 0..8.
-- The dated backup must not already exist; reruns fail without overwriting it.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
LOCK TABLE public.shop_items IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE public.road_shop_overrides_backup_20260921 AS
SELECT si.*, CURRENT_TIMESTAMP AS backed_up_at
FROM public.shop_items si
WHERE shop_type = 10 AND shop_id BETWEEN 0 AND 8;
ALTER TABLE public.road_shop_overrides_backup_20260921 ADD PRIMARY KEY (id);

DELETE FROM public.shop_items si
USING public.road_shop_overrides_backup_20260921 backup
WHERE si.id = backup.id
  AND si.shop_type = 10 AND si.shop_id BETWEEN 0 AND 8;

-- Do not reset the sequence or delete purchase history, currency, or inventory.
COMMIT;
