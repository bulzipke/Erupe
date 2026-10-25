# Seasonal quest supplies

This package prepares local standard-item supply intents. It has no database,
network, campaign allocator, live activation, currency writer, inventory writer,
or claim/save handler. It does not implement a permanent quest license.

`NewApprovedSeasonalDraft` builds the approved
`season-supplies-approved-20261024-v1` policy:

| Key | Local policy | Original quest requirements |
| --- | --- | --- |
| `gw_ticket` | Item type 7, item 6831, four free tickets once per character per approved GW season window | 50070–50073, one ticket each |
| `hunter_dice_bag` | Item type 7, item 3638, one bag for 1 gold G per repeatable purchase in its approved new-year window | 51032–51035 and 55620–55624, one bag each |

The factory has no window identities or dates. `Prepare` therefore returns
`ErrUnknownWindow` until a separately approved window is bound in a new local
catalog. Even a preparation with a test window returns `LiveEnabled=false` and
`CampaignID=0`. Native inventory/save validation is still outstanding.

`New` validates definitions, nonzero standard-item quantities, paid prices in
1–65535, quest mappings, globally unique window IDs, and nonoverlapping windows
for the same supply. Windows use `[start,end)`; touching windows are allowed.
Catalogs and prepared intents own their slices, and every accessor copies them.
Canonical SHA-256 definition digests cover the semantic policy. Catalog digests
also cover version and window bindings.

`Prepared.FreeClaimScope` is the durable uniqueness contract
`(CharacterID, WindowID)`. Date edits, catalog-version changes, reconnects,
restarts, campaign aliases and request nonces must not create another free claim
within that same approved window. Historical window identities must be retained
by the eventual persistent adapter. A later approved annual window can have a
different stable ID; a rolling material rotation cannot mint one automatically.

`Prepared.PaidReplayContract` separates the uniqueness scope `(AccountID, Nonce)`
from the request content digest, which binds the character, supply, season
identity and semantic policy. Reusing a nonce for different content conflicts;
a new nonce describes a new purchase. `ComparePaidReplay` rejects empty or
partial contracts. A future adapter must consult its committed receipt before
fresh-window validation and return an exact receipt after closure without
charging or granting again.

These are contracts, not implemented persistence guarantees. A future adapter
must authenticate the account and character, preserve existing histories,
atomically charge/record/stage delivery, and verify the native full-box, cancel,
retry and saved-inventory behavior. Original rank, equipment, party/carry,
consumption/failure, progression and reward/completion rules remain in force.
