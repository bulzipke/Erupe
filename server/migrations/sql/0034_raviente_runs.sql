-- A Raviente siege is one communal run, not one personal quest result.  Keep
-- its wall-clock lifetime and the hunters who actually entered one of its
-- combat/support quests in dedicated tables.
CREATE TABLE public.raviente_runs (
    id BIGSERIAL PRIMARY KEY,
    channel_key TEXT NOT NULL,
    raviente_generation INTEGER NOT NULL,
    event_kind TEXT NOT NULL DEFAULT 'unknown',
    status TEXT NOT NULL DEFAULT 'active',
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    duration_ms BIGINT,
    end_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT raviente_runs_channel_key_nonempty
        CHECK (btrim(channel_key) <> ''),
    CONSTRAINT raviente_runs_generation_range
        CHECK (raviente_generation >= 0 AND raviente_generation <= 65535),
    CONSTRAINT raviente_runs_event_kind_check
        CHECK (event_kind IN ('unknown', 'berserk', 'extreme', 'small')),
    CONSTRAINT raviente_runs_status_check
        CHECK (status IN ('active', 'completed', 'aborted')),
    CONSTRAINT raviente_runs_duration_positive
        CHECK (duration_ms IS NULL OR duration_ms > 0),
    CONSTRAINT raviente_runs_terminal_fields_check CHECK (
        (status = 'active' AND ended_at IS NULL AND duration_ms IS NULL) OR
        (status = 'completed' AND ended_at IS NOT NULL AND duration_ms IS NOT NULL) OR
        (status = 'aborted' AND ended_at IS NOT NULL AND duration_ms IS NULL)
    )
);

-- A channel cannot legitimately run two sieges at once.  This also protects
-- against duplicate start packets and concurrent handlers.
CREATE UNIQUE INDEX raviente_runs_one_active_per_channel_idx
    ON public.raviente_runs (channel_key)
    WHERE status = 'active';

CREATE INDEX raviente_runs_ranking_idx
    ON public.raviente_runs (event_kind, duration_ms ASC, id ASC)
    WHERE status = 'completed';

-- Character IDs and names are intentional snapshots rather than foreign keys:
-- deleting or renaming a character must not rewrite historical team records.
CREATE TABLE public.raviente_run_participants (
    run_id BIGINT NOT NULL REFERENCES public.raviente_runs(id) ON DELETE CASCADE,
    character_id_snapshot INTEGER NOT NULL,
    character_name_snapshot TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (run_id, character_id_snapshot),
    CONSTRAINT raviente_run_participants_character_id_positive
        CHECK (character_id_snapshot > 0),
    CONSTRAINT raviente_run_participants_name_nonempty
        CHECK (btrim(character_name_snapshot) <> '')
);

CREATE INDEX raviente_run_participants_name_idx
    ON public.raviente_run_participants
       (run_id, lower(character_name_snapshot), character_name_snapshot);
