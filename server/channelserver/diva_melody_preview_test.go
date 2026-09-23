package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

type melodyPreviewRepo struct {
	melodyMockRepo
	eligibilityCalls int
}

func (r *melodyPreviewRepo) CanUseDivaMelodyShop(userID, charID uint32, mode int) (bool, error) {
	r.eligibilityCalls++
	return false, errors.New("purchase eligibility is not catalog visibility")
}

func TestDivaMelodyPreviewDoesNotRequirePurchaseEligibility(t *testing.T) {
	for _, mode := range []int{-1, 1, 2, 3} {
		s := createMockSession(5, createMockServer())
		s.userID = 7
		s.server.erupeConfig.RealClientMode = cfg.ZZ
		s.server.erupeConfig.DebugOptions.DivaOverride = mode
		r := &melodyPreviewRepo{melodyMockRepo: melodyMockRepo{err: errDivaMelodyUnavailable}}
		s.server.divaRepo = r
		// Native preview 107a4a20 and actual shop 107bbcb0 use the same 9/0 request.
		handleMsgMhfEnumerateShop(s, &mhfpacket.MsgMhfEnumerateShop{AckHandle: 19, ShopType: 9, ShopID: 0, Limit: 512})
		a := readAck(t, s)
		if a.ErrorCode != 0 || !a.IsBufferResponse || a.AckHandle != 19 ||
			len(a.Payload) != 4+76*30 || !bytes.Equal(a.Payload[:4], []byte{0, 76, 0, 76}) {
			t.Fatalf("mode %d hid the catalog: %+v", mode, a)
		}
		if r.eligibilityCalls != 0 || r.uses != 0 {
			t.Fatalf("mode %d queried or changed purchase state: %+v", mode, r)
		}
		for i, item := range divaMelodyShopItems() {
			row := a.Payload[4+i*30 : 4+(i+1)*30]
			if binary.BigEndian.Uint32(row[4:8]) != item.ItemID ||
				binary.BigEndian.Uint32(row[8:12]) != item.Cost ||
				binary.BigEndian.Uint16(row[12:14]) != item.Quantity {
				t.Fatalf("mode %d row %d lost item/cost/quantity: %x", mode, i, row)
			}
		}
		// Seeing the catalog must not grant a successful USE response.
		handleMsgMhfUseUdShopCoin(s, &mhfpacket.MsgMhfUseUdShopCoin{AckHandle: 20, Cost: 1})
		a = readAck(t, s)
		if a.ErrorCode == 0 || a.IsBufferResponse || a.AckHandle != 20 || r.uses != 1 {
			t.Fatalf("mode %d preview granted purchase: %+v %+v", mode, a, r)
		}
	}
}

func TestDivaMelodyPreviewSessionAndShopScope(t *testing.T) {
	for _, tc := range []struct {
		name               string
		client             cfg.Mode
		mode               int
		char, user, shop   uint32
		shifted, wantItems bool
	}{
		{name: "static catalog needs no repository", client: cfg.ZZ, mode: 2, char: 5, user: 7, wantItems: true},
		{name: "unsupported client", client: cfg.Z1, mode: 2, char: 5, user: 7},
		{name: "disabled", client: cfg.ZZ, mode: 0, char: 5, user: 7},
		{name: "no character", client: cfg.ZZ, mode: 2, user: 7},
		{name: "no user", client: cfg.ZZ, mode: 2, char: 5},
		{name: "other shop", client: cfg.ZZ, mode: 2, char: 5, user: 7, shop: 1},
		{name: "shifted clock", client: cfg.ZZ, mode: 2, char: 5, user: 7, shifted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := createMockSession(tc.char, createMockServer())
			s.userID = tc.user
			s.server.erupeConfig.RealClientMode = tc.client
			s.server.erupeConfig.DebugOptions.DivaOverride = tc.mode
			s.server.divaRepo = nil
			if tc.shifted {
				hour := 12
				s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour = &hour
			}
			handleMsgMhfEnumerateShop(s, &mhfpacket.MsgMhfEnumerateShop{AckHandle: 1, ShopType: 9, ShopID: tc.shop, Limit: 512})
			a := readAck(t, s)
			count := 0
			if tc.wantItems {
				count = 76
			}
			if a.ErrorCode != 0 || !a.IsBufferResponse || len(a.Payload) != 4+count*30 ||
				int(binary.BigEndian.Uint16(a.Payload[:2])) != count || int(binary.BigEndian.Uint16(a.Payload[2:4])) != count {
				t.Fatal(a)
			}
		})
	}
}
