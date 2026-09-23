package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestDivaMelodyCatalogAndWire(t *testing.T) {
	rows := divaMelodyRewards()
	if len(rows) != 20 || rows[0].Threshold != 14000 || rows[4].Threshold != 84000 || rows[9].Threshold != 134000 {
		t.Fatal(rows)
	}
	for i, r := range rows {
		if r.Quantity != uint16(i%10+1) || r.GR != (i < 10) || r.ItemType != 29 || r.ItemID != 0 || r.RewardType != 6 {
			t.Fatal(r)
		}
		if err := validateDivaRewardCatalog(6, []DivaRewardCatalogEntry{r}); err != nil {
			t.Fatal(err)
		}
		r.RewardType = 1
		if err := validateDivaRewardCatalog(1, []DivaRewardCatalogEntry{r}); err == nil {
			t.Fatal("melody accepted as prayer reward")
		}
	}
	items := divaMelodyShopItems()
	if len(items) != 76 {
		t.Fatal(len(items))
	}
	seen := map[uint32]bool{}
	for i, item := range items {
		want := uint32(1)
		if i >= 66 {
			want = 2
		}
		if seen[item.ItemID] || item.Quantity != 1 || item.Cost != want || item.ItemID == 0 || item.ID != item.ItemID {
			t.Fatal(item)
		}
		seen[item.ItemID] = true
	}
	for _, balance := range []uint8{0, 1, 2, 10} {
		if got := divaMelodyPayload(balance); !bytes.Equal(got, []byte{0, 0, 0, balance}) {
			t.Fatalf("big-endian simple ACK %x", got)
		}
	}
	for _, cost := range []uint8{0, 3, 10, 255} {
		if divaMelodyAllowedCost(cost) {
			t.Fatal(cost)
		}
	}
	if !divaMelodyAllowedCost(1) || !divaMelodyAllowedCost(2) {
		t.Fatal("real catalog cost rejected")
	}
	row := DivaRewardOffer{RewardType: 6, ItemType: 29, Quantity: 10, CatalogKey: "melody-cap-10"}
	if !validDivaMelodyReceipt(row) {
		t.Fatal(row)
	}
	row.Quantity = 9
	if validDivaMelodyReceipt(row) {
		t.Fatal("changed snapshot accepted")
	}
}

func TestDivaMelodyHRCustomMilestonesAndPreservedGRKeys(t *testing.T) {
	wantHR := [...]uint32{7000, 17000, 27000, 37500, 42000, 47000, 52000, 57000, 62000, 67000}
	rows, display := divaMelodyRewards(), divaMelodyDisplay()
	if len(rows) != 2*len(wantHR) || len(display) != len(rows) {
		t.Fatal(rows, display)
	}
	if err := validateDivaRewardCatalog(6, rows); err != nil {
		t.Fatal(err)
	}
	for i, threshold := range wantHR {
		gr, hr := rows[i], rows[len(wantHR)+i]
		if gr.Key != fmt.Sprintf("melody-cap-%d", i+1) || gr.Threshold != threshold*2 || !gr.GR {
			t.Fatal("existing GR rule changed", gr)
		}
		if hr.Key != fmt.Sprintf("custom-hr-melody-cap-%d", i+1) || hr.Threshold != threshold || hr.GR ||
			hr.Quantity != gr.Quantity || hr.Basis != "server-custom-hr-half-gr" {
			t.Fatal("incorrect custom HR rule", hr)
		}
	}
	for i, rule := range rows {
		if got := display[i]; got.PointsReq != int(rule.Threshold) || got.GR != rule.GR ||
			got.ItemType != int(divaMelodyItemType) || got.Quantity != int(rule.Quantity) {
			t.Fatal("rank/threshold lost on display", got, rule)
		}
		receipt := DivaRewardOffer{RewardType: 6, ItemType: divaMelodyItemType, CatalogKey: rule.Key, Quantity: rule.Quantity}
		if !validDivaMelodyReceipt(receipt) {
			t.Fatal("valid immutable receipt rejected", receipt)
		}
		receipt.Quantity = uint16(rule.Quantity%uint16(divaMelodyMax)) + 1
		if validDivaMelodyReceipt(receipt) {
			t.Fatal("changed cap accepted", receipt)
		}
	}
}

func TestDivaMelodyEventBounds(t *testing.T) {
	event := DivaEvent{ID: 1, StartTime: 1900000000}
	start, _ := divaInterceptionWindow(event)
	end := time.Unix(int64(event.StartTime)+divaPhaseDuration+2*divaWeekDuration, 0)
	for _, tc := range []struct {
		now  time.Time
		want bool
	}{{start.Add(-time.Nanosecond), false}, {start, true}, {end.Add(-time.Nanosecond), true}, {end, false}} {
		if got := divaMelodyUnexpired(event, tc.now); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	if divaMelodyUnexpired(DivaEvent{}, start) {
		t.Fatal("orphan round allowed")
	}
}

type melodyMockRepo struct {
	mockDivaRepo
	balance uint8
	err     error
	shop    bool
	uses    int
	cost    uint8
}

func (r *melodyMockRepo) GetDivaMelody(userID, charID uint32, mode int) (uint8, error) {
	return r.balance, r.err
}
func (r *melodyMockRepo) UseDivaMelody(userID, charID uint32, cost uint8, mode int) (uint8, error) {
	r.uses++
	r.cost = cost
	return r.balance, r.err
}
func (r *melodyMockRepo) CanUseDivaMelodyShop(userID, charID uint32, mode int) (bool, error) {
	return r.shop, r.err
}

func TestDivaMelodyEnabledAcknowledgements(t *testing.T) {
	s := createMockSession(5, createMockServer())
	s.userID = 7
	s.server.erupeConfig.RealClientMode = cfg.ZZ
	s.server.erupeConfig.DebugOptions.DivaOverride = -1
	r := &melodyMockRepo{balance: 10, shop: true}
	s.server.divaRepo = r
	handleMsgMhfGetUdShopCoin(s, &mhfpacket.MsgMhfGetUdShopCoin{AckHandle: 1})
	a := readAck(t, s)
	if a.ErrorCode != 0 || a.IsBufferResponse || !bytes.Equal(a.Payload, []byte{0, 0, 0, 10}) {
		t.Fatal(a)
	}
	handleMsgMhfUseUdShopCoin(s, &mhfpacket.MsgMhfUseUdShopCoin{AckHandle: 2, Cost: 2})
	a = readAck(t, s)
	if a.ErrorCode != 0 || a.IsBufferResponse || r.uses != 1 || r.cost != 2 {
		t.Fatal(a, r)
	}
	for _, cost := range []uint8{0, 3, 255} {
		handleMsgMhfUseUdShopCoin(s, &mhfpacket.MsgMhfUseUdShopCoin{AckHandle: 3, Cost: cost})
		if a = readAck(t, s); a.ErrorCode == 0 || a.IsBufferResponse {
			t.Fatal(a)
		}
	}
	if r.uses != 1 {
		t.Fatal("invalid costs reached wallet")
	}
	r.err = errDivaMelodyUnavailable
	handleMsgMhfUseUdShopCoin(s, &mhfpacket.MsgMhfUseUdShopCoin{AckHandle: 4, Cost: 1})
	if a = readAck(t, s); a.ErrorCode == 0 || a.IsBufferResponse {
		t.Fatal(a)
	}
	r.err = errors.New("db unavailable")
	handleMsgMhfGetUdShopCoin(s, &mhfpacket.MsgMhfGetUdShopCoin{AckHandle: 5})
	if a = readAck(t, s); a.ErrorCode == 0 || a.IsBufferResponse {
		t.Fatal(a)
	}
	r.err = nil
	handleMsgMhfEnumerateShop(s, &mhfpacket.MsgMhfEnumerateShop{AckHandle: 6, ShopType: 9, ShopID: 0, Limit: 512})
	a = readAck(t, s)
	if a.ErrorCode != 0 || !a.IsBufferResponse || len(a.Payload) != 4+76*30 || !bytes.Equal(a.Payload[:4], []byte{0, 76, 0, 76}) {
		t.Fatal(a)
	}
	for _, shopID := range []uint32{1, 999} {
		handleMsgMhfEnumerateShop(s, &mhfpacket.MsgMhfEnumerateShop{AckHandle: 7, ShopType: 9, ShopID: shopID, Limit: 512})
		if a = readAck(t, s); a.ErrorCode != 0 || !bytes.Equal(a.Payload, make([]byte, 4)) {
			t.Fatal(a)
		}
	}
	r.shop = false
	handleMsgMhfEnumerateShop(s, &mhfpacket.MsgMhfEnumerateShop{AckHandle: 8, ShopType: 9, Limit: 512})
	if a = readAck(t, s); a.ErrorCode != 0 || len(a.Payload) != 4+76*30 || !bytes.Equal(a.Payload[:4], []byte{0, 76, 0, 76}) {
		t.Fatal(a)
	}
}

// Independent snapshot of the official non-decoration catalog. IDs were
// matched by normalized Japanese name, not guessed from consecutive ranges.
func TestDivaMelodyRestoredCatalog(t *testing.T) {
	want := [...]uint32{
		0x36c7, 0x36bc, 0x36bd, 0x36c4, 0x36d2, 0x36d1, 0x36c6, 0x36d8,
		0x3728, 0x3729, 0x372a, 0x3725, 0x3726, 0x3727,
		0x3b58, 0x3b59, 0x3b5a, 0x3b5b, 0x3b5c, 0x3c62, 0x3c63,
		0x4095, 0x4096, 0x4097, 0x3853, 0x3855,
		0x3696, 0x37db, 0x38c9, 0x39a6, 0x3a06, 0x3adc, 0x3b7a, 0x4048, 0x4049, 0x404a,
	}
	items := divaMelodyShopItems()
	if len(items) != 40+len(want) {
		t.Fatal(len(items))
	}
	for i, id := range want {
		cost := uint32(1)
		if i >= 26 {
			cost = 2
		}
		if got := items[40+i]; got != (ShopItem{ID: id, ItemID: id, Cost: cost, Quantity: 1}) {
			t.Fatalf("catalog row %d: %+v", i, got)
		}
	}
	// Callers must not be able to mutate a shared catalog across sessions.
	items[40].Quantity = 99
	if divaMelodyShopItems()[40].Quantity != 1 {
		t.Fatal("catalog aliases caller memory")
	}
}

func TestDivaMelodyShopLimitsAndLastRow(t *testing.T) {
	s := createMockSession(5, createMockServer())
	s.userID = 7
	s.server.erupeConfig.RealClientMode = cfg.ZZ
	s.server.erupeConfig.DebugOptions.DivaOverride = -1
	s.server.divaRepo = &melodyMockRepo{shop: true}
	items := divaMelodyShopItems()
	for _, limit := range []uint16{0, 1, 40, 42, 66, 75, 76, 512} {
		handleMsgMhfEnumerateShop(s, &mhfpacket.MsgMhfEnumerateShop{AckHandle: 1, ShopType: 9, Limit: limit})
		a := readAck(t, s)
		count := min(int(limit), len(items))
		if a.ErrorCode != 0 || !a.IsBufferResponse || len(a.Payload) != 4+count*30 ||
			int(binary.BigEndian.Uint16(a.Payload[:2])) != count ||
			int(binary.BigEndian.Uint16(a.Payload[2:4])) != count {
			t.Fatalf("limit %d: %+v", limit, a)
		}
		for i, item := range items[:count] {
			row := a.Payload[4+i*30 : 4+(i+1)*30]
			if binary.BigEndian.Uint32(row[:4]) != item.ID ||
				binary.BigEndian.Uint32(row[4:8]) != item.ItemID ||
				binary.BigEndian.Uint32(row[8:12]) != item.Cost ||
				binary.BigEndian.Uint16(row[12:14]) != item.Quantity {
				t.Fatalf("limit %d row %d: %x", limit, i, row)
			}
		}
	}
}
