-- Account-wide attendance rewards, Korean calendar days (UTC+09).
CREATE TABLE public.daily_gacha_coins (
    user_id integer NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    reward_day date NOT NULL,
    online_ms integer NOT NULL DEFAULT 0 CHECK (online_ms BETWEEN 0 AND 3600000),
    intervals jsonb NOT NULL DEFAULT '[]'::jsonb,
    first_claimed boolean NOT NULL DEFAULT false,
    bonus_claimed boolean NOT NULL DEFAULT false,
    PRIMARY KEY (user_id, reward_day),
    CHECK (NOT bonus_claimed OR (first_claimed AND online_ms = 3600000))
);
