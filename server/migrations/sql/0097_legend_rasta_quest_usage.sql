-- Actual quest companions from deployment onward; no saved-hire backfill.
-- Receipts contain only a random server run token, never player/owner IDs.
CREATE TABLE legend_rasta_quest_receipts (
    run_id text PRIMARY KEY CHECK (length(run_id) = 32)
);
CREATE TABLE legend_rasta_usage_stats (
    legend_id smallint PRIMARY KEY CHECK (legend_id BETWEEN 1 AND 21 AND legend_id <> 16),
    usage_count bigint NOT NULL CHECK (usage_count > 0)
);
