-- ZZ's native catalog has 889 rows in tabs 0..6, NONE in tabs 7/8.
-- Supply only server-delivered products and three absent decorations.
-- Quantity is always one. Original weekly limits are opt-in via migration 0072;
-- the server uses Monday 00:00 UTC+9, as requested by this server's operator.
-- Evidence/conflicts: docs/road-shop-original-catalog.md.
-- ID/price/condition: https://ferias.life.coocan.jp/sozai/other_sozai.js
-- Limits: https://w.atwiki.jp/giurasu/pages/1464.html
-- Ravi limits cross-checked with TWO 2018 tables (the wiki contains typo rows):
-- https://sonouchiyameru.blog.fc2.com/blog-entry-188.html
-- https://tachipeople.blog.fc2.com/blog-entry-67.html
-- Special limits total 19,800P for one GX7 part; the July 2017 game screenshot
-- confirms 祖龍の輝玉 costs 2,000P, requires 3 kills, and has 3 exchanges left:
-- https://kemkemkem.hatenadiary.jp/entry/2017/07/22/215901
-- Official addition of ALL seven Tower appearance tickets (2018-12-26):
-- https://web.archive.org/web/20191218002242/http://cog-members.mhf-z.jp/sp/news/13639.html
-- Bonito/Meirida use the contemporary Jan 9 table's stage 100/110 values;
-- Ferias reverses those two stages. Membership is verified, final conditions
-- remain a documented source conflict, not grounds to omit live products.
-- https://byuwasoku.hatenablog.com/entry/2019/01/09/120644
WITH catalog(shop_id,item_id,cost,min_gr,max_quantity,road_floors,road_fatalis) AS (
    VALUES
    -- Missing decorations. 空穏 requires stage 11, corroborated by
    -- https://blog.livedoor.jp/salvare3260/archives/16117088.html
    (4,15232,1000,1,0,1,0),    -- 3B80 G級・怪護珠
    (4,14950,1000,1,0,1,0),    -- 3A66 G級・超舞珠
    (4,15093,1000,1,0,11,0),   -- 3AF5 G級・空穏珠
    -- Limited: 47 rows, including the two verified live-service products.
    (7,11286,500,1,20,1,0),    -- 2C16 いにしえの超鉄鋼
    (7,14432,1000,1,5,90,0),   -- 3860 ライオス解放券Ｄ
    (7,14433,1000,1,5,100,0),  -- 3861 ボニト解放券Ｄ (Jan 9, 2019 table)
    (7,14434,1000,1,5,110,0),  -- 3862 メイリダ解放券Ｄ (Jan 9, 2019 table)
    (7,14435,1000,1,5,120,0),  -- 3863 ミニオム解放券Ｄ
    (7,14436,1000,1,5,130,0),  -- 3864 ハリメノ解放券Ｄ
    (7,14021,1000,1,5,140,0),  -- 36C5 ワンス解放券Ｄ
    (7,14742,4000,1,10,20,0),  -- 3996 灰輝原珠: TWO independent tiers
    (7,14742,2000,1,20,40,0),
    (7,14298,1000,200,5,10,0), -- 37DA 脅異の象徴
    (7,13895,100,1,5,1,0),     -- 3647 迎撃の白団旗: Battle Song ONLY
    (7,13896,500,1,5,1,0),     -- 3648 迎撃の紫団旗: road_shop.go filters
    (7,13897,1000,1,5,1,0),    -- 3649 迎撃の橙団旗
    (7,14445,1000,1,1,1,0),    -- 386D 再覚之古書・煉
    (7,14443,3000,1,5,1,0),    -- 386B 煉技之古書
    (7,14444,5000,1,5,1,0),    -- 386C 煉技指南書
    (7,14639,1000,200,5,11,0), -- 392F 狩煉の印
    (7,15448,1000,200,2,11,0), -- 3C58 託された秘文書
    (7,12460,500,1,140,1,0),   -- 30AC 大巌竜の深黒皮
    (7,12461,500,1,140,1,0),   -- 30AD 大巌竜の深黒鱗
    (7,12462,1000,1,65,1,0),   -- 30AE 大巌竜の深黒血
    (7,12463,1000,1,65,1,0),   -- 30AF 大巌竜の輝結晶
    (7,12464,1500,1,15,1,0),   -- 30B0 大巌竜の深黒角
    (7,12465,1500,1,35,1,0),   -- 30B1 大巌竜の深黒翼
    (7,12466,1500,1,50,1,0),   -- 30B2 大巌竜の深黒尾
    (7,12467,1500,1,15,1,0),   -- 30B3 大巌竜の深黒牙
    (7,12468,1500,1,20,1,0),   -- 30B4 大巌竜の秘髄
    (7,12469,1500,1,50,1,0),   -- 30B5 大巌竜の深黒棘 (2018 blogs: 50)
    (7,14389,500,1,46,10,0),   -- 3835 和花の絹
    (7,14743,500,1,46,10,0),   -- 3997 和花の鋼鉄
    (7,14853,500,1,46,10,0),   -- 3A05 和花の鋳鉄
    (7,15020,500,1,46,10,0),   -- 3AAC 和奏の鍛鋼
    (7,15021,500,1,46,10,0),   -- 3AAD 和奏の綿
    (7,15111,500,1,46,10,0),   -- 3B07 和鳳の絹
    (7,15112,500,1,46,10,0),   -- 3B08 和奏の銀石
    (7,15773,500,1,46,10,0),   -- 3D9D 和燭の麻
    (7,16338,500,1,31,10,0),   -- 3FD2 和獄の煉鉄
    (7,16339,500,1,31,10,0),   -- 3FD3 和獄の純鉄
    -- Introduction conditions verified in Jan/Feb 2019 blog records, NOT
    -- Ferias's unverified later stage-10 entries. See the investigation doc.
    (7,16340,500,1,46,50,0),   -- 3FD4 和獄の絹
    (7,16341,500,1,46,50,0),   -- 3FD5 和戟の布
    (7,14424,1000,1,5,30,0),   -- 3858 煉華解放券Ｄ
    (7,15032,1000,1,5,40,0),   -- 3AB8 煉緋解放券Ｄ
    (7,15117,1000,1,5,50,0),   -- 3B0D 煉鳳解放券Ｄ
    (7,15868,1000,1,5,60,0),   -- 3DFC 煉燭解放券Ｄ
    (7,16508,1000,1,5,70,0),   -- 407C 煉獄解放券Ｄ
    (7,16509,1000,1,5,80,0),   -- 407D 煉戟解放券Ｄ
    (7,14946,1000,1,5,100,0),  -- 3A62 煉華解放券Ｃ
    -- Special: 11 rows. The client checks road_fatalis; no free unlock.
    (8,14390,200,1,10,0,1),    -- 3836 祖龍の雷鱗
    (8,14391,200,1,10,0,1),    -- 3837 祖龍の雷殻
    (8,14392,2000,1,3,0,3),    -- 3838 祖龍の輝玉
    (8,14393,500,1,2,0,1),     -- 3839 祖龍の蒼角
    (8,14394,700,1,2,0,2),     -- 383A 祖龍の紅雷角
    (8,14395,1000,1,2,0,2),    -- 383B 黒龍の紅閃眼
    (8,14396,400,1,2,0,1),     -- 383C 祖龍の蒼翼爪
    (8,14397,800,1,2,0,2),     -- 383D 祖龍の裂空翼
    (8,14398,400,1,2,0,1),     -- 383E 祖龍の白輝殻
    (8,14399,1100,1,2,0,2),    -- 383F 祖龍の淡紅殻
    (8,14446,1000,1,5,0,4)     -- 386E 祖龍解放券Ｄ
)
INSERT INTO public.shop_items
    (shop_type,shop_id,item_id,cost,quantity,min_hr,min_sr,min_gr,store_level,
     max_quantity,road_floors,road_fatalis,road_weekly_limit)
SELECT 10,c.shop_id,c.item_id,c.cost,1,0,0,c.min_gr,0,
       c.max_quantity,c.road_floors,c.road_fatalis,c.shop_id IN (7,8)
FROM catalog c
WHERE NOT EXISTS (
    SELECT 1 FROM public.shop_items s
    WHERE s.shop_type=10 AND s.shop_id=c.shop_id AND s.item_id=c.item_id
      AND s.road_floors=c.road_floors AND s.road_fatalis=c.road_fatalis
)
ORDER BY c.shop_id,c.item_id,c.road_floors;
