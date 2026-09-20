-- Freeze each participation-day/ranking bundle before exposing receipt IDs.
CREATE TABLE diva_reward_groups (
    char_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    reward_type SMALLINT NOT NULL CHECK(reward_type IN (0,3)),
    group_key TEXT NOT NULL,
    variant TEXT NOT NULL,
    PRIMARY KEY(char_id,event_id,reward_type,group_key)
);
-- Existing offered AND claimed original GR rows must block HR duplicates.
INSERT INTO diva_reward_groups(char_id,event_id,reward_type,group_key,variant)
SELECT DISTINCT char_id,event_id,0,'daily-' || split_part(catalog_key,'-',2),'gr'
FROM diva_reward_receipts
WHERE reward_type=0 AND catalog_key ~ '^daily-[1-7]-[0-9a-f]+$'
ON CONFLICT DO NOTHING;

-- Membership at the prayer-end boundary, not membership at claim time.
-- Start observation now; do not invent past membership for ended rounds.
CREATE TABLE diva_guild_membership_history (
    id BIGSERIAL PRIMARY KEY,
    char_id BIGINT NOT NULL,
    guild_id BIGINT NOT NULL,
    valid_from TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ
);
CREATE UNIQUE INDEX diva_guild_membership_current ON diva_guild_membership_history(char_id) WHERE valid_until IS NULL;
CREATE INDEX diva_guild_membership_at ON diva_guild_membership_history(char_id,valid_from);
-- Protect the seed/trigger installation gap if another channel is still live.
LOCK TABLE guild_characters IN SHARE ROW EXCLUSIVE MODE;
INSERT INTO diva_guild_membership_history(char_id,guild_id,valid_from)
SELECT character_id,guild_id,statement_timestamp() FROM guild_characters
WHERE character_id IS NOT NULL AND guild_id IS NOT NULL;

CREATE FUNCTION record_diva_guild_membership() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed_at TIMESTAMPTZ := clock_timestamp();
BEGIN
    IF TG_OP='UPDATE' THEN
        IF NEW.character_id IS NOT DISTINCT FROM OLD.character_id AND NEW.guild_id IS NOT DISTINCT FROM OLD.guild_id THEN
            RETURN NEW;
        END IF;
    END IF;
    IF TG_OP IN ('UPDATE','DELETE') THEN
        UPDATE diva_guild_membership_history SET valid_until=changed_at
        WHERE char_id=OLD.character_id AND valid_until IS NULL;
    END IF;
    IF TG_OP IN ('INSERT','UPDATE') AND NEW.character_id IS NOT NULL AND NEW.guild_id IS NOT NULL THEN
        INSERT INTO diva_guild_membership_history(char_id,guild_id,valid_from)
        VALUES(NEW.character_id,NEW.guild_id,changed_at);
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER diva_guild_membership_change AFTER INSERT OR DELETE OR UPDATE OF character_id,guild_id
ON guild_characters FOR EACH ROW EXECUTE FUNCTION record_diva_guild_membership();

CREATE TABLE diva_guild_finalizations (
    event_id INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    finalized_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE diva_guild_final_ranks (
    event_id INTEGER NOT NULL REFERENCES diva_guild_finalizations(event_id) ON DELETE CASCADE,
    guild_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    points BIGINT NOT NULL,
    rank BIGINT NOT NULL,
    PRIMARY KEY(event_id,guild_id)
);
