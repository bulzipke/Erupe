-- HR/GR guild-area bundles choose one rank variant per round and threshold.
ALTER TABLE diva_reward_groups DROP CONSTRAINT diva_reward_groups_reward_type_check;
ALTER TABLE diva_reward_groups ADD CONSTRAINT diva_reward_groups_reward_type_check
    CHECK(reward_type IN (0,1,3,6,7));

-- Exact retained GR keys from divaApprovedGuildRewards, including pending
-- offers: they already fixed the rank variant before HR rewards were added.
-- All three 22-area rows belong to the same milestone, not separate bundles.
WITH approved_gr(catalog_key,areas) AS (
    VALUES
        ('tactics-guild-2-7-1026',2),
        ('tactics-guild-3-7-1026',3),
        ('tactics-guild-5-7-7456',5),
        ('tactics-guild-6-7-1026',6),
        ('tactics-guild-8-7-7457',8),
        ('tactics-guild-10-7-1026',10),
        ('tactics-guild-20-7-7458',20),
        ('tactics-guild-22-7-1026',22),
        ('tactics-guild-22-7-13692',22),
        ('tactics-guild-22-7-13693',22),
        ('tactics-guild-24-7-7463',24),
        ('tactics-guild-26-26-0',26)
)
INSERT INTO diva_reward_groups(char_id,event_id,reward_type,group_key,variant)
SELECT DISTINCT r.char_id,r.event_id,7,'milestone-' || a.areas::text,'gr'
FROM diva_reward_receipts r JOIN approved_gr a ON a.catalog_key=r.catalog_key
WHERE r.reward_type=7
ON CONFLICT DO NOTHING;
