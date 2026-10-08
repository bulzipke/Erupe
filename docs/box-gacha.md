# Box gachas (ZZ client)

How the ZZ client draws a box gacha, and how the server matches it. Client
addresses are mhfo-hd.dll (unpacked image); the same logic is in mhfo.dll.

## What the client expects

- **Reward entries are balls.** The client adds the `weight` of every reward
  entry (`entry_type` 100) of a box gacha (`gacha_type` 4 or 5) into the box's
  ball total (menu object +0x8b96). The official New Year boxes had 58 prizes
  in 242 balls this way.
- **Drawn counts.** `GET_BOX_GACHA_INFO` is one count byte and up to 64
  records of `entry_id` (u32) + drawn count (u8), read into a 322-byte buffer
  (`FUN_11532750`). The client subtracts each entry's drawn count from its
  weight to show what is left, and sums the counts into the drawn total
  (+0x8b98).
- **Empty box.** When the total equals the drawn count, closing the result
  screen sends `RESET_BOX_GACHA_INFO` without asking (state 0x2a), and a roll
  that needs more balls than are left asks to reset first (state 0x24). The
  last cell of the lineup grid (state 0x34) is a reset button.
- **Limits.** At most 64 reward entries per gacha, up to five items each; a
  ball count fits one byte (1-255 per entry).

## Server behaviour

- A box entry's `weight` is its ball count, clamped to 1-255 (`gachaBoxBallCount`);
  the shop detail sends it as the entry weight.
- `gacha_box` keeps one row per drawn ball. A roll draws its `rolls` balls
  without replacement from the balls left; if fewer are left than the roll
  draws, the roll is refused before its cost is paid.
- `GET_BOX_GACHA_INFO` groups the rows by entry.

## One-time boxes

Set `gacha_shop.one_time = true` on a box gacha (migration 0077) to let each
character draw it once:

- `RESET_BOX_GACHA_INFO` fails for it (the client shows its communication
  error and closes the menu).
- The shop list byte after `gacha_type` carries bit 0x02 for it (bit 0x01 is
  `hidden`). The client stores that byte at entry +0x1c4 and only tests it in
  the normal-gacha list pages, which never hold box gachas. vorbis.dll (D570)
  reads the bit: the emptied box stays shown as drawn (no automatic reset),
  the reset button is hidden, and a roll needing more balls than are left does
  nothing.

Progress is per character, like ordinary boxes.

## Reward types the client handles itself

- **Zenny (item type 10)** is credited by the client when it shows the result
  and is left out of the client's pending-box list, so the server does not
  store it in `gacha_items` (a stored zenny behind the last visible item
  could never be received).
- **Costs.** The client pays cost types 7 (items), 10 (zenny), 12/13, 14 and 16
  from the character save before sending the roll (`FUN_1046d200`); the
  server deducts 17 (net cafe points), 19/20 (gacha coins) and 21 (frontier
  points) and refuses any other cost type.
