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

Allowed values are `none`, `random`, `kaiji`, `higanjima`, `nier`, `evangelion`, and `pso2`. Omit the
setting only to retain the old global `GameplayOptions.Enable*Event` behavior.
To avoid NPC collisions, set an explicit value on every world.

`random` selects one of Kaiji, Higanjima, NieR, Evangelion, and PSO2 with equal probability when the world's
authenticated-player count changes from zero to one. Every channel under the
same entrance entry shares that choice. The event remains fixed while anyone
is connected, is cleared after the final logout, and is selected again on the
next zero-to-one transition. NPC tune flags and scoped event quests always use
the same selection in worlds that allow collaboration quest delivery.

Collaboration quests and tune flags are delivered only to open (`Type: 1`, 자유) worlds.
This applies to explicit modes, `random`, and the
legacy global flags. Other world types receive neither collaboration quest entries
nor collaboration tune flags; random selection behavior is unchanged. This is a
world-type rule, independent of the configured display name.

The built-in Kaiji (40215), Higanjima (40217), NieR (40221, 40223–40227), Evangelion (40211–40214), and PSO2 (40239)
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

## Evangelion quest delivery

Set `"CollabEvent": "evangelion"` on a free/open world to select it explicitly.
Existing `"CollabEvent": "random"` worlds include Evangelion automatically.
When the per-world setting is omitted, `GameplayOptions.EnableEvangelionEvent`
(default `false`) enables its quests and tune flag using the legacy global-flag path.
Explicit modes, including `none`, override this global flag.

The local quest inventory and live NAS binaries identify these four quests:

| ID | Korean title | Main target | Required item ID / count |
| --- | --- | --- | --- |
| 40211 | 몬스터 요격 명령：초호기 | 하루도메르그 | 12104 / 1 |
| 40212 | 몬스터 요격 명령：영호기 | 하루도메르그 | 12104 / 1 |
| 40213 | 토벌 작전, 개시：Mark.０６ | 루코디오라 | 12788 / 1 |
| 40214 | 토벌 작전, 개시：２호기 | 루코디오라 | 12788 / 1 |

The [first collaboration announcement](https://blog.ja.playstation.com/2015/07/22/20150722_mhfg/)
and [second collaboration announcement](https://blog.ja.playstation.com/2015/11/18/20151118-mhfg/)
corroborate the respective EVA unit groups. These are the related quests found
in the audited server inventory; this is not a claim about every historical release.
Source event headers specify four players, type 18, and mark 1 for all four.
Quest binaries, item requirements, rewards, and other eligibility rules remain unchanged.
Required quest files must exist under the configured quest path.

Collaboration tune IDs are Kaiji `1106`, Higanjima `1144`, NieR `1153`,
Evangelion `1156`, and PSO2 `1130`. The active event's flag is sent with value `1` in the existing
quest-list response. Inactive flags are omitted (not explicitly sent as `0`);
the client must reset its collaboration state when refreshing the list or changing
worlds. No collaboration flags are sent outside open worlds.

Evangelion `1156` is the agreed custom-client contract, not a verified original
Evangelion NPC flag. The client must implement its behavior; sending this flag
alone does not implement NPC appearance or grant required quest items.

Migration `0039_event_quest_evangelion_scope.sql` extends the existing DB check
constraint to accept `collab_scope = 'evangelion'` for optional manual overrides
or additional verified quests. It inserts no event-quest rows. The four built-in
IDs require no DB registration; matching DB rows retain their own metadata and
scheduling without generating duplicate list entries.

## Phantasy Star Online 2 (PSO2)

Use `"CollabEvent": "pso2"` for a fixed selection, or `"CollabEvent": "random"`
to include it in the five-event rotation. The legacy global option is
`GameplayOptions.EnablePSO2Event` (default `false`); an explicit world mode
overrides it. Only open worlds (`Type: 1`) receive quest `40239` and tune `1130=1`.
Inactive/other-world flags are omitted, following the client reset contract above.

The confirmed quest is **40239 — 랏피와 놀자 (ラッピーとあそぼう)**.
The live NAS binary identifies the Rappies main objective and delivery of one
yellow feather as Sub A. Its original event header specifies four players,
type 18, mark 1; the current binary's event-list entry is 733 bytes and has
no required entry item (`requiredItemType=0`, `requiredItemCount=0`). All other
original quest rules and rewards remain unchanged.

The [official PSO2 collaboration announcement](https://blog.ja.playstation.com/2018/10/31/20181031-mhfz/)
confirms this quest title and Rappies gameplay. Searching the audited 9,162-ID
server inventory and checking the original `Collab/40239_Rappies.json` found
no additional confirmed PSO2 quests. The similarly named `밀림의 판타지스타`
(23320/54446) concerns Gypceros subspecies, not this collaboration, and is excluded.

The built-in entry requires no DB registration. Migration
`0040_event_quest_pso2_scope.sql` allows `collab_scope='pso2'` on optional DB
overrides/additional verified quests, without inserting any rows. Existing
40239 rows retain their metadata and schedule and cannot bypass the event/world
gate even with an empty scope.

Tune `1130` is the agreed client contract. This server change sends the flag
and quest listing; it does not implement or verify client-side NPC, A.I.S,
object, or music behavior and does not distribute items separately.
