-- Permit optional PSO2 quest overrides. Built-in 40239 needs no DB row.
ALTER TABLE public.event_quests
    DROP CONSTRAINT IF EXISTS event_quests_collab_scope_check;

ALTER TABLE public.event_quests
    ADD CONSTRAINT event_quests_collab_scope_check
    CHECK (collab_scope IN ('', 'kaiji', 'higanjima', 'nier', 'evangelion', 'pso2'));
