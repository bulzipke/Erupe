CREATE TABLE festa_goal_rounds (
    event_id integer PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    previous_event_id integer REFERENCES events(id) ON DELETE SET NULL
);

CREATE TABLE festa_goal_guild_sizes (
    event_id integer NOT NULL REFERENCES festa_goal_rounds(event_id) ON DELETE CASCADE,
    guild_id integer NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    participants integer NOT NULL CHECK (participants > 0),
    PRIMARY KEY (event_id, guild_id)
);
