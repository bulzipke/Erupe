-- One-round box gachas: a box gacha (gacha_type 4/5) with one_time set is drawn
-- once per character. Once every ball is drawn the box stays empty: reset
-- requests are refused, and the shop list tells the client (bit 0x02 of the
-- byte after gacha_type) so the vorbis.dll client patch hides the reset button
-- and skips the client's automatic reset of an emptied box.
ALTER TABLE gacha_shop ADD COLUMN IF NOT EXISTS one_time boolean NOT NULL DEFAULT false;
