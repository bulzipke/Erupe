package channelserver

import "testing"

func TestTowerGemWireIDs(t *testing.T) {
	for index := 0; index < 30; index++ {
		id := towerGemID(index)
		got, valid := towerGemIndex(int32(id))
		if !valid || got != index {
			t.Fatalf("round trip %d -> %#x -> %d/%v", index, id, got, valid)
		}
		if index%5 == 4 && id&0xff != 8 {
			t.Fatalf("candlestick encoded as %#x", id)
		}
	}
	for _, id := range []int32{-1, 0, 5, 6, 7, 9, 0x105, 0x600, 0x601, 0x10001} {
		if _, valid := towerGemIndex(id); valid {
			t.Errorf("invalid gem %#x accepted", id)
		}
	}
}

func TestTowerGemDepositAlreadyFull(t *testing.T) {
	mock := &mockTowerRepo{gems: "10,0,0,0,0"}
	if err := newTestTowerService(mock).AddGem(1, 0, 2147483647); err != nil {
		t.Fatalf("full inventory must ack a valid deposit: %v", err)
	}
	if mock.updatedGems != "" {
		t.Fatalf("full inventory should not be written: %q", mock.updatedGems)
	}
}
