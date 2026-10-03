-- Original Hunting Road limited/special products have weekly, not lifetime,
-- limits. Opt in only the restored catalog; retain all existing custom shop
-- purchase counts and leave other currencies/shops unchanged.
ALTER TABLE public.shop_items
    ADD COLUMN IF NOT EXISTS road_weekly_limit boolean NOT NULL DEFAULT false;
ALTER TABLE public.shop_items_bought
    ADD COLUMN IF NOT EXISTS week_start timestamptz;
