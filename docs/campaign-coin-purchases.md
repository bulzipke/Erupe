# Premium-kit gold G coin purchases

Migration 0086 adds `campaign_coin_prices`, `campaign_coin_purchases`, and `campaign_coin_rewards`. Existing category 6/7 kits get price 50; the campaign seed also initializes prices after fresh installation without replacing operator changes. Prices are database data, not DLL constants.

Use `SELECT c.id,c.title,p.price FROM campaign_coin_prices p JOIN campaigns c ON c.id=p.campaign_id ORDER BY c.id;` to inspect prices. Change a price with `UPDATE campaign_coin_prices SET price=50 WHERE campaign_id=<id>;` (allowed range 1..65535). A confirmation using an old price is rejected, never silently charged at a new price.

The server owns the account-wide `users.gacha_premium` balance. A transaction locks the authenticated account, verifies character ownership, campaign scope, time, quote, supported catalog and pending rewards, debits the balance and snapshots that purchase's rewards. A nonce is unique per account; a committed duplicate returns success without another debit. A deliberate repeat confirmation creates a new nonce and purchase after the previous rewards have all been collected. Original legacy entitlement/claim history is not deleted.

Purchase reward receipt IDs are independent from catalog IDs (positive signed 32-bit range 0x40000000..0x7fffffff). Claiming is account/character scoped and transactional; retries do not alter another purchase. Ordinary campaigns retain their old flow. Legacy reusable codes no longer unlock priced kits, but already-authenticated unclaimed rewards remain collectible without payment.

Requires the R303 client purchase UI (HD/non-HD, 30/60fps) together with this server. No packet length or opcode changes: the unused event DWORD carries marked price data; the existing code field carries a price and nonce. Server simple ACK statuses 0x434f0001..4 distinguish shortage, changed price, pending rewards, and unavailable campaigns.

Current live catalog: 77 purchasable kits (IDs 1900000001..1900000077), with 2,195 reward rows. All prices are rounded upward to multiples of five: 71 kits at 30, two at 50, one at 25, one at 10, and two at 5 account-wide gold G coins. The prior 42 price rows are retained with expired campaigns and no category links, not sold alongside the new catalog. Existing course and gacha-Poogie-clothing exclusions remain in effect. Special-hunting quests 40191/40192 were also published; runtime ticket consumption and the series ticket-to-G-upgrade exchange still need an in-game check.

Catalog presentation was updated on 2026-10-21: original kits retain collaboration/general grouping and sort within each group by the displayed name. The collaboration block has 13 external-IP products and two Yasushi Nirasawa design collaborations (Divol and Genome); Vampire belongs to Vampire Hunter. Package kits start with starter, special-hunting-quest, and Gudan sets; the remaining items follow the previously reviewed anniversary/NPC/general order. Native category IDs 6/7, campaign IDs, prices, reward rows, link IDs and purchase history were preserved. The server emits links in `campaign_category_links.id` order and reads them for each enumeration; no server rebuild or restart is needed.

Requested product-label changes were synchronized to the localization glossary and Redis; equipment names were not changed. Historical research, applied manual SQL and rollback backups are operator records archived outside the source checkout, not server runtime dependencies.

## Read-only kit contents (R309)

With the updated server and R309 DLL, Z's existing event-information window shows the selected priced kit's full database reward catalog, eight items per page, current price, translated in-game item names and quantities. Left/right logical menu actions change pages; native confirm/cancel/mouse-close behavior returns to the list. Ordinary unpriced event information, the three-second purchase guard and paid-reward collection are unchanged.

The existing enumerate-item request (009b) uses `campaign_id | 0x80000000` to request a preview. A positive PostgreSQL INT campaign ID cannot carry this bit. The response is `KPV1`, big-endian uint16 price/count, then uint16 item ID/quantity pairs. It contains no reward or receipt IDs and is never passed to the native item-grant parser. One read-only SELECT validates character ownership, category 6/7, supported item rewards and sale dates; it does not change balances, entitlements, stamps or claim state. Price and items use the same statement snapshot. Replies are bounded by the native sender's 16 KiB capacity (4,094 rows); oversized/empty/unsupported catalogs are rejected, never partially shown. Pending or partly collected rewards do not filter the preview.

The DLL owns its ACK and static buffer, consumes only that ACK, and uses the public locked cancel when closing/reopening. Module-generation reset never calls old-image code. Old servers reject the marked request and the new UI displays a Korean loading failure; they cannot accidentally return collectible preview items. No new opcode, inventory protocol, database migration or production data update is required. Deploying the server code requires the operator's normal rebuild/restart; the DLL is installed manually by the user.

Verified with isolated localhost:5433/erupe_test PostgreSQL: `go test -mod=readonly -vet=off ./server/channelserver -run Campaign -count=1 -timeout=3m`, also with `-race`. Production was accessed read-only to compare all 77 products and 2,195 reward rows against the new query: largest response 176 bytes / six pages, all item IDs within the native name table. No deployment or in-game visual test was performed by the agent.

Inventory continues to use the original client grant/save protocol. Coin debit and durable entitlement are atomic, but this does not redesign the whole game's inventory/crash-save model.

Run `go test -vet=off ./server/channelserver -run Campaign -count=1` against an isolated localhost:5433/erupe_test PostgreSQL only. Existing unrelated vet failure in diva_area_ranking_test.go is why this focused command disables vet. Do not point the destructive test helpers at production.
