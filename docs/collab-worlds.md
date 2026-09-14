# Per-world collaboration events

Kaiji, Higanjima, and NieR use incompatible NPC layouts in the Rasta Bar. Set
one collaboration mode per `Entrance.Entries` world to prevent their NPC tune
flags from being delivered together.

```json
"Entries": [
  { "Name": "입문", "Type": 3, "CollabEvent": "none", "Channels": [ ... ] },
  { "Name": "자유", "Type": 1, "CollabEvent": "random", "Channels": [ ... ] },
  { "Name": "복귀", "Type": 5, "CollabEvent": "none", "Channels": [ ... ] }
]
```

Allowed values are `none`, `random`, `kaiji`, `higanjima`, and `nier`. Omit the
setting only to retain the old global `GameplayOptions.Enable*Event` behavior.
To avoid NPC collisions, set an explicit value on every world.

`random` selects one of Kaiji, Higanjima, and NieR when the world's
authenticated-player count changes from zero to one. Every channel under the
same entrance entry shares that choice. The event remains fixed while anyone
is connected, is cleared after the final logout, and is selected again on the
next zero-to-one transition. NPC tune flags and scoped event quests always use
the same selection in worlds that allow collaboration quest delivery.

Collaboration quests are delivered only to open (`Type: 1`, 자유) worlds.
This applies to explicit modes, `random`, and the
legacy global flags. Other world types receive no collaboration quest entries;
their NPC tune flags and random selection behavior are unchanged. This is a
world-type rule, independent of the configured display name.

The built-in Kaiji (40215), Higanjima (40217), and NieR (40221, 40223–40227)
quests are added without requiring database rows. Database overrides for these
IDs follow the same restrictions even when their `collab_scope` is empty.

The `0026_event_quest_collab_scope.sql` migration adds `collab_scope` to
`event_quests`. Except for the built-in IDs above, an empty scope is a normal
event quest and is unaffected by the collaboration world restriction.
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

Only matching scoped quests are included in an eligible world's event-quest
list. Beyond the built-in IDs above, the server does not guess which unscoped
quests are collaborations: tag any additional collaboration quests explicitly.
