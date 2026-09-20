-- Replace the unverified 500,000..100,000,000-point seed with a partial,
-- evidence-backed round-40 interception catalog. Preserve the former table
-- contents for operator recovery. See docs/diva-round40-applied.md for sources.
-- This changes the display catalog, not the still-unverified interception
-- claim eligibility/treasure/melody/account-shared currency implementation.
CREATE TABLE diva_prizes_before_round40 AS TABLE diva_prizes WITH DATA;
DELETE FROM diva_prizes;

INSERT INTO diva_prizes(type,points_req,item_type,item_id,quantity,gr,repeatable) VALUES
    -- Personal: historical inference accepted at >=70% subjective confidence.
    ('personal', 10000,7,13974,1,true,false), -- Katante silk, 85%
    ('personal', 15000,7,14299,1,true,false), -- Rikante silk, 85%
    ('personal', 20000,7,14537,1,true,false), -- Merente silk, 85%
    ('personal', 25000,7,14758,1,true,false), -- Utante silk, 85%
    ('personal', 30000,7,14854,1,true,false), -- Furante silk, 85%
    ('personal', 86000,26,0,5300,true,false), -- 80%
    -- Personal: directly visible on the round-40 screens.
    ('personal', 90000,7,14063,1,true,false),
    ('personal', 92000,7,10730,5,true,false),
    ('personal', 98000,7,10731,5,true,false),
    ('personal',102000,26,0,5500,true,false),
    ('personal',106000,7,10732,5,true,false),
    ('personal',110000,7,10189,1,true,false),
    ('personal',118000,26,0,5700,true,false),
    ('personal',126000,7,10188,1,true,false),
    ('personal',146000,26,0,5900,true,false),
    ('personal',150000,7,14063,1,true,false),
    ('personal',160000,26,0,6100,true,false),
    -- Personal: later thresholds inferred from independent 28/32 screens,
    -- consistent with the directly verified round-40 146/150/160k rows (75%).
    ('personal',174000,26,0,6300,true,false),
    ('personal',180000,7,14063,1,true,false),
    ('personal',186000,26,0,6500,true,false),
    ('personal',200000,7,10187,1,true,false),
    -- Guild: directly visible round-40 area rewards.
    ('guild',2,7,1026,5,true,false),
    ('guild',3,7,1026,20,true,false),
    ('guild',5,7,7456,3,true,false),
    ('guild',6,7,1026,20,true,false),
    ('guild',8,7,7457,3,true,false),
    ('guild',10,7,1026,20,true,false),
    -- Guild: independent round-35/38 screens agree (85%).
    ('guild',20,7,7458,3,true,false),
    ('guild',22,7,1026,40,true,false),
    ('guild',22,7,13692,7,true,false),
    ('guild',22,7,13693,7,true,false),
    ('guild',24,7,7463,3,true,false),
    ('guild',26,26,0,3000,true,false);

-- Deliberately omitted: cumulative melody caps, special guild-room unlock,
-- missing HR rows, and the 28/30/32-area inference below the accepted threshold.
