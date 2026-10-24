# Conquest War (討伐征戦 / Seibatsu)

Tracks what is known about the Conquest War event system and what remains to be
reverse-engineered before it can be fully implemented in Erupe.

The `feature/conquest` branch (origin) attempted a partial implementation but drifted too far
from `develop` without completing the core gameplay loop and is not mergeable in its current
state. Its findings are incorporated below.

---

## Game Context

**Conquest War** (討伐征戦, also called *Seibatsu*) is a weekly rotating time-limited event
introduced in the G2 update (July 2013). Players hunt legendary monsters and race to level them
up on a per-player leaderboard.

The event follows a **three-week, three-phase cycle** tracked server-side as the "Earth" system:

| Week | Phase | Japanese | Description |
|------|-------|----------|-------------|
| 1 | **Conquest (Seibatsu)** | 討伐征戦 | Hunting phase — players level their monsters |
| 2 | **Pallone Festival** | パローネ祭典 | Side festival event concurrent with conquest rewards |
| 3 | **Tower (Dure)** | 塔 | Tower climbing event for additional rewards |

### Conquest Mechanics

- Each player has their own independent monster (not shared with others).
- Players hunt their own monster or join quests at the same or higher level.
- A monster starts at level 1 and caps at **9999**.
- Level gain per hunt: **+5** (no faints), **+3** (one faint), **+1** (multiple faints).
- As the monster levels, its stats scale up, making each subsequent hunt harder.
- At week end, rewards are distributed based on the player's rank on the per-monster
  leaderboard.

### Target Monsters (configurable)

The live service used **Shantien**, **Disufiroa**, **G-Rank Black Fatalis**, and
**G-Rank Crimson Fatalis**. The branch defaults to monster IDs `[116, 107, 2, 36]`
(Deviljho, Rajang, Rathalos, Gore Magala — suitable for G8 and below, where the original
four are not available).

For clients at `RealClientMode <= G8`, only the first 3 monsters are exposed; G9+ exposes 4.

### Reward Distribution Types

The `DistributionType` field in reward packets uses these sentinel values:

| Value | Meaning |
|-------|---------|
| `7201` | Item reward (ItemID + quantity) |
| `7202` | N-Points (currency) |
| `7203` | Guild contribution points |

---

## Packet Overview

Thirteen packets implement the Conquest/Earth system. All live in `network/mhfpacket/`.
None have `Build()` implemented (all return `NOT IMPLEMENTED`) — responses are built
directly in handler code using `byteframe`.

### `MsgMhfGetEarthStatus` — Client → Server → Client

Fetches the current Earth event windows and which monsters are active.

**Request** (`msg_mhf_get_earth_status.go`):
```
AckHandle uint32
Unk0      uint32   — unknown; never used by handler
Unk1      uint32   — unknown; never used by handler
```

**Response** (built in `handlers_earth.go → handleMsgMhfGetEarthStatus`):
```
for each active earth event (up to 3: Conquest, Pallone, Tower):
  [uint32] StartTime       — Unix timestamp
  [uint32] EndTime         — Unix timestamp
  [int32]  StatusID        — 1 or 2 (Conquest); 11 (Pallone active) or 12 (Pallone reward); 21 (Tower)
  [int32]  EarthID         — unique event ID from DB row
  [int32]  MonsterID × N   — active conquest monsters (3 for G8, 4 for G9+)
```

**Status ID semantics**: the difference between `1` and `2` for the Conquest phase is not
known. The branch selects `1` when the hunt week is active and `2` otherwise, but this is
a guess.

**Current state**: Implemented. Event windows are generated from a single `events` table row
(`event_type = 'earth'`). A 21-day rolling cycle is computed from that row's `start_time`.
Debug mode (`EarthDebug = true`) collapses the windows to week boundaries for faster testing.

---

### `MsgMhfGetEarthValue` — Client → Server → Client

Fetches numeric values associated with the current Earth event (kill counts, floor tallies,
special flags).

**Request** (`msg_mhf_get_earth_value.go`):
```
AckHandle uint32
Unk0      uint32   — unknown
Unk1      uint32   — unknown
ReqType   uint32   — 1, 2, or 3 (see below)
Unk3–Unk6 uint32   — unknown; never used by handler
```

**Response**: a variable-length array of 6-uint32 entries, wrapped in `doAckEarthSucceed`.
Each entry: `[ID, Value, Unk, Unk, Unk, Unk]`. The last four fields are always zero in
known captures.

| ReqType | Known entries | Notes |
|---------|--------------|-------|
| 1 | `{1, 100}`, `{2, 100}` | Block + DureSlays count — exact meaning unclear |
| 2 | `{1, 5771}`, `{2, 1847}` | Block + Floors? — "Floors?" is a guess |
| 3 | `{1001, 36}` getTouhaHistory; `{9001, 3}` getKohouhinDropStopFlag; `{9002, 10, 300}` getKohouhinForceValue | `ttcSetDisableFlag` relationship unknown |

**Current state**: Implemented with hardcoded values. No database persistence.

---

### `MsgMhfReadBeatLevel` — Client → Server → Client

Reads the player's current conquest beat levels (monster progress values) from the server.

**Request** (`msg_mhf_read_beat_level.go`):
```
AckHandle    uint32
Unk0         uint32   — always 1 in the JP client (hardcoded literal)
ValidIDCount uint32   — always 4 in the JP client
IDs          [16]uint32 — always [0x74, 0x6B, 0x02, 0x24, 0, 0, ...] (hardcoded)
```

**Response**: `ValidIDCount` entries of `[ID uint32, Value uint32, 0 uint32, 0 uint32]`.
Default value if no DB data: `{0,1, 0,1, 0,1, 0,1}` (level 1 for each slot).

**Current state**: Fully implemented. Beat levels are read from `characters.conquest_data`
(16-byte BYTEA). Defaults to level 1 if the column is NULL.

---

### `MsgMhfUpdateBeatLevel` — Client → Server → Client

Saves the player's updated conquest beat levels after a quest.

**Request** (`msg_mhf_update_beat_level.go`):
```
AckHandle uint32
Unk1      uint32     — unknown
Unk2      uint32     — unknown
Data1     [16]int32  — unknown purpose; entirely discarded by the handler
Data2     [16]int32  — beat level data; only first 4 values are stored
```

**Response**: `{0x00, 0x00, 0x00, 0x00}`.

**Current state**: Implemented, but incomplete. Only `Data2[0..3]` is written to the DB.
`Data1` and `Data2[4..15]` are silently ignored. `Unk1`/`Unk2` purposes are unknown.

---

### `MsgMhfReadBeatLevelAllRanking` — Client → Server → Client

Fetches the global leaderboard for a given monster.

**Request** (`msg_mhf_read_beat_level_all_ranking.go`):
```
AckHandle uint32
Unk0      uint32
MonsterID int32   — which monster's ranking to fetch
Unk2      int32   — unknown
```

**Response structure** (from known captures):
```
[uint32] Unk
[int32]  Unk
[int32]  Unk
for each of 100 entries:
  [uint32] Rank
  [uint32] Level
  [32 bytes] HunterName (null-padded)
```

**Current state**: Stubbed. Returns 100 zero-filled entries. No database ranking data exists.

---

### `MsgMhfReadBeatLevelMyRanking` — Client → Server → Client

Fetches the player's own rank on the conquest leaderboard.

**Request** (`msg_mhf_read_beat_level_my_ranking.go`):
```
AckHandle uint32
Unk0      uint32
Unk1      uint32
Unk2      [16]int32  — unknown; possibly the same ID array as ReadBeatLevel
```

**Current state**: Stubbed. Returns an empty buffer. Response format unknown.

---

### `MsgMhfReadLastWeekBeatRanking` — Client → Server → Client

Purpose is partially understood: the handler comment says "controls the monster headings for
the other menus". Likely provides context for which monster's data to display.

**Request** (`msg_mhf_read_last_week_beat_ranking.go`):
```
AckHandle    uint32
Unk0         uint32
EarthMonster int32
```

**Response** (current stub): `[EarthMonster uint32, 0, 0, 0]`. Actual format unknown.

**Current state**: Minimal stub. Response structure not reverse-engineered.

---

### `MsgMhfGetBreakSeibatuLevelReward` — Client → Server → Client

Returns per-monster level-break milestone rewards (items granted at specific level thresholds).

**Request** (`msg_mhf_get_break_seibatu_level_reward.go`):
```
AckHandle    uint32
Unk0         uint32   — unknown; debug-printed but never used
EarthMonster int32
```

**Response**: variable-length array of reward entries via `doAckEarthSucceed`:
```
[int32] ItemID
[int32] Quantity
[int32] Level    — the level threshold at which this reward unlocks
[int32] Unk      — always 0 in known data
```

**Current state**: Implemented with hardcoded per-monster reward tables. Item IDs were
derived from packet captures. No database backend.

---

### `MsgMhfGetWeeklySeibatuRankingReward` — Client → Server → Client

Returns reward tables for conquest ranking, Pallone Festival routes, and Tower floors.
The most complex handler in the branch.

**Request** (`msg_mhf_get_weekly_seibatu_ranking_reward.go`):
```
AckHandle    uint32
Unk0         uint32   — unknown; debug-printed but never used
Operation    uint32   — 1 = conquest ranking, 3 = Pallone festival, 5 = event rewards
ID           uint32   — event/route ID (for Op=1: aligns with EarthStatus 1 and 2)
EarthMonster uint32
```

**Response format for Operation = 1** (conquest ranking rewards):
```
per entry:
  [int32]  Unk0
  [int32]  ItemID
  [uint32] Amount
  [int32]  PlaceFrom
  [int32]  PlaceTo
```

**Response format for Operations 3 and 5** (Pallone/Tower):
```
per entry:
  [int32]  Index0   — floor number (Op=5) or place rank (Op=3)
  [int32]  Index1
  [uint32] Index2   — distribution slot (Op=5 tower dure: 1 or 2)
  [int32]  DistributionType   — 7201/7202/7203
  [int32]  ItemID
  [int32]  Amount
```

**Current state**: Implemented with hardcoded tables derived from packet captures.
- Operation 1: All four monsters return the same bracket table (ranks 1–100, 101–1000,
  1000–1001). The tables are identical for all monsters — this may be correct, or captures
  were only recorded for one monster.
- Operation 3 (Pallone): the current handler returns one zero-filled 24-byte
  placeholder. The older branch's 91-entry description does not describe this
  checkout. See [the current ZZ investigation](pallone-festival.md).
- Operation 5 (Tower): Tower dure kill rewards (260001) and 155-entry floor reward table
  (260003, floors 1–1500) are hardcoded from captures.

Note in source: "Can only have 10 in each dist" — the maximum entries per distribution slot
before the client discards them is 10.

---

### `MsgMhfGetFixedSeibatuRankingTable` — Client → Server → Client

Returns a static "fixed" leaderboard (likely a seeded/display ranking, not live player data).
The handler notes this packet is *not* triggered when `EarthStatus == 1`, suggesting it
belongs to the reward-week display rather than the hunt-week display.

**Request** (`msg_mhf_get_fixed_seibatu_ranking_table.go`):
```
AckHandle    uint32
Unk0         uint32   — unknown
Unk1         int32    — unknown
EarthMonster int32
Unk3         int32    — unknown
Unk4         int32    — unknown
```

**Response**: up to 9 entries:
```
[int32]   Rank
[int32]   Level
[32 bytes] HunterName (null-padded)
```

**Current state**: Implemented with 9 hardcoded "Hunter N" placeholder entries per monster.
`Unk1`, `Unk3`, `Unk4` purposes unknown.

---

### `MsgMhfGetSeibattle` — Client → Server → Client

Fetches Seibattle (guild-vs-guild battle) data. The `GuildID` field suggests this is
guild-specific, but the handler ignores it entirely.

**Request** (`msg_mhf_get_seibattle.go`):
```
AckHandle uint32
Unk0      uint8
Type      uint8    — 1=timetable, 3=key score, 4=career, 5=opponent, 6=convention result,
                     7=char score, 8=cur result
GuildID   uint32
Unk3      uint8    — unknown
Unk4      uint16   — unknown
```

**Response**: varies by `Type`. Timetable (Type=1) returns 3 eight-hour battle windows
computed from midnight. All other types return zero-filled structs.

**Current state**: Stubbed. No database queries, no guild-specific data. The seibattle
guild-vs-guild combat system is entirely unimplemented.

---

### `MsgMhfPostSeibattle` — Client → Server → Client

Submits a seibattle result. All fields are unknown.

**Request** (`msg_mhf_post_seibattle.go`):
```
AckHandle uint32
Unk0      uint8
Unk1      uint8
Unk2      uint32
Unk3      uint8
Unk4      uint16
Unk5      uint16
Unk6      uint8
```

**Current state**: Stubbed. Returns `{0,0,0,0}`. No data is read or persisted.

---

### `MsgMhfGetAdditionalBeatReward` — Client → Server → Client

Purpose unclear. The handler comment states: *"Actual responses in packet captures are all
just giant batches of null bytes. I'm assuming this is because it used to be tied to an
actual event that no longer triggers in the client."*

**Request** (`msg_mhf_get_additional_beat_reward.go`):
```
AckHandle uint32
Unk0–Unk3 uint32   — all unknown
```

**Current state**: Returns 260 (`0x104`) zero bytes. Whether real responses were ever
non-zero is unknown.

---

## Database Schema

The branch adds two migrations:

```sql
-- schemas/patch-schema/23-earth.sql
ALTER TYPE event_type ADD VALUE 'earth';

-- schemas/patch-schema/24-conquest.sql
ALTER TABLE public.characters ADD COLUMN IF NOT EXISTS conquest_data BYTEA;
```

And seeds four conquest quests (`schemas/bundled-schema/ConquestQuests.sql`):
quest IDs `54257`, `54258`, `54277`, `54370` — all `quest_type = 33`, `max_players = 0`.

**Missing tables** required for a full implementation:

| Table | Purpose |
|-------|---------|
| `conquest_rankings` | Per-player, per-monster beat level leaderboard |
| `conquest_reward_claims` | Track which level-break and ranking rewards have been claimed |
| `seibattle_scores` | Guild seibattle results and career records |
| `seibattle_schedules` | Persistent timetable (currently computed in memory) |

---

## Configuration

Two keys were added to `config.go` / `config.json` by the branch:

| Key | Type | Default | Purpose |
|-----|------|---------|---------|
| `EarthDebug` | bool | `false` | Collapses event windows to week boundaries for testing |
| `EarthMonsters` | []int32 | `[116, 107, 2, 36]` | Active conquest target monster IDs |

---

## What Is Already Understood

- The three-phase Earth event cycle (Conquest → Pallone → Tower) and its 21-day rolling
  window, keyed to a single `events` row.
- `GetEarthStatus` response wire format: per-phase `[Start, End, StatusID, EarthID, MonsterIDs…]`.
- `ReadBeatLevel` request is fully hardcoded by the JP client (IDs `0x74, 0x6B, 0x02, 0x24`);
  no dynamic ID resolution is needed.
- Per-character beat level storage: 4 × int32, 16 bytes, in `characters.conquest_data`.
- Level-break reward item IDs and quantities for monsters 116, 107, 2, 36 (from captures).
- Weekly ranking reward brackets for conquest (ranks 1–100, 101–1000, 1000–1001).
- Tower floor reward table (floors 1–1500, item IDs and quantities from captures).
- Tower dure kill reward distributions (dist 1 and 2, from captures).
- `GetWeeklySeibatuRankingReward` response wire format for all three operations.
- `GetFixedSeibatuRankingTable` response wire format (rank + level + 32-byte name).
- `GetBreakSeibatuLevelReward` response wire format (ItemID + Quantity + Level + Unk).
- Distribution type sentinels: `7201` = item, `7202` = N-Points, `7203` = guild contribution.
- The 10-entry-per-distribution-slot limit in weekly seibatu ranking rewards.
- `GetSeibattle` timetable format: 3 × 8-hour windows from midnight.
- Conquest quest IDs: `54257`, `54258`, `54277`, `54370` (type 33).

---

## What Needs RE Before Full Implementation

### High Priority — blocks any functional gameplay

| Unknown | Where to look | Notes |
|---------|---------------|-------|
| Semantics of `EarthStatus` IDs 1 vs 2 | Packet captures during hunt week vs reward week | Currently guessed; wrong selection may break phase detection |
| `MsgMhfUpdateBeatLevel.Data1[16]` | Captures with known quest outcome | Second int32 array entirely discarded; may carry the level gain delta |
| `MsgMhfUpdateBeatLevel.Unk1 / Unk2` | Same captures | May carry monster ID or quest ID needed for routing |
| `ReadBeatLevelAllRanking` response structure | Captures from an active leaderboard | Header fields (3 × uint32 before the 100 entries) unknown |
| `ReadBeatLevelMyRanking` response structure | Same | Format entirely unknown; returns empty today |
| `ReadLastWeekBeatRanking` full response | Captures after week rollover | Only monster ID echoed back today |

### Medium Priority — required for accurate reward flow

| Unknown | Where to look | Notes |
|---------|---------------|-------|
| `MsgMhfPostSeibattle` all fields (`Unk0–Unk6`) | Captures after a seibattle result | Handler does nothing today; this is the score submission path |
| `GetSeibattle` types 3–8 response formats | Captures for each `Type` value | Currently all return zero structs |
| `GetSeibattle.Unk0 / Unk3 / Unk4` | Same captures | Likely context selectors for guild/season |
| `GetEarthValue.Unk0 / Unk1 / Unk3–Unk6` | Captures across different event phases | 6 of the 8 request fields are unknown |
| `GetEarthStatus.Unk0 / Unk1` | Captures across phases | Never used by the handler; may be version or session flags |
| `GetWeeklySeibatuRankingReward` Op=3 content | Captures during Pallone Festival | Current handler returns one zero-filled placeholder; see `pallone-festival.md` |
| Claim tracking semantics | Compare reward endpoint with gacha claim flow | No "claimed" flag exists anywhere in the schema |

### Low Priority — cosmetic / completeness

| Unknown | Where to look | Notes |
|---------|---------------|-------|
| `GetFixedSeibatuRankingTable.Unk1 / Unk3 / Unk4` | Captures | Likely unused alignment or version fields |
| `GetBreakSeibatuLevelReward.Unk0` | Captures with different monsters | Debug-printed; may be season or event ID |
| `ReadBeatLevelAllRanking.Unk0 / Unk2` | Captures | Likely pagination or season selectors |
| `GetAdditionalBeatReward` full structure | Captures if the packet is ever non-null | May be permanently dead in the last client version |
| Pallone Festival route semantics | ZZ client tables and delivery responses | 11 star groups, up to 12 published route keys per group; see `pallone-festival.md` |
| Original live-service event scheduling cadence | JP wiki archives | Cycle length and reset time not publicly documented |

---

## Relation to Other Systems

**Gacha service** (`svc_gacha.go`): The reward distribution model (distribution type,
item ID, amount, rank brackets) is structurally similar to the gacha reward pipeline.
Conquest reward claiming can likely reuse or adapt `GachaService.ClaimRewards` and its
point transaction infrastructure.

**Raviente siege** (`sys_channel_server.go`, `handlers_register.go`): Conquest quests may
use the same `MsgMhfRegisterEvent` / semaphore pattern for quest slot management, though
this has not been confirmed with captures.

**Tower event** (`feature/tower` branch): The Tower phase is part of the same Earth event
cycle. The `GetWeeklySeibatuRankingReward` handler already covers Tower rewards (Op=5).
The two branches should be coordinated or merged.

---

## Known Code Quality Issues in the Branch

The following must be fixed before any part of this branch is merged:

- `fmt.Printf` debug prints left in packet `Parse()` methods:
  `msg_mhf_get_break_seibatu_level_reward.go`, `msg_mhf_get_weekly_seibatu_ranking_reward.go`,
  `msg_mhf_get_fixed_seibatu_ranking_table.go`, `msg_mhf_read_last_week_beat_ranking.go`
- The `cleanupEarthStatus` function wipes `conquest_data` for all characters on event
  expiry — this erases history. Completed conquest data should be archived, not deleted.
- The branch introduced a large `handlers.go` consolidation file that deleted existing test
  files (`handlers_achievement_test.go`, `channel_isolation_test.go`, etc.). These must be
  restored.
- DB access in `handlers_earth.go` uses raw `s.server.db` calls instead of the repo pattern.
  Any merge must route these through `CharacterRepo` and `EventRepo` interfaces.
- `EarthMonsters` config currently accepts any IDs; a G9+ client will crash if fewer than 4
  monsters are configured when `RealClientMode > G8`.
