-- Separate the native newcomer (+0x2008) and returning (+0x2004) deadlines.
-- Keep the previously agreed start: the account's FIRST character creation.
-- Additional characters and deletion/recreation never renew this entitlement.
ALTER TABLE public.users ADD COLUMN IF NOT EXISTS newcomer_expires timestamptz;

DROP TRIGGER IF EXISTS characters_grant_rookie_return ON public.characters;
DROP FUNCTION IF EXISTS public.grant_rookie_return_on_first_character();

CREATE OR REPLACE FUNCTION public.grant_account_newcomer_on_first_character()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE public.users
       SET newcomer_expires = CURRENT_TIMESTAMP + INTERVAL '30 days',
           rookie_return_pending = false
     WHERE id = NEW.user_id AND rookie_return_pending;
    RETURN NEW;
END;
$$;

CREATE TRIGGER characters_grant_account_newcomer
AFTER INSERT ON public.characters
FOR EACH ROW EXECUTE FUNCTION public.grant_account_newcomer_on_first_character();

-- 0080 did not retain the provenance of a consumed grant. Do NOT guess whether
-- an existing return_expires was a newcomer or a genuine returning entitlement.
-- Preserve those historical dates; only unconsumed/new accounts use the new field.
