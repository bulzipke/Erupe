-- Keep the already published goal cuts for existing rounds.
ALTER TABLE festa_goal_rounds
    ADD COLUMN souls_per_participant integer NOT NULL DEFAULT 500 CHECK (souls_per_participant > 0),
    ADD COLUMN guild_join_cutoff timestamptz;

CREATE TABLE festa_goal_guild_participants (
    event_id integer NOT NULL,
    guild_id integer NOT NULL,
    user_id integer NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (event_id, guild_id, user_id),
    FOREIGN KEY (event_id, guild_id) REFERENCES festa_goal_guild_sizes(event_id, guild_id) ON DELETE CASCADE
);

-- Keep the first receiving character for the whole account, across prize tiers.
CREATE TABLE festa_goal_guild_claimants (
    event_id integer NOT NULL REFERENCES festa_goal_rounds(event_id) ON DELETE CASCADE,
    user_id integer NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    character_id integer NOT NULL,
    PRIMARY KEY (event_id, user_id)
);

INSERT INTO festa_trials (objective, goal_id, times_req, locale_req, reward)
SELECT 5, moss.quest_id, 0, 0, moss.souls
FROM (VALUES (63303, 99), (53226, 200)) AS moss(quest_id, souls)
WHERE NOT EXISTS (
    SELECT 1 FROM festa_trials t WHERE t.objective=5 AND t.goal_id=moss.quest_id
);

INSERT INTO event_quests (max_players, quest_type, quest_id, mark)
SELECT 4, 26, moss.quest_id, 0
FROM (VALUES (63303), (53226)) AS moss(quest_id)
WHERE NOT EXISTS (
    SELECT 1 FROM event_quests q WHERE q.quest_type=26 AND q.quest_id=moss.quest_id
);
