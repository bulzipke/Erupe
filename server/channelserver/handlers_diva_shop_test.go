package channelserver

import (
	"testing"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

type contextShopRepo struct {
	mockShopRepo
	loads, batches, mode int
	charID, shopID       uint32
	batch                []exchangeShopPurchase
}

func (r *contextShopRepo) GetDivaShopItems(shopID, charID uint32, mode int) ([]ShopItem, error) {
	r.loads++
	r.shopID, r.charID, r.mode = shopID, charID, mode
	return r.shopItems, r.shopItemsErr
}

func (r *contextShopRepo) RecordExchangeShopPurchases(charID uint32, batch []exchangeShopPurchase, mode int) error {
	r.batches++
	r.charID, r.mode, r.batch = charID, mode, batch
	return r.recordErr
}

func TestDivaShopHandlerCatalogAndContext(t *testing.T) {
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.erupeConfig.DebugOptions.DivaOverride = 3
	repo := &contextShopRepo{}
	for i := 0; i < 131; i++ {
		repo.shopItems = append(repo.shopItems, ShopItem{ID: uint32(1000 + i), ItemID: uint32(i + 1), MaxQuantity: 5, UsedQuantity: 3})
	}
	srv.shopRepo = repo
	s := createMockSession(7, srv)
	handleMsgMhfEnumerateShop(s, &mhfpacket.MsgMhfEnumerateShop{AckHandle: 100, ShopType: 8, ShopID: 7, Limit: 512})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || len(ack.Payload) != 4+131*30 {
		t.Fatalf("catalog acknowledgement: %+v", ack)
	}
	bf := byteframe.NewByteFrameFromBytes(ack.Payload)
	if bf.ReadUint16() != 131 || bf.ReadUint16() != 131 || bf.ReadUint32() != 1000 {
		t.Fatal("ZZ catalog count/hash changed")
	}
	if repo.loads != 1 || repo.charID != 7 || repo.shopID != 7 || repo.mode != 3 {
		t.Fatalf("missing round context: %+v", repo)
	}
	repo.shopItemsErr = errDivaShopUnavailable
	handleMsgMhfEnumerateShop(s, &mhfpacket.MsgMhfEnumerateShop{AckHandle: 101, ShopType: 8, ShopID: 7, Limit: 512})
	if readAck(t, s).ErrorCode == 0 {
		t.Fatal("repository error returned a successful catalog")
	}
}

func TestDivaShopHandlerAtomicBatchAndFailureAck(t *testing.T) {
	for _, reject := range []bool{false, true} {
		srv := createMockServer()
		srv.erupeConfig.DebugOptions.DivaOverride = -1
		repo := &contextShopRepo{}
		if reject {
			repo.recordErr = errDivaShopUnavailable
		}
		srv.shopRepo = repo
		s := createMockSession(7, srv)
		bf := byteframe.NewByteFrame()
		bf.WriteUint16(4)
		for _, p := range []exchangeShopPurchase{{1001, 2}, {0, 1}, {1002, 3}, {1003, 0}} {
			bf.WriteUint32(p.ItemHash)
			bf.WriteUint32(p.Quantity)
		}
		handleMsgMhfAcquireExchangeShop(s, &mhfpacket.MsgMhfAcquireExchangeShop{AckHandle: 100, RawDataPayload: bf.Data()})
		ack := readAck(t, s)
		if (ack.ErrorCode != 0) != reject {
			t.Fatalf("reject=%v acknowledgement=%+v", reject, ack)
		}
		if repo.batches != 1 || repo.charID != 7 || repo.mode != -1 || len(repo.purchases) != 0 || len(repo.batch) != 2 || repo.batch[1] != (exchangeShopPurchase{1002, 3}) {
			t.Fatalf("batch was split, duplicated, or lost context: %+v", repo)
		}
	}
}
