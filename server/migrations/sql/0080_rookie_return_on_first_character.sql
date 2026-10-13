-- A newcomer receives the account-wide 30-day Return-world window when the
-- first character slot is created, not when the account is registered.
-- Existing accounts are not reclassified as newcomers by this migration.
ALTER TABLE public.users
    ADD COLUMN IF NOT EXISTS rookie_return_pending boolean NOT NULL DEFAULT false;
ALTER TABLE public.users ALTER COLUMN rookie_return_pending SET DEFAULT true;

CREATE OR REPLACE FUNCTION public.grant_rookie_return_on_first_character()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- The conditional UPDATE serializes concurrent inserts for one account.
    -- Consuming the persistent flag also prevents deletion/recreation renewals.
    UPDATE public.users
       SET return_expires = GREATEST(return_expires, CURRENT_TIMESTAMP + INTERVAL '30 days'),
           rookie_return_pending = false
     WHERE id = NEW.user_id AND rookie_return_pending;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS characters_grant_rookie_return ON public.characters;
CREATE TRIGGER characters_grant_rookie_return
AFTER INSERT ON public.characters
FOR EACH ROW EXECUTE FUNCTION public.grant_rookie_return_on_first_character();
