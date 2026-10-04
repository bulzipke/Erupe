-- Operator action, NOT an automatic migration.
-- Complete Guide HR5/HR6 milestone packages, official Z notice revised 2016-04-21:
-- https://web.archive.org/web/20190623231104id_/http://cog-members.mhf-z.jp/sp/news/7883.html
-- Archived 2019-06-23. All IDs checked against the current Korean mhfdat.bin.
-- ASCII UI text: the existing distribution handler encodes it as Shift-JIS.
-- min_hr is OLD stored HR, not the HR displayed by the ZZ client.
-- Existing packages, acceptance history, inventory and currency are untouched.
-- Reruns do nothing only if the complete metadata and item list still match.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
LOCK TABLE public.distribution, public.distribution_items IN SHARE ROW EXCLUSIVE MODE;

CREATE TEMP TABLE guide_hr_expected_packages (
    event_name text PRIMARY KEY, description text NOT NULL, min_hr integer NOT NULL
) ON COMMIT DROP;
INSERT INTO guide_hr_expected_packages VALUES
    ('HR5 Breakthrough Reward', E'~C05Guild reward for reaching HR5.\nColude FY armor, weapon materials, jewels and tickets.\nOnce per character.', 100),
    ('HR6 Breakthrough Reward', E'~C05Guild reward for reaching HR6 (HC Invitation).\nPorta Ticket Sakura x300.\nOnce per character. Do not sell beyond the Zenny limit.', 300);

CREATE TEMP TABLE guide_hr_expected_items (
    event_name text NOT NULL, ordinal integer NOT NULL, item_type integer NOT NULL,
    item_id integer NOT NULL, quantity integer NOT NULL,
    PRIMARY KEY (event_name, ordinal), UNIQUE (event_name, item_type, item_id),
    CHECK (item_id BETWEEN 1 AND 65535), CHECK (quantity BETWEEN 1 AND 65535)
) ON COMMIT DROP;
INSERT INTO guide_hr_expected_items VALUES
    ('HR5 Breakthrough Reward',  1, 7, 13190, 250), -- GP교환권 / GP交換券
    ('HR5 Breakthrough Reward',  2, 7,  1472,  90), -- 포르타티켓[연분홍] / ポルタチケット桜
    ('HR5 Breakthrough Reward',  3, 7,  8174,  15), -- 식혈룡 토벌증표 / 喰血竜討伐の証
    ('HR5 Breakthrough Reward',  4, 7,  8175,  25), -- 휘계룡 토벌증표 / 輝界竜討伐の証
    ('HR5 Breakthrough Reward',  5, 7,  8143,  36), -- 휘계룡 갑각 / 輝界竜の甲殻
    ('HR5 Breakthrough Reward',  6, 7,  8146,   6), -- 휘계룡 꼬리 / 輝界竜の尻尾
    ('HR5 Breakthrough Reward',  7, 7,  8142,  36), -- 휘계룡 비늘 / 輝界竜の鱗
    ('HR5 Breakthrough Reward',  8, 7,  1426,  20), -- 고룡종 두꺼운비늘 / 古龍種の厚鱗
    ('HR5 Breakthrough Reward',  9, 7,  1423,   4), -- 고룡종 억센 뿔 / 古龍種の剛角
    ('HR5 Breakthrough Reward', 10, 7,  1411,   5), -- 고룡종 억센 발톱 / 古龍種の剛爪
    ('HR5 Breakthrough Reward', 11, 7,  1420,   8), -- 고룡종 억센 날개 / 古龍種の剛翼
    ('HR5 Breakthrough Reward', 12, 7,  2209,   6), -- 고룡종 구슬 / 古龍種の珠
    ('HR5 Breakthrough Reward', 13, 7,  1410,   8), -- 고룡종 첨조 / 古龍種の尖爪
    ('HR5 Breakthrough Reward', 14, 7,  1408,   6), -- 고룡종 특급 가죽 / 古龍種の特上皮
    ('HR5 Breakthrough Reward', 15, 7,  1405,   8), -- 고룡종 특급 털 / 古龍種の特上毛
    ('HR5 Breakthrough Reward', 16, 7,  1417,   4), -- 고룡종 매우진한피 / 古龍種の特濃血
    ('HR5 Breakthrough Reward', 17, 1,  7382,   1), -- 코루데 FY 헬름
    ('HR5 Breakthrough Reward', 18, 2,  6691,   1), -- 코루데 FY 메일
    ('HR5 Breakthrough Reward', 19, 3,  6684,   1), -- 코루데 FY 암
    ('HR5 Breakthrough Reward', 20, 4,  6838,   1), -- 코루데 FY 폴드
    ('HR5 Breakthrough Reward', 21, 0,  6684,   1), -- 코루데 FY 그리브
    ('HR5 Breakthrough Reward', 22, 1,  7389,   1), -- 코루데 FY 캡
    ('HR5 Breakthrough Reward', 23, 2,  6698,   1), -- 코루데 FY 레지스트
    ('HR5 Breakthrough Reward', 24, 3,  6691,   1), -- 코루데 FY 가드
    ('HR5 Breakthrough Reward', 25, 4,  6845,   1), -- 코루데 FY 코트
    ('HR5 Breakthrough Reward', 26, 0,  6691,   1), -- 코루데 FY 레깅스
    ('HR5 Breakthrough Reward', 27, 7,   910,   8), -- 강력주 / 剛力珠
    ('HR6 Breakthrough Reward',  1, 7,  1472, 300); -- 포르타티켓[연분홍]

DO $$
DECLARE p record; package_id integer; package_count integer;
BEGIN
    FOR p IN SELECT * FROM guide_hr_expected_packages ORDER BY min_hr LOOP
        SELECT count(*), min(id) INTO package_count, package_id
        FROM public.distribution WHERE event_name = p.event_name;
        IF package_count > 1 THEN
            RAISE EXCEPTION 'Ambiguous Guide package %; nothing changed', p.event_name;
        ELSIF package_count = 0 THEN
            INSERT INTO public.distribution
                (character_id, type, deadline, event_name, description,
                 times_acceptable, min_hr, selection)
            VALUES (NULL, 1, NULL, p.event_name, p.description, 1, p.min_hr, false)
            RETURNING id INTO package_id;
            INSERT INTO public.distribution_items (distribution_id, item_type, item_id, quantity)
            SELECT package_id, item_type, item_id, quantity
            FROM guide_hr_expected_items WHERE event_name = p.event_name ORDER BY ordinal;
        END IF;
        IF NOT EXISTS (
            SELECT 1 FROM public.distribution d WHERE d.id = package_id
              AND d.character_id IS NULL AND d.type = 1 AND d.deadline IS NULL
              AND d.description = p.description AND d.times_acceptable = 1
              AND d.min_hr = p.min_hr AND d.max_hr IS NULL
              AND d.min_sr IS NULL AND d.max_sr IS NULL
              AND d.min_gr IS NULL AND d.max_gr IS NULL
              AND COALESCE(d.rights, 0) = 0 AND COALESCE(d.selection, false) = false
        ) THEN
            RAISE EXCEPTION 'Guide package % has different conditions; nothing changed', p.event_name;
        END IF;
        IF EXISTS (
            (SELECT item_type, item_id, quantity FROM public.distribution_items
             WHERE distribution_id = package_id EXCEPT ALL
             SELECT item_type, item_id, quantity FROM guide_hr_expected_items
             WHERE event_name = p.event_name)
            UNION ALL
            (SELECT item_type, item_id, quantity FROM guide_hr_expected_items
             WHERE event_name = p.event_name EXCEPT ALL
             SELECT item_type, item_id, quantity FROM public.distribution_items
             WHERE distribution_id = package_id)
        ) THEN
            RAISE EXCEPTION 'Guide package % has different items; nothing changed', p.event_name;
        END IF;
    END LOOP;
END $$;

SELECT d.id, d.event_name, d.min_hr, d.times_acceptable, count(i.id) AS item_entries
FROM public.distribution d
JOIN guide_hr_expected_packages p ON p.event_name = d.event_name
JOIN public.distribution_items i ON i.distribution_id = d.id
GROUP BY d.id ORDER BY d.min_hr;
COMMIT;
