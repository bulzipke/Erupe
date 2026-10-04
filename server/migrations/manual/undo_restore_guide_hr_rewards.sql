-- OPTIONAL operator rollback; NOT run during restoration.
-- Only removes the exact two new Guide packages if nobody has accepted them.
-- Any changed metadata/items or acceptance history aborts the entire rollback.
-- Never removes acceptance history or changes inventory/currency/sequences.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
LOCK TABLE public.distribution, public.distribution_items, public.distributions_accepted
    IN SHARE ROW EXCLUSIVE MODE;

CREATE TEMP TABLE guide_hr_undo_expected (
    event_name text PRIMARY KEY, min_hr integer NOT NULL,
    description text NOT NULL, item_checksum text NOT NULL
) ON COMMIT DROP;
INSERT INTO guide_hr_undo_expected VALUES
    ('HR5 Breakthrough Reward', 100,
     E'~C05Guild reward for reaching HR5.\nColude FY armor, weapon materials, jewels and tickets.\nOnce per character.',
     '74ddd306b534c6d20ae03d27e3f5ca6f'),
    ('HR6 Breakthrough Reward', 300,
     E'~C05Guild reward for reaching HR6 (HC Invitation).\nPorta Ticket Sakura x300.\nOnce per character. Do not sell beyond the Zenny limit.',
     '60ab57c594d298b155aeb62aaae674b1');

DO $$
DECLARE p record; package_id integer; package_count integer; actual_checksum text;
BEGIN
    FOR p IN SELECT * FROM guide_hr_undo_expected LOOP
        SELECT count(*), min(id) INTO package_count, package_id
        FROM public.distribution WHERE event_name = p.event_name;
        IF package_count > 1 THEN
            RAISE EXCEPTION 'Ambiguous Guide package %; rollback aborted', p.event_name;
        ELSIF package_count = 0 THEN
            CONTINUE;
        END IF;
        IF EXISTS (SELECT 1 FROM public.distributions_accepted WHERE distribution_id = package_id) THEN
            RAISE EXCEPTION 'Guide package % has been accepted; rollback aborted', p.event_name;
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
            RAISE EXCEPTION 'Guide package % was changed; rollback aborted', p.event_name;
        END IF;
        SELECT md5(string_agg(item_type::text || ':' || item_id::text || ':' || quantity::text,
                              ',' ORDER BY item_type, item_id, quantity))
        INTO actual_checksum
        FROM public.distribution_items WHERE distribution_id = package_id;
        IF actual_checksum IS DISTINCT FROM p.item_checksum OR EXISTS (
            SELECT 1 FROM public.distribution_items WHERE distribution_id = package_id
            AND (item_id IS NULL OR quantity IS NULL)
        ) THEN
            RAISE EXCEPTION 'Guide package % items were changed; rollback aborted', p.event_name;
        END IF;
    END LOOP;
END $$;

DELETE FROM public.distribution_items i
USING public.distribution d, guide_hr_undo_expected e
WHERE i.distribution_id = d.id AND d.event_name = e.event_name;
DELETE FROM public.distribution d
USING guide_hr_undo_expected e WHERE d.event_name = e.event_name;
COMMIT;
