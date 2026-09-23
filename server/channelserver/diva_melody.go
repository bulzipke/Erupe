package channelserver

import (
	"errors"
	"fmt"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

const divaMelodyItemType uint8 = 29
const divaMelodyMax uint8 = 10

var errDivaMelodyUnavailable = errors.New("diva melody unavailable or insufficient balance")

type DivaMelodyRepository interface {
	GetDivaMelody(userID, charID uint32, override int) (uint8, error)
	UseDivaMelody(userID, charID uint32, cost uint8, override int) (uint8, error)
	CanUseDivaMelodyShop(userID, charID uint32, override int) (bool, error)
}

// GR: lower milestones follow round 19 (1..4) and round 38 (5); 6..10
// are directly visible in round-40 screenshots. HR is a user-approved server
// custom rule at half those thresholds, NOT a recovered historical HR table.
func divaMelodyRewards() []DivaRewardCatalogEntry {
	points := [...]uint32{14000, 34000, 54000, 75000, 84000, 94000, 104000, 114000, 124000, 134000}
	rows := make([]DivaRewardCatalogEntry, 2*len(points))
	for i, p := range points {
		basis := "round40-direct"
		if i < 4 {
			basis = "round19-inferred"
		} else if i == 4 {
			basis = "round38-inferred"
		}
		rows[i] = DivaRewardCatalogEntry{Key: fmt.Sprintf("melody-cap-%d", i+1), RewardType: 6,
			Threshold: p, GR: true, ItemType: divaMelodyItemType, Quantity: uint16(i + 1), Basis: basis}
		// Keep the existing GR keys and immutable receipts unchanged. Both
		// ranks credit the same account cap, never additive per-rank balances.
		rows[len(points)+i] = DivaRewardCatalogEntry{Key: fmt.Sprintf("custom-hr-melody-cap-%d", i+1), RewardType: 6,
			Threshold: p / 2, ItemType: divaMelodyItemType, Quantity: uint16(i + 1), Basis: "server-custom-hr-half-gr"}
	}
	return rows
}

func divaMelodyDisplay() []DivaPrize {
	var rows []DivaPrize
	for _, r := range divaMelodyRewards() {
		rows = append(rows, DivaPrize{Type: "personal", PointsReq: int(r.Threshold),
			ItemType: int(divaMelodyItemType), Quantity: int(r.Quantity), GR: r.GR})
	}
	return rows
}

// The final official catalog supplies all 76 names/prices; Ferias supplies IDs.
// Single-unit quantities follow contemporary exchange reports. Some individual
// tickets/silks are family-level inferences, NOT direct round-40 observations;
// see docs/diva-melody-catalog-restoration.md. No runtime web data is required.
func divaMelodyShopItems() []ShopItem {
	items := make([]ShopItem, 0, 76)
	for _, first := range [...]uint32{0x26d3, 0x26da, 0x2fb5, 0x2fba, 0x31d2, 0x31d7, 0x33a2, 0x33a7} {
		for id := first; id < first+5; id++ {
			items = append(items, ShopItem{ID: id, ItemID: id, Cost: 1, Quantity: 1})
		}
	}
	// Official order: 26 exterior tickets after the 40 decorations.
	for _, id := range [...]uint32{
		0x36c7, 0x36bc, 0x36bd, 0x36c4, 0x36d2, 0x36d1, 0x36c6, 0x36d8,
		0x3728, 0x3729, 0x372a, 0x3725, 0x3726, 0x3727,
		0x3b58, 0x3b59, 0x3b5a, 0x3b5b, 0x3b5c, 0x3c62, 0x3c63,
		0x4095, 0x4096, 0x4097, 0x3853, 0x3855,
	} {
		items = append(items, ShopItem{ID: id, ItemID: id, Cost: 1, Quantity: 1})
	}
	// Ten armor-production materials, each costing two melodies.
	for _, id := range [...]uint32{
		0x3696, 0x37db, 0x38c9, 0x39a6, 0x3a06,
		0x3adc, 0x3b7a, 0x4048, 0x4049, 0x404a,
	} {
		items = append(items, ShopItem{ID: id, ItemID: id, Cost: 2, Quantity: 1})
	}
	return items
}

func divaMelodyAllowedCost(cost uint8) bool {
	for _, item := range divaMelodyShopItems() {
		if uint32(cost) == item.Cost {
			return true
		}
	}
	return false
}

func divaMelodyUnexpired(event DivaEvent, now time.Time) bool {
	start, _ := divaInterceptionWindow(event)
	end := time.Unix(int64(event.StartTime)+divaPhaseDuration+2*divaWeekDuration, 0)
	return event.ID != 0 && !now.Before(start) && now.Before(end)
}

// 11501860 converts the four simple-ACK wire bytes to a big-endian uint32;
// 115372b0 then reads its low byte. The wire is 00 00 00 balance, not balance 00 00 00.
func divaMelodyPayload(balance uint8) []byte {
	bf := byteframe.NewByteFrame()
	bf.WriteUint32(uint32(balance))
	return bf.Data()
}

func divaMelodySessionEnabled(s *Session) bool {
	options := s.server.erupeConfig
	return options.RealClientMode == cfg.ZZ && s.charID != 0 && s.userID != 0 &&
		options.DebugOptions.DivaOverride != 0 && options.DebugOptions.InGameTimeOverrideHour == nil
}

func divaMelodySessionRepo(s *Session) (DivaMelodyRepository, bool) {
	if !divaMelodySessionEnabled(s) {
		return nil, false
	}
	r, ok := s.server.divaRepo.(DivaMelodyRepository)
	return r, ok
}

func getDivaMelody(s *Session, ack uint32) {
	r, ok := divaMelodySessionRepo(s)
	if !ok {
		doAckSimpleSucceed(s, ack, divaMelodyPayload(0))
		return
	}
	balance, err := r.GetDivaMelody(s.userID, s.charID, s.server.erupeConfig.DebugOptions.DivaOverride)
	if err != nil || balance > divaMelodyMax {
		s.logger.Warn("Failed to read Diva melody balance", zap.Error(err))
		doAckSimpleFail(s, ack, make([]byte, 4))
		return
	}
	doAckSimpleSucceed(s, ack, divaMelodyPayload(balance))
}

func useDivaMelody(s *Session, p *mhfpacket.MsgMhfUseUdShopCoin) {
	r, ok := divaMelodySessionRepo(s)
	if !ok || !divaMelodyAllowedCost(p.Cost) {
		doAckSimpleFail(s, p.AckHandle, make([]byte, 4))
		return
	}
	balance, err := r.UseDivaMelody(s.userID, s.charID, p.Cost, s.server.erupeConfig.DebugOptions.DivaOverride)
	if err != nil || balance > divaMelodyMax {
		if err != nil && !errors.Is(err, errDivaMelodyUnavailable) {
			s.logger.Warn("Failed to spend Diva melody", zap.Error(err))
		}
		doAckSimpleFail(s, p.AckHandle, make([]byte, 4))
		return
	}
	// The native client grants the selected item after this success. Do not
	// grant another copy here, and do not replay a success as an idempotent ACK.
	doAckSimpleSucceed(s, p.AckHandle, divaMelodyPayload(balance))
}

func enumerateDivaMelodyShop(s *Session, p *mhfpacket.MsgMhfEnumerateShop) {
	bf := byteframe.NewByteFrame()
	var items []ShopItem
	// The read-only interception preview (107a4a20) and the actual exchange
	// (107bbcb0) both enumerate 9/0 and share one native catalog cache. Gating
	// this list on welcome-period/hall access hides the preview during battle.
	// USE_UD_SHOP_COIN still checks the real period, membership and wallet.
	if divaMelodySessionEnabled(s) && p.ShopID == 0 {
		items = divaMelodyShopItems()
	}
	if len(items) > int(p.Limit) {
		items = items[:p.Limit]
	}
	writeShopItems(bf, items, s.server.erupeConfig.RealClientMode)
	doAckBufSucceed(s, p.AckHandle, bf.Data())
}
