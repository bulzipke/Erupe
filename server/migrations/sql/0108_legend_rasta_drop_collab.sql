-- The Fate collaboration presets Ruler (17), Red Saber (18), Black Saber (19)
-- and Black Rider (20) were counted as Legend Rastas by mistake and are no
-- longer recorded; drop what was already counted.
DELETE FROM legend_rasta_usage_stats WHERE legend_id BETWEEN 17 AND 20;
