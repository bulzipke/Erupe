# Premium-kit gold G coin purchases

Migration 0086 adds `campaign_coin_prices`, `campaign_coin_purchases`, and `campaign_coin_rewards`. Existing category 6/7 kits get price 50; the campaign seed also initializes prices after fresh installation without replacing operator changes. Prices are database data, not DLL constants.

Use `SELECT c.id,c.title,p.price FROM campaign_coin_prices p JOIN campaigns c ON c.id=p.campaign_id ORDER BY c.id;` to inspect prices. Change a price with `UPDATE campaign_coin_prices SET price=50 WHERE campaign_id=<id>;` (allowed range 1..65535). A confirmation using an old price is rejected, never silently charged at a new price.

The server owns the account-wide `users.gacha_premium` balance. A transaction locks the authenticated account, verifies character ownership, campaign scope, time, quote, supported catalog and pending rewards, debits the balance and snapshots that purchase's rewards. A nonce is unique per account; a committed duplicate returns success without another debit. A deliberate repeat confirmation creates a new nonce and purchase after the previous rewards have all been collected. Original legacy entitlement/claim history is not deleted.

Purchase reward receipt IDs are independent from catalog IDs (positive signed 32-bit range 0x40000000..0x7fffffff). Claiming is account/character scoped and transactional; retries do not alter another purchase. Ordinary campaigns retain their old flow. Legacy reusable codes no longer unlock priced kits, but already-authenticated unclaimed rewards remain collectible without payment.

Requires the R303 client purchase UI (HD/non-HD, 30/60fps) together with this server. No packet length or opcode changes: the unused event DWORD carries marked price data; the existing code field carries a price and nonce. Server simple ACK statuses 0x434f0001..4 distinguish shortage, changed price, pending rewards, and unavailable campaigns.

Current live catalog: 42 prices, 38 kits with ordinary material rewards. The following four have no reward rows and cannot be purchased (no debit): vampire 125278872, waist 615934227, white-cat outfit 766534701, meist 1032922449. Their reward data was not guessed.

Inventory continues to use the original client grant/save protocol. Coin debit and durable entitlement are atomic, but this does not redesign the whole game's inventory/crash-save model.

Run `go test -vet=off ./server/channelserver -run Campaign -count=1` against an isolated localhost:5433/erupe_test PostgreSQL only. Existing unrelated vet failure in diva_area_ranking_test.go is why this focused command disables vet. Do not point the destructive test helpers at production.
