-- Operator action, NOT an automatic migration. (2026-10-11)
-- Renames the two Guide HR packages back from the Korean names to the old ASCII names.
-- Only touches rows whose name AND description still match the old ASCII values exactly.
-- Already-localized rows are left alone; anything else aborts. Items/acceptance history untouched.
-- This is the UNDO of localize_guide_hr_rewards_ko.sql (Korean -> old ASCII).
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
LOCK TABLE public.distribution IN SHARE ROW EXCLUSIVE MODE;

CREATE TEMP TABLE guide_hr_rename (
    old_name text PRIMARY KEY, old_description text NOT NULL,
    new_name text NOT NULL, new_description text NOT NULL
) ON COMMIT DROP;
INSERT INTO guide_hr_rename VALUES
    ('ＨＲ５돌파의 포상품', E'~C05ＨＲ５ 달성 길드 포상품.\n코루데FY 방어구, 무기 소재, 장식품, 티켓.\n캐릭터당 1회.',
     'HR5 Breakthrough Reward', E'~C05Guild reward for reaching HR5.\nColude FY armor, weapon materials, jewels and tickets.\nOnce per character.'),
    ('ＨＲ６돌파의 포상품', E'~C05ＨＲ６ 달성 길드 포상품(HC 초대장).\n포르타 티켓[벚꽃] ×300.\n캐릭터당 1회. 제니 상한을 넘겨 팔지 마세요.',
     'HR6 Breakthrough Reward', E'~C05Guild reward for reaching HR6 (HC Invitation).\nPorta Ticket Sakura x300.\nOnce per character. Do not sell beyond the Zenny limit.');

DO $$
DECLARE p record; n_old integer; n_new integer;
BEGIN
    FOR p IN SELECT * FROM guide_hr_rename LOOP
        SELECT count(*) INTO n_old FROM public.distribution WHERE event_name = p.old_name;
        SELECT count(*) INTO n_new FROM public.distribution WHERE event_name = p.new_name;
        IF n_old = 0 AND n_new = 1 THEN
            CONTINUE; -- already localized
        ELSIF n_old <> 1 OR n_new <> 0 THEN
            RAISE EXCEPTION 'Guide package % / % ambiguous (old %, new %); nothing changed', p.old_name, p.new_name, n_old, n_new;
        END IF;
        UPDATE public.distribution SET event_name = p.new_name, description = p.new_description
        WHERE event_name = p.old_name AND description = p.old_description;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'Guide package % has a different description; nothing changed', p.old_name;
        END IF;
    END LOOP;
END $$;

SELECT id, event_name, min_hr FROM public.distribution
WHERE event_name IN (SELECT new_name FROM guide_hr_rename) ORDER BY min_hr;
COMMIT;
