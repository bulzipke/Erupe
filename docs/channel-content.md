# Per-channel collaboration and MezFes settings

`Entrance.Entries[].Channels` now accepts two optional content overrides:

| Channel option | Value | Behavior |
| --- | --- | --- |
| `CollabEvent` | omitted or `""` | Inherit the world's `CollabEvent`, including legacy global flags if that is also empty. |
| `CollabEvent` | `"none"` | Ordinary channel: no collaboration NPC tunes or collaboration quests, even if the world/global setting enables them. |
| `CollabEvent` | `"kaiji"`, `"higanjima"`, `"nier"`, `"evangelion"`, `"pso2"` | Fixed collaboration; never rerolls. |
| `CollabEvent` | `"random"` | Join the world's shared random rotation, even if the world's default is `none`. |
| `MezFesMinigame` | omitted or `0` | Preserve existing behavior: world-shared random selection if enabled, otherwise global `MezFesSwitchMinigame`. |
| `MezFesMinigame` | `2` | Always Tokotoko Partnya (first of the two group games). |
| `MezFesMinigame` | `4` | Always Volpakkun Together (second group game). |

The game numbers are native **IDs**, not consecutive menu numbers: `1` is not a
valid group-game override. Unsupported values are rejected at server startup.
Fixed games are accepted only in a Type 6 world with a Z2/ZZ client.
Collaboration delivery still requires Type 1 (open/free); this has not moved to
Type 2. Existing configurations without these keys retain their behavior.

Only random channels share their respective world's random occupancy count.
Fixed/ordinary channels do not keep a random draw alive. Fixed MezFes channels
also do not join a random game counter when random mode is enabled for other
channels of the same world.

## Recommended fixed layout

[Example JSON](examples/channel-content.json) is a **configuration fragment**, not
a complete replacement for `config.json`. Merge `GameplayOptions` and replace
only the existing open/free and MezFes entries, leaving all other worlds/settings
in place. Keep the original entry order. The example preserves the current first
channel ports (`53314`, `53318`) and uses unused example ports `53319`–`53324` for
the additional channels. Check for port conflicts and publish/allow the new TCP
ports in Docker, NAT and the firewall as applicable.

| World | Channel | Setting |
| --- | --- | --- |
| 자유 | 1 | `CollabEvent: "none"` |
| 자유 | 2 | `CollabEvent: "kaiji"` |
| 자유 | 3 | `CollabEvent: "higanjima"` |
| 자유 | 4 | `CollabEvent: "nier"` |
| 자유 | 5 | `CollabEvent: "evangelion"` |
| 자유 | 6 | `CollabEvent: "pso2"` |
| 메제페스 | 1 | `MezFesMinigame: 2` |
| 메제페스 | 2 | `MezFesMinigame: 4` |

Set each world's default `CollabEvent` to `none` so any additional channel without
an override stays ordinary. For all-fixed MezFes channels,
`GameplayOptions.MezFesRandomMinigame: false` makes that intent explicit; fixed
overrides also take precedence if it remains `true`.

Recommended **world** descriptions:

- 자유: `1 일반 / 2~6 콜라보 고정`
- 메제페스: `1채널 게임1 / 2채널 게임2`

Description is shared by the whole world; it is not a per-channel text field in
the current entrance protocol. Korean is already encoded as CP949. The ZZ text
field holds name + NUL + description + final NUL within 65 bytes. These two
examples use 30 and 35 bytes including their names and terminators. Keep them
short; the existing byte truncation can split a Hangul character if overfilled.

## Client compatibility and channel moves

Fixed MezFes uses the existing custom tune `0x7ffe` (`32766`) with value `2` or `4`.
Install a Vorbis version containing `mezfes_rotation.inl` (the R297 change or a
later build containing it); stock clients ignore the signal and keep their
launcher-selected game. No new client patch or new packet format is added here.
The hook is common to HD/non-HD and 30/60 FPS.

When any enabled fixed MezFes channel exists, every Z2/ZZ channel sends the
selection/fallback signal. This restores the normal launcher-equivalent choice
after leaving a fixed channel, including when random mode is disabled.
Solo games, weekly tickets and event dates are not changed.

Collaboration NPC flags and quest lists use the same effective channel event.
The native ZZ tune parser clears its collaboration tables before each reply
(HD `115119e0` -> `11511850`; non-HD `114eb140` -> `114eafb0`), then applies the
current flags. A normal channel therefore omits all collaboration flags instead
of preserving the previous channel's flag. Existing custom EVA/PSO2 client
behavior and content resources remain prerequisites.

Source changes require rebuilding and restarting Erupe. This change does not
edit the deployed NAS configuration, change the database, deploy a DLL or restart
the service. In-game switching still needs a live smoke test after deployment.
