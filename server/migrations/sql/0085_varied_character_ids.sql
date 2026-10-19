-- The client encodes a numeric ID as six little-endian base-32 digits. Small
-- sequential numbers consequently look like R21111. Keep the native encoding
-- and all existing IDs, but spread new allocations over the full 30-bit space.
-- This is a display-ID permutation, not a secret/security token.
LOCK TABLE public.characters IN ACCESS EXCLUSIVE MODE;

CREATE FUNCTION public.permute_character_id(value bigint)
RETURNS integer LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE
    mixed bigint := value;
BEGIN
    IF value < 1 OR value > 1073741823 THEN
        RAISE EXCEPTION 'Character ID counter outside the native 30-bit range';
    END IF;
    -- Every XOR shift and odd multiplication modulo 2^30 is invertible.
    -- Different counters therefore give different IDs; only input zero would
    -- produce zero. bigint intermediates avoid integer multiplication overflow.
    mixed := ((mixed # (mixed >> 16)) * 73244475) & 1073741823;
    mixed := ((mixed # (mixed >> 16)) * 73244475) & 1073741823;
    mixed := mixed # (mixed >> 16);
    RETURN mixed::integer;
END;
$$;

DO $$
DECLARE
    legacy_max bigint;
BEGIN
    -- Reserve consumed legacy sequence values too, including deleted characters
    -- and rolled-back creations. IDs below this boundary are never reused.
    SELECT GREATEST(
        (SELECT COALESCE(MAX(id), 0) FROM public.characters),
        (SELECT COALESCE(MAX(id), 0) FROM public.user_binary),
        (SELECT CASE WHEN is_called THEN last_value ELSE 0 END
           FROM public.characters_id_seq)
    ) INTO legacy_max;
    IF legacy_max >= 1073741823 THEN
        RAISE EXCEPTION 'No native character ID range remains above legacy IDs';
    END IF;
    -- MINVALUE retains the legacy boundary. A separate, non-cycling counter
    -- tracks allocations; the original sequence is left intact.
    EXECUTE format(
        'CREATE SEQUENCE public.character_id_counter_seq AS bigint MINVALUE %s MAXVALUE 1073741823 START WITH %s NO CYCLE CACHE 1',
        legacy_max + 1, legacy_max + 1);
END;
$$;

ALTER SEQUENCE public.character_id_counter_seq OWNED BY public.characters.id;

CREATE FUNCTION public.next_character_id()
RETURNS integer LANGUAGE plpgsql VOLATILE AS $$
DECLARE
    candidate integer;
    first_new bigint;
BEGIN
    SELECT seqmin INTO STRICT first_new FROM pg_catalog.pg_sequence
     WHERE seqrelid = 'public.character_id_counter_seq'::regclass;
    LOOP
        candidate := public.permute_character_id(
            nextval('public.character_id_counter_seq'::regclass));
        -- nextval is atomic and is not rolled back. A consumed counter cannot
        -- repeat, even after deletion, rollback or concurrent creation.
        IF candidate >= first_new
           AND NOT EXISTS (SELECT 1 FROM public.characters WHERE id = candidate)
           AND NOT EXISTS (SELECT 1 FROM public.user_binary WHERE id = candidate) THEN
            RETURN candidate;
        END IF;
    END LOOP;
END;
$$;

ALTER TABLE public.characters ALTER COLUMN id
    SET DEFAULT public.next_character_id();
