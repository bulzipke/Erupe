-- Hunting Road (狩煉道) and Tower (天廊) skills are learned with different
-- points (Road SP in the save data, TSP in tower.tsp), but ZZ reuses the G10
-- Tower skill screen for the Hunting Road and keeps one 64-entry level array,
-- which the server stored whole in tower.skills. Split the store by skill id:
-- the thirteen Tower-only skills (3, 4, 6-13, 16, 17, 21) stay in
-- tower.skills and every other level (the nine common skills and the Road
-- skills) moves to road_skills. GetTowerInfo InfoType 2 merges them back.
CREATE TABLE IF NOT EXISTS road_skills (
    character_id integer PRIMARY KEY,
    skills text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO road_skills (character_id, skills)
SELECT DISTINCT ON (t.char_id) t.char_id,
       array_to_string(ARRAY(
           SELECT CASE WHEN s.i - 1 IN (3, 4, 6, 7, 8, 9, 10, 11, 12, 13, 16, 17, 21) THEN '0' ELSE s.v END
           FROM unnest(string_to_array(t.skills, ',')) WITH ORDINALITY AS s(v, i)
           ORDER BY s.i), ',')
FROM tower t
WHERE t.char_id IS NOT NULL AND t.skills IS NOT NULL
ORDER BY t.char_id
ON CONFLICT (character_id) DO NOTHING;

UPDATE tower t SET skills = array_to_string(ARRAY(
    SELECT CASE WHEN s.i - 1 IN (3, 4, 6, 7, 8, 9, 10, 11, 12, 13, 16, 17, 21) THEN s.v ELSE '0' END
    FROM unnest(string_to_array(t.skills, ',')) WITH ORDINALITY AS s(v, i)
    ORDER BY s.i), ',')
WHERE t.skills IS NOT NULL;
