-- Hide a received quest definition without deleting its row identity, changing
-- its schedule, or revoking file, departure, campaign, or native NPC access.
-- Keep hidden rows in repository results so built-in providers do not recreate
-- them as new visible definitions. Operators can change this value in the DB.
-- Listing suppression applies to ordinary HR/G and long-term definitions
-- (quest types 18, 28, and 54); other dedicated reception remains unchanged.
ALTER TABLE public.event_quests
    ADD COLUMN IF NOT EXISTS list_hidden boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN public.event_quests.list_hidden IS
    'Exclude this row from received quest lists only; preserve its identity and quest authority.';
