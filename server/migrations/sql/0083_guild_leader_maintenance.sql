-- Ordinary guild leaders are checked once per Monday 00:00 (UTC+9).
-- Installation is not itself a missed maintenance: start from this week, so
-- existing guilds are first evaluated at the next scheduled Monday.
CREATE TABLE guild_leadership_maintenance (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    checked_week timestamptz NOT NULL
);
INSERT INTO guild_leadership_maintenance(singleton, checked_week)
VALUES(true, date_trunc('week', CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Seoul') AT TIME ZONE 'Asia/Seoul');

-- A manual handover or a newly created guild must not be reprocessed against
-- a maintenance boundary preceding that appointment.
ALTER TABLE guilds ADD COLUMN leader_changed_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP;
CREATE FUNCTION stamp_guild_leader_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.leader_id IS DISTINCT FROM OLD.leader_id THEN
        NEW.leader_changed_at := CURRENT_TIMESTAMP;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guild_leader_changed
BEFORE UPDATE OF leader_id ON guilds
FOR EACH ROW EXECUTE FUNCTION stamp_guild_leader_change();
