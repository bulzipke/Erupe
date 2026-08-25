-- Legacy sign sessions have no last-use timestamp, so their age cannot be
-- established safely. Initialize only the expiry cursor; this is deliberately
-- not represented as an issuance or creation timestamp.
ALTER TABLE public.sign_sessions
    ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ;

UPDATE public.sign_sessions
SET last_used_at = COALESCE(last_used_at, NOW());

ALTER TABLE public.sign_sessions
    ALTER COLUMN last_used_at SET DEFAULT NOW(),
    ALTER COLUMN last_used_at SET NOT NULL;

CREATE INDEX IF NOT EXISTS sign_sessions_unbound_last_used_idx
    ON public.sign_sessions (last_used_at)
    WHERE server_id IS NULL;
