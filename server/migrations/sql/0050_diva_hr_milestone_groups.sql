-- HR milestone replacements share one rank variant per event/threshold with GR.
ALTER TABLE diva_reward_groups DROP CONSTRAINT diva_reward_groups_reward_type_check;
ALTER TABLE diva_reward_groups ADD CONSTRAINT diva_reward_groups_reward_type_check
    CHECK(reward_type IN (0,1,3,6));

-- Preserve offered as well as claimed historical GR prayer bundles.
INSERT INTO diva_reward_groups(char_id,event_id,reward_type,group_key,variant)
SELECT DISTINCT char_id,event_id,1,'milestone-20000','gr'
FROM diva_reward_receipts
WHERE reward_type=1 AND catalog_key ~ '^norma-gr-20000-'
ON CONFLICT DO NOTHING;

-- Existing interception receipt keys refer to the retained DB prize IDs.
INSERT INTO diva_reward_groups(char_id,event_id,reward_type,group_key,variant)
SELECT DISTINCT r.char_id,r.event_id,6,'milestone-' || p.points_req::text,'gr'
FROM diva_reward_receipts r JOIN diva_prizes p ON r.catalog_key='tactics-personal-' || p.id::text
WHERE r.reward_type=6 AND p.type='personal' AND p.gr AND p.points_req>0 AND NOT p.repeatable
ON CONFLICT DO NOTHING;
