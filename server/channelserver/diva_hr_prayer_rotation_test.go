package channelserver

import (
	"bytes"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"erupe-ce/network/mhfpacket"
)

func TestDivaHRPrayerRotationPolicyAndBoundaries(t *testing.T) {
	for _, hr := range []uint16{2, 99, 100, 999} {
		t.Run(fmt.Sprint(hr), func(t *testing.T) {
			rotation := divaPrayerRotationForRank(hr, 0)
			if rotation.Key != "hr-prayer-v1" || rotation.track() != "hr" || rotation.Start != 21000 || rotation.Interval != 1000 {
				t.Fatalf("wrong HR continuation: %+v", rotation)
			}
			for _, tc := range []struct {
				points int64
				want   uint32
			}{
				{math.MinInt64, 0}, {-1, 0}, {0, 0}, {20000, 0}, {20999, 0},
				{21000, 1}, {21999, 1}, {22000, 2}, {22999, 2}, {23000, 3},
			} {
				if got := rotation.dueCount(tc.points); got != tc.want {
					t.Fatalf("%d points: %d due, want %d", tc.points, got, tc.want)
				}
			}
			multiplier, lower, upper := uint16(1), uint16(2), uint16(99)
			if hr >= 100 {
				multiplier, lower, upper = 2, 100, 999
			}
			for index := uint32(0); index < 12; index++ {
				row := rotation.reward(index)
				itemType, itemID, quantity := uint8(7), uint16(0x3694), multiplier
				if index%2 != 0 {
					itemType, itemID, quantity = 26, 0, 100*multiplier
				}
				if row.Key != fmt.Sprintf("rotation-hr-prayer-v1-%d", index) || row.Threshold != 21000+1000*index ||
					row.ItemType != itemType || row.ItemID != itemID || row.Quantity != quantity || row.GR || row.NormaRepeat ||
					row.MinHR != lower || row.MaxHR != upper || !strings.HasPrefix(row.Basis, "custom-inferred-") {
					t.Fatalf("wrong inferred reward: %+v", row)
				}
				if err := validateDivaRewardCatalog(1, []DivaRewardCatalogEntry{row}); err != nil {
					t.Fatal(err)
				}
			}
			maxCount := uint32((uint64(math.MaxUint32)-uint64(rotation.Start))/uint64(rotation.Interval) + 1)
			if rotation.dueCount(math.MaxInt64) != maxCount || rotation.dueCount(math.MaxUint32) != maxCount ||
				rotation.reward(maxCount) != (DivaRewardCatalogEntry{}) || rotation.reward(math.MaxUint32) != (DivaRewardCatalogEntry{}) {
				t.Fatal("overflow changed the HR sequence")
			}
			last := rotation.reward(maxCount - 1)
			if last.Key == "" || uint64(last.Threshold)+1000 <= math.MaxUint32 {
				t.Fatal("wrong final representable reward")
			}
		})
	}
	for _, hr := range []uint16{0, 1, 1000, math.MaxUint16} {
		rotation := divaPrayerRotationForRank(hr, 0)
		if rotation.track() != "" || rotation.dueCount(math.MaxInt64) != 0 || rotation.reward(0) != (DivaRewardCatalogEntry{}) {
			t.Fatalf("invalid HR %d received a rotation", hr)
		}
		if !reflect.DeepEqual(divaPrayerRotationForRank(hr, 1), divaPrayerRotation()) {
			t.Fatal("GR policy changed")
		}
	}
}

func TestDivaHRPrayerRotationPreservesFiniteRewards(t *testing.T) {
	for _, hr := range []uint16{2, 100} {
		rotation := divaHRPrayerRotation(hr)
		fixed := eligibleDivaSongRewards(1, DivaRewardProgress{HR: hr, Points: math.MaxInt64})
		if len(fixed) != 6 {
			t.Fatalf("fixed HR table changed: %+v", fixed)
		}
		for _, row := range fixed {
			if row.Threshold >= rotation.Start || row.NormaRepeat || strings.HasPrefix(row.Key, "rotation-") {
				t.Fatal("fixed and repeated rewards overlap")
			}
		}
		rotation.Items[0].Quantity = 999
		if divaHRPrayerRotation(hr).Items[0].Quantity == 999 {
			t.Fatal("caller mutated canonical HR policy")
		}
	}
	// Promotion changes only the snapshot for not-yet-issued indexes, not their
	// identity. The repository's shared HR cursor enforces the single issuance.
	for index := uint32(0); index < 10; index++ {
		low, high := divaHRPrayerRotation(2).reward(index), divaHRPrayerRotation(100).reward(index)
		if low.Key != high.Key || low.Threshold != high.Threshold || high.Quantity != 2*low.Quantity {
			t.Fatal("HR promotion changed sequence identity")
		}
	}
}

func TestDivaHRPrayerRotationHandlerMaterialAndGP(t *testing.T) {
	for _, hr := range []uint16{2, 100} {
		t.Run(fmt.Sprint(hr), func(t *testing.T) {
			event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Add(-time.Hour).Unix())}
			s, repo := newDivaRewardHandlerTestSession(event)
			repo.progress = DivaRewardProgress{HR: hr, Points: 22000}
			for index := uint32(0); index < 2; index++ {
				row := divaHRPrayerRotation(hr).reward(index)
				repo.offerOverride = append(repo.offerOverride, DivaRewardOffer{ID: 700 + index, EventID: event.ID, RewardType: 1,
					CatalogKey: row.Key, ItemType: row.ItemType, ItemID: row.ItemID, Quantity: row.Quantity})
			}
			handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 90, Unk0: 1, RewardType: 1})
			ack := readAck(t, s)
			if ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, divaAvailableRewardPayload(repo.offerOverride)) ||
				repo.rotationCalls != 1 || repo.rotationProgress != repo.progress || len(repo.candidates) != 6 || s.hasPendingDivaRewardClaims() {
				t.Fatalf("HR query failed: %+v", ack)
			}
			handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 91, RewardType: 1, ItemIDCount: 2, RewardIDs: []uint32{700, 701}})
			if ack := readAck(t, s); ack.ErrorCode != 0 || !s.hasPendingDivaRewardClaims() || repo.prepareCalls != 1 {
				t.Fatalf("HR claim failed: %+v", ack)
			}
		})
	}
}
