CREATE TABLE festa_trial_votes (
    event_id integer NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    guild_id integer NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    trial_id integer NOT NULL REFERENCES festa_trials(id),
    PRIMARY KEY (event_id, guild_id)
);

-- Keep ballots already cast during registration when upgrading an active round.
INSERT INTO festa_trial_votes(event_id,guild_id,trial_id)
SELECT r.event_id,r.guild_id,t.id
FROM festa_event_lifecycle l
JOIN festa_registrations r ON r.event_id=l.event_id
JOIN guilds g ON g.id=r.guild_id
JOIN guild_characters m ON m.guild_id=g.id AND m.character_id=g.leader_id
JOIN festa_trials t ON t.id=m.trial_vote AND t.objective BETWEEN 1 AND 4
ON CONFLICT DO NOTHING;

CREATE INDEX festa_submissions_round_tally ON festa_submissions(event_id,"timestamp");
CREATE INDEX festa_submissions_round_guild ON festa_submissions(event_id,guild_id);
