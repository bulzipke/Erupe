-- Server-wide counters for how often each quest is cleared or left uncleared.
--
-- A clear is counted from MSG_SYS_RECORD_LOG, which the ZZ client sends when a
-- quest ends successfully. There is no matching failure packet, so a failure is
-- counted when a character that started a quest returns to a non-quest stage
-- without that result ever arriving.
CREATE TABLE IF NOT EXISTS public.quest_result_stats (
    quest_id INTEGER NOT NULL,
    quest_name TEXT NOT NULL DEFAULT '',
    cleared BIGINT NOT NULL DEFAULT 0,
    failed BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (quest_id),
    CONSTRAINT quest_result_stats_quest_range
        CHECK (quest_id > 0 AND quest_id <= 65535),
    CONSTRAINT quest_result_stats_nonnegative
        CHECK (cleared >= 0 AND failed >= 0)
);

CREATE INDEX IF NOT EXISTS quest_result_stats_cleared_idx
    ON public.quest_result_stats (cleared DESC, quest_id ASC)
    WHERE cleared > 0;

CREATE INDEX IF NOT EXISTS quest_result_stats_failed_idx
    ON public.quest_result_stats (failed DESC, quest_id ASC)
    WHERE failed > 0;
