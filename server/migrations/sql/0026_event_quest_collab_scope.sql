-- Scope collaboration quests to a single world-level collaboration layout.
-- Empty scope means a normal event quest that is shown in every world.
ALTER TABLE IF EXISTS event_quests
    ADD COLUMN IF NOT EXISTS collab_scope TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'event_quests_collab_scope_check'
          AND conrelid = 'public.event_quests'::regclass
    ) THEN
        ALTER TABLE public.event_quests
            ADD CONSTRAINT event_quests_collab_scope_check
            CHECK (collab_scope IN ('', 'kaiji', 'higanjima', 'nier'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS event_quests_collab_scope_idx
    ON event_quests (collab_scope);
