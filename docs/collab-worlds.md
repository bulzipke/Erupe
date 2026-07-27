# Per-world collaboration events

Kaiji, Higanjima, and NieR use incompatible NPC layouts in the Rasta Bar. Set
one collaboration mode per `Entrance.Entries` world to prevent their NPC tune
flags from being delivered together.

```json
"Entries": [
  { "Name": "입문", "CollabEvent": "none", "Channels": [ ... ] },
  { "Name": "자유", "CollabEvent": "kaiji", "Channels": [ ... ] },
  { "Name": "달인", "CollabEvent": "higanjima", "Channels": [ ... ] },
  { "Name": "구인", "CollabEvent": "nier", "Channels": [ ... ] }
]
```

Allowed values are `none`, `kaiji`, `higanjima`, and `nier`. Omit the setting
only to retain the old global `GameplayOptions.Enable*Event` behavior. To avoid
NPC collisions, set an explicit value on every world.

The `0026_event_quest_collab_scope.sql` migration adds `collab_scope` to
`event_quests`. Empty scope is a normal event quest and is visible everywhere.
Tag each collaboration quest after identifying it from your client data:

```sql
UPDATE event_quests
SET collab_scope = 'kaiji'
WHERE quest_id IN (...);

UPDATE event_quests
SET collab_scope = 'higanjima'
WHERE quest_id IN (...);

UPDATE event_quests
SET collab_scope = 'nier'
WHERE quest_id IN (...);
```

Only matching scoped quests are included in that world's event-quest list.
The server does not guess quest IDs: incorrectly labeling a quest would hide
valid content or expose a quest with the wrong NPC layout.
