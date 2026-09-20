package channelserver

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestDivaHistoricalNormaCatalogAndWire(t *testing.T) {
	if err := validateDivaRewardCatalog(1, divaHistoricalPrayerRewards); err != nil {
		t.Fatal(err)
	}
	wantItems := []uint16{0, 0x2c15, 0x2c75, 0x2c78, 0x22e7, 0x2caf}
	wantQuantities := []uint16{5100, 10, 5, 8, 10, 5}
	if len(divaHistoricalPrayerRewards) != len(wantItems) {
		t.Fatal("historical screenshot must contain exactly six rows")
	}
	for i, r := range divaHistoricalPrayerRewards {
		if r.ItemID != wantItems[i] || r.Quantity != wantQuantities[i] || r.Threshold != 20000 || !r.GR || r.Basis != "2017-05-24-screen-direct" {
			t.Fatalf("historical screenshot row %d changed: %+v", i, r)
		}
	}
	// 19-byte native norma row, not the 15-byte daily or 14-byte ranking row.
	want := []byte{0, 1, 26, 0, 0, 0x13, 0xec, 1, 0, 0, 3, 0xe7, 0, 0, 0, 1, 0, 0, 0x4e, 0x20, 0}
	if got := divaNormaRewardPayload(divaHistoricalPrayerRewards[:1]); !bytes.Equal(got, want) {
		t.Fatalf("norma wire = %x, want %x", got, want)
	}
	if got := divaNormaRewardPayload(diva40SongRewards); !bytes.Equal(got, []byte{0, 0}) {
		t.Fatalf("daily/ranking rewards leaked into norma: %x", got)
	}
	for _, mode := range []cfg.Mode{cfg.ZZ, cfg.Z2} {
		srv := createMockServer()
		srv.erupeConfig.RealClientMode = mode
		srv.divaRepo = nil
		s := createMockSession(56, srv)
		handleMsgMhfGetUdNormaPresentList(s, &mhfpacket.MsgMhfGetUdNormaPresentList{AckHandle: 71})
		ack := readAck(t, s)
		count := 0
		if mode == cfg.ZZ {
			count = 29 // Eleven historical GR, twelve approved HR, six real rotation rows.
		}
		if ack.ErrorCode != 0 || ack.AckHandle != 71 || len(ack.Payload) != 2+count*19 || int(binary.BigEndian.Uint16(ack.Payload[:2])) != count {
			t.Fatalf("wrong version-gated norma list: %+v", ack)
		}
	}
}

func TestDivaHistoricalNormaEligibilityAndClaim(t *testing.T) {
	for _, tt := range []struct {
		name   string
		gr     uint16
		points int64
		want   int
	}{
		{"zero", 1, 0, 0}, {"negative", 1, -1, 0},
		{"below milestone", 1, 19999, 0}, {"exact milestone", 1, 20000, 6},
		{"above milestone", 999, 20001, 6}, {"all historical milestones", 999, 999999, 11}, {"HR excluded", 0, 20000, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Add(-time.Hour).Unix())}
			s, repo := newDivaRewardHandlerTestSession(event)
			s.server.erupeConfig.DebugOptions.DivaOverride = 1
			repo.progress = DivaRewardProgress{GR: tt.gr, Points: tt.points}
			handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 72, Unk0: 1, RewardType: 1})
			ack := readAck(t, s)
			if ack.ErrorCode != 0 || len(ack.Payload) != 2+tt.want*9 || int(ack.Payload[1]) != tt.want || repo.offerKind != 1 {
				t.Fatalf("wrong norma eligibility: %+v", ack)
			}
			if repo.progressChar != s.charID || repo.progressEvent != event.ID {
				t.Fatal("norma query used another character/event")
			}
			if tt.want == 0 {
				return
			}
			ids := make([]uint32, len(repo.offers))
			for i, offer := range repo.offers {
				ids[i] = offer.ID
			}
			handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 73, RewardType: 1, ItemIDCount: uint8(len(ids)), RewardIDs: ids})
			if ack := readAck(t, s); ack.ErrorCode != 0 || repo.prepareKind != 1 || !s.hasPendingDivaRewardClaims() {
				t.Fatalf("norma receipts were not staged for atomic saves: %+v", ack)
			}
		})
	}
}

func TestDivaHistoricalNormaReceiptSaveIntegration(t *testing.T) {
	characters, db, charID, eventID := setupDivaSaveTest(t)
	repo := NewDivaRepository(db)
	rewards := eligibleDivaSongRewards(1, DivaRewardProgress{GR: 1, Points: 20000})
	offers, err := repo.OfferDivaRewards(charID, eventID, 1, rewards)
	if err != nil || len(offers) != 6 {
		t.Fatalf("norma offers: %+v %v", offers, err)
	}
	var itemIDs, gpIDs, ids []uint32
	for _, offer := range offers {
		ids = append(ids, offer.ID)
		if offer.ItemType == 26 {
			gpIDs = append(gpIDs, offer.ID)
		} else {
			itemIDs = append(itemIDs, offer.ID)
		}
	}
	if _, err := repo.PrepareDivaRewardClaims(charID, 2, ids); err == nil {
		t.Fatal("norma receipts accepted in the ranking category")
	}
	if pending, err := repo.PrepareDivaRewardClaims(charID, 1, ids); err != nil || len(pending) != 6 {
		t.Fatalf("norma receipt preparation: %+v %v", pending, err)
	}
	if err := characters.SaveCharacterDataAtomic(SaveAtomicParams{CharID: charID, Name: "RewardHunter", CompSave: []byte{1, 2}, HouseData: []byte{3}, DivaRewardIDs: itemIDs}); err != nil {
		t.Fatal(err)
	}
	if remaining, err := repo.OfferDivaRewards(charID, eventID, 1, rewards); err != nil || len(remaining) != 1 || remaining[0].ItemType != 26 {
		t.Fatalf("item save consumed GP or reoffered materials: %+v %v", remaining, err)
	}
	if err := characters.UpdateGCPAndPactWithDivaRewards(charID, 5100, 0, gpIDs); err != nil {
		t.Fatal(err)
	}
	if remaining, err := repo.OfferDivaRewards(charID, eventID, 1, rewards); err != nil || len(remaining) != 0 {
		t.Fatalf("claimed norma rewards reoffered: %+v %v", remaining, err)
	}
	if retry, err := repo.PrepareDivaRewardClaims(charID, 1, ids); err != nil || len(retry) != 0 {
		t.Fatalf("claimed receipt retry grants again: %+v %v", retry, err)
	}
}
