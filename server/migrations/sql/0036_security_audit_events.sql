-- Append-only security observations. These rows deliberately contain no
-- credentials or packet bodies; they are intended for post-incident review.
CREATE TABLE IF NOT EXISTS public.security_audit_events (
    id           BIGSERIAL PRIMARY KEY,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Deliberately no foreign keys: rejected requests may name users or
    -- characters that do not exist, and those attempted identifiers are
    -- still valuable audit evidence.
    user_id      BIGINT,
    character_id BIGINT,
    source       TEXT NOT NULL,
    event_type   TEXT NOT NULL,
    severity     TEXT NOT NULL,
    decision     TEXT NOT NULL,
    details      JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS security_audit_events_character_created_idx
    ON public.security_audit_events (character_id, created_at DESC);

CREATE INDEX IF NOT EXISTS security_audit_events_type_created_idx
    ON public.security_audit_events (event_type, created_at DESC);

CREATE INDEX IF NOT EXISTS security_audit_events_created_idx
    ON public.security_audit_events (created_at);
