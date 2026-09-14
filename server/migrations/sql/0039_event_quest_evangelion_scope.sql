-- Allow optional DB overrides/additional quests for the Evangelion event.
-- Built-in delivery of 40211-40214 does not require inserting any rows.
ALTER TABLE public.event_quests
    DROP CONSTRAINT IF EXISTS event_quests_collab_scope_check;

ALTER TABLE public.event_quests
    ADD CONSTRAINT event_quests_collab_scope_check
    CHECK (collab_scope IN ('', 'kaiji', 'higanjima', 'nier', 'evangelion'));
