# Diva song-phase implementation notes (2026-09-21)

Verified against Ghidra program mhfo-hd.dll.clean.mapped.bin (ZZ):

| Function | Finding |
| --- | --- |
| 0x114fd370 | SetKiju sends one color byte, not u16. |
| 0x11534740 | MyPoint starts with status, then eight 18-byte daily entries. Each entry contains two color/u32-base/u32-bonus triplets. |
| 0x1039d620 | Displayed totals sum those fields; duplicating points duplicates totals. |
| 0x11535ed0 / 0x103b4a50 | The active personal/guild screen requests kinds 0/2; both read 100 entries of reserved u8 + displayed rank u8 + name[25] + points u32. |
| 0x11535fb0 | Unused kind 1/3 paths read 47-byte entries; their semantics remain unverified, so those responses stay empty. |
| 0x115361e0 | MyRanking reads six u32 fields and a 25-byte guild name (49 bytes). |
| 0x114fe470 | MyRanking requests a 49-byte buffer. |
| 0x103abae0 / 0x11536890 | SetKiju recognizes payload 0xF9 (-7) as a refusal; payload 1 incorrectly follows the success UI. |
| 0x103aba40 | A nonzero second daily color consumes the change right. Published winner count gates subsequent days. |
| 0x1039d720 / 0x1039d800 | Active color is second then first; if absent today, scan earlier days backwards. |

## Bead correction (2026-09-21)

Migration 0043 introduces event-scoped daily choices. The first choice is free;
one different choice fills the second slot. Selecting the active color again is
idempotent. At UTC+9 noon the active color carries into the next day, but the
second slot resets. Contributions and selections share the character row lock.
Both color triplets are returned without moving pre-change points to the new
color. Legacy assignments retain their last color; their schema did not track
whether a change right had been consumed, so migration does not invent that fact.

Winner colors now refer to completed event-relative noon windows, not reversed
rolling 24-hour totals from unrelated events. Premium bonus points are excluded
from this color settlement. Ties among recorded colors use the lower color ID
(deterministic emulator ordering, not a verified retail tie-break rule). Empty
days remain zero; no winning color or prayer level is fabricated. The retail UI
counts nonzero winner slots, so a completely empty completed event day can still
block its later change gate. The retail empty-day settlement rule needs evidence
before changing that behavior.
No live DB changes or deployment were made by this patch.

Migration 0042 adds an immediate contribution journal, publication cutoff support,
and a persistent forced-song-phase anchor. It repairs known 256/512/768/1024
legacy color values and converts old selection+24h expiries to next UTC+9 noon.
It does not invent missing contributions. Pre-migration aggregate-only points
are not backfilled into historical daily/ranking records without timestamps.

Personal and guild ranking publication cutoffs follow the requested UTC+9 04:00/12:00/20:00
schedule, with an additional 18:00 cutoff on the first date. The user explicitly
kept 18:00 after the historical official 18:30 rule was recovered. Contributions are
stored immediately and the personal point display does not wait for publication.
Publication is calculated at query time; no background timer or destructive reset.

Ranking kinds 0 (personal) and 2 (guild) now use real contribution totals. Guild
attribution is snapshotted on new submissions; old un-attributed records are not
assigned retroactively to present guild membership. Unknown kind 1/3 lists and
the unverified extra own-rank fields remain zero. Round-40 GR daily and personal-ranking
reward lists now have eligibility checks and receipts committed with the matching
item/GP save. Interception personal rewards now use new-round contribution records
(see diva-interception-rounds.md); guild-area eligibility is still unimplemented.
Norma/HR-daily/guild-ranking rewards, prayer effects and map-based interception
rankings remain incomplete: this does not complete DivaOverride=1.
See diva-round40-applied.md for verified versus inferred rows and remaining gaps.

Bonus-target transport has since been implemented for explicit ZZ schedules
(see diva-bonus-targets.md), plus the opt-in DivaBonusRandom four-color, three-hour
UTC+9 schedule. The default stays empty: round-40 target multipliers are not
recovered yet. QuestPoints already includes the client's target bonus;
BonusPoints is the separate Premium-course addition, not the prayer buff.

DivaOverride=1 now retains its start date across midnight/restarts for one song
phase. After that phase expires it creates another forced song event, retaining
old records. Overrides 2/3 now also retain their phase anchors across midnight
and restarts, with non-destructive round renewal (see diva-interception-rounds.md).
Changing an override still requires a server restart.

After deployment, reconnect before testing. A submitted 60 points should produce
an INFO "Diva song points saved" record with questPoints=60. If absent, capture
MSG_MHF_ADD_UD_POINT and the client phase/point accumulator path; do not compensate
by issuing guessed points. Existing production data was inspected read-only;
the migration is applied on the next startup of the updated server.
