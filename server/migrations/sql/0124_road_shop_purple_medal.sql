-- Hunter Medal Purple, exchanged while a medal-consuming quest is available.
-- 500P is the server's provisional price. The 30-item limit uses the Road week.
INSERT INTO public.shop_items
    (shop_type, shop_id, item_id, cost, quantity, min_hr, min_sr, min_gr,
     store_level, max_quantity, road_floors, road_fatalis, road_weekly_limit)
SELECT 10, 7, 9665, 500, 1, 0, 0, 200, 0, 30, 11, 0, true
WHERE NOT EXISTS (
    SELECT 1 FROM public.shop_items WHERE shop_type=10 AND shop_id=7 AND item_id=9665
);
