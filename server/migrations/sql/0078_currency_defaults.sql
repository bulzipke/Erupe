-- Server-held gacha currencies were created without a default, so every
-- account started with NULL and additions such as `frontier_points + $1`
-- stayed NULL (frontier points from gifts and step-up gachas were lost).
-- Existing NULLs become 0 and new rows start at 0.
UPDATE users SET frontier_points = 0 WHERE frontier_points IS NULL;
UPDATE users SET gacha_premium = 0 WHERE gacha_premium IS NULL;
UPDATE users SET gacha_trial = 0 WHERE gacha_trial IS NULL;

ALTER TABLE users ALTER COLUMN frontier_points SET DEFAULT 0;
ALTER TABLE users ALTER COLUMN gacha_premium SET DEFAULT 0;
ALTER TABLE users ALTER COLUMN gacha_trial SET DEFAULT 0;
