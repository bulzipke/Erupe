-- Do not infer historical membership from the present guild. Existing song
-- journals and individual totals stay unchanged; their unknown guild is NULL.
-- No FK: deleting a guild must not erase the historical attribution itself.
ALTER TABLE diva_song_records ADD COLUMN guild_id BIGINT CHECK (guild_id > 0);
CREATE INDEX diva_song_records_guild_ranking ON diva_song_records(event_id,guild_id,submitted_at)
WHERE guild_id IS NOT NULL;
