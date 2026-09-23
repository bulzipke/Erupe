# Special guild-hall Poogies: server implementation

## Confirmed native behavior

Read-only analysis of `mhfo-hd.dll.clean.mapped.bin` (ZZ):

- In special guild hall map `445` (`0x1BD`), `115547D0` calls
  `11551A60` (`snj_db_set_pigcloth_tactics`). This sends
  `OPERATE_GUILD` actions `25`, `26`, `27`, one for each Poogie.
- `INFO_GUILD` has a second three-byte clothing array, independent of the
  ordinary guild hall's array. Previously the server repeated the ordinary
  array and acknowledged the special operations without implementing them.
- `106F4580` enumerates IDs `0..9`; on map 445 it bypasses the ordinary
  unlocked-clothing mask. `106F7A10` does not charge ordinary clothing
  materials, and `106FB710` treats these choices as available.
- `106F7110` allows the change-clothes menu only when `DAT_1E5054A0 == 1`.
  This is `INFO_GUILD` role 1, the guild leader, not ordinary-member role 2.
  The selection/confirmation states eventually call `115547D0(0)`.
- Special Poogie names are client-local fixed text. No extra name fields or
  rename operation are introduced.

## Server behavior

Migration `0060` adds `guilds.diva_pugi_outfit_1..3`, default 0, constrained
to `0..9`. They survive the end of an event and are not copied from or reset
with ordinary clothes. Reapplying the migration preserves stored choices.
`GuildRepository.Save` deliberately does not update these fields, so an old
ordinary metadata snapshot cannot replace them.

Only the verified ZZ operation is enabled. The handler rejects malformed or
out-of-range 32-bit IDs before narrowing them, disabled/other forced phases,
and client-only time shifts. The repository rechecks the actual latest round,
real welcome-song period, accepted membership, current guild leader, and at
least one acquired map area inside a transaction. An appointed leader need not
personally have earned points: the special hall is a guild entitlement.

Locks follow character → event lifecycle → guild → membership → map. The
guild-before-membership order matches disband/mission operations. Database
time is sampled again after waits; an expired request is rejected. Only the
selected special clothing column is updated, with no item/money mutation.
Success/failure uses the existing simple ACK; no new client patch is needed.

## Verification boundary

Pure tests cover clothing bounds, operation/ACK mapping, malformed data,
mode/clock guards, and independent `INFO_GUILD` bytes including pre-Z1 length.
Isolated-database tests cover independent ordinary saves, all ten clothes,
actual-period boundaries, hall unlock, accepted membership, leader transfer,
and persistence after expiry. Migration tests cover defaults, constraints,
and recovery reapplication. These tests do not access the operating database.

In-game confirmation is still needed: change each Poogie as the leader,
reopen/relog to confirm persistence, inspect from another guild member, and
verify the ordinary guild hall remains unchanged. Native feeding/song effects
are separate from this clothing-state change and are not reimplemented here.
