-- Server-held currencies are debited conditionally; the database also refuses
-- any write that would leave them negative. Negative balances left by the old
-- unchecked debits are reset to zero first.
UPDATE characters SET netcafe_points = 0 WHERE netcafe_points < 0;
UPDATE users SET gacha_premium = 0 WHERE gacha_premium < 0;
UPDATE users SET gacha_trial = 0 WHERE gacha_trial < 0;
UPDATE users SET frontier_points = 0 WHERE frontier_points < 0;

ALTER TABLE characters ADD CONSTRAINT characters_netcafe_points_non_negative CHECK (netcafe_points >= 0);
ALTER TABLE users ADD CONSTRAINT users_gacha_premium_non_negative CHECK (gacha_premium >= 0);
ALTER TABLE users ADD CONSTRAINT users_gacha_trial_non_negative CHECK (gacha_trial >= 0);
ALTER TABLE users ADD CONSTRAINT users_frontier_points_non_negative CHECK (frontier_points >= 0);
