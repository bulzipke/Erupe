package channelserver

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestDivaPrayerRotationDueCount(t *testing.T) {
	for _, test := range []struct {
		points int64
		gr     uint16
		want   uint32
	}{
		{math.MinInt64, 1, 0}, {-1, 1, 0}, {0, 1, 0},
		{102000, 1, 0}, {102999, 1, 0},
		{103000, 1, 1}, {103999, 1, 1},
		{104000, 1, 2}, {104999, 1, 2}, {105000, 1, 3},
		{103000, 0, 0}, {math.MaxInt64, 0, 0},
		{105000, math.MaxUint16, 3},
	} {
		t.Run(fmt.Sprintf("%d-gr%d", test.points, test.gr), func(t *testing.T) {
			if got := divaPrayerRotationDueCount(test.points, test.gr); got != test.want {
				t.Fatalf("due count = %d, want %d", got, test.want)
			}
		})
	}
	rotation := divaPrayerRotation()
	want := uint32((uint64(math.MaxUint32)-uint64(rotation.Start))/uint64(rotation.Interval) + 1)
	for _, points := range []int64{math.MaxUint32, math.MaxUint32 + 1, math.MaxInt64} {
		if got := divaPrayerRotationDueCount(points, 1); got != want {
			t.Fatalf("score %d: count = %d, want %d", points, got, want)
		}
	}
}

func TestDivaPrayerRotationStableRewards(t *testing.T) {
	rotation := divaPrayerRotation()
	for index := uint32(0); index < 70; index++ {
		reward := divaPrayerRotationReward(index)
		wantItem := uint16(0x2338 + index%2)
		if reward.Key != fmt.Sprintf("rotation-%s-%d", rotation.Key, index) ||
			reward.ItemID != wantItem || reward.Quantity != 5 || reward.ItemType != 7 ||
			reward.RewardType != 1 || !reward.GR || reward.NormaRepeat ||
			reward.Threshold != 103000+1000*index || !strings.HasPrefix(reward.Basis, "custom-") {
			t.Fatalf("invalid rotation reward %d: %+v", index, reward)
		}
	}
	// Invalid indexes must not wrap into small thresholds or become payable.
	count := divaPrayerRotationDueCount(math.MaxInt64, 1)
	last := divaPrayerRotationReward(count - 1)
	if last.Key == "" || uint64(last.Threshold)+uint64(rotation.Interval) <= math.MaxUint32 {
		t.Fatalf("incorrect final representable reward: %+v", last)
	}
	for _, index := range []uint32{count, math.MaxUint32} {
		if reward := divaPrayerRotationReward(index); reward != (DivaRewardCatalogEntry{}) {
			t.Fatalf("unrepresentable index %d produced %+v", index, reward)
		}
	}
}

func TestDivaPrayerRotationConfigCopies(t *testing.T) {
	rotation := divaPrayerRotation()
	rotation.Items[0].ItemID = 0
	rotation.Items[1].Quantity = 0
	if divaPrayerRotationReward(0).ItemID != 0x2338 || divaPrayerRotationReward(1).Quantity != 5 {
		t.Fatal("caller changed the canonical rotation")
	}
}

func TestDivaPrayerRotationDisplayPreservesMilestones(t *testing.T) {
	catalog := divaPrayerRewardCatalog()
	before := append([]DivaRewardCatalogEntry(nil), catalog...)
	finite := divaNormaRewardPayload(catalog)
	wire := divaPrayerNormaRewardPayload()
	if len(catalog) != 23 || len(wire) != 2+29*19 || binary.BigEndian.Uint16(wire) != 29 {
		t.Fatalf("unexpected mixed HR/GR catalog shape: fixed=%d wire=%d", len(catalog), len(wire))
	}
	if !reflect.DeepEqual(catalog, before) || !reflect.DeepEqual(divaPrayerRewardCatalog(), before) {
		t.Fatal("display mutated the finite payout catalog")
	}
	counts := map[uint32]int{}
	var fixedRows []byte
	tailCounts := map[uint32]int{}
	var previous uint32
	for at := 2; at < len(wire); at += 19 {
		row := wire[at : at+19]
		points := binary.BigEndian.Uint32(row[14:18])
		if points < previous {
			t.Fatal("wire sorting can separate native reward and repeat-flag arrays")
		}
		previous = points
		hr, gr := uint16(binary.BigEndian.Uint32(row[10:14])), uint16(row[5])
		key := uint32(hr)
		if gr > 0 {
			key = 0
		}
		counts[key]++
		rotation := divaPrayerRotationForRank(hr, gr)
		if points < rotation.Start {
			if row[18] != 0 {
				t.Fatal("fixed rewards gained a repeat marker")
			}
			fixedRows = append(fixedRows, row...)
			continue
		}
		tailCounts[key]++
		index := (points - rotation.Start) / rotation.Interval
		reward := rotation.reward(index)
		wantFlag := byte(1)
		if gr == 0 && hr == 2 && index == 0 {
			wantFlag = 0
		}
		lo, hi := uint32(reward.MinHR), uint32(reward.MaxHR)
		if reward.GR {
			lo, hi = 1, 999
		}
		if row[0] != reward.ItemType || binary.BigEndian.Uint16(row[1:3]) != reward.ItemID ||
			binary.BigEndian.Uint16(row[3:5]) != reward.Quantity ||
			binary.BigEndian.Uint32(row[6:10]) != hi || binary.BigEndian.Uint32(row[10:14]) != lo ||
			points != reward.Threshold || row[18] != wantFlag {
			t.Fatalf("repeat display row differs from actual entitlement %d: %x", index, row)
		}
	}
	if !bytes.Equal(finite[2:], fixedRows) {
		t.Fatal("display changed existing HR/GR item quantities, points or bounds")
	}
	if !reflect.DeepEqual(counts, map[uint32]int{0: 13, 2: 8, 100: 8}) ||
		!reflect.DeepEqual(tailCounts, map[uint32]int{0: 2, 2: 2, 100: 2}) {
		t.Fatalf("rank tabs changed: %v", counts)
	}
	for _, rank := range []DivaRewardProgress{{HR: 2}, {HR: 100}, {GR: 1}} {
		rank.Points = math.MaxUint32
		for _, reward := range eligibleDivaSongRewards(1, rank) {
			if reward.NormaRepeat || strings.HasPrefix(reward.Key, "rotation-") {
				t.Fatal("display-only tail leaked into finite reward eligibility")
			}
		}
	}
	// The native isolated-function probe can consume this verbose-test output
	// to verify the actual wire, including mixed GP/material reward columns.
	t.Logf("native norma wire: %x", wire)
}

func TestDivaPrayerRotationHRTailBoundaryOrder(t *testing.T) {
	wire := divaPrayerNormaRewardPayload()
	type tailRow struct {
		points   uint32
		low, hi  uint32
		item     uint16
		quantity uint16
		kind     byte
		flag     byte
	}
	var actual []tailRow
	for at := 2; at < len(wire); at += 19 {
		row := wire[at : at+19]
		points := binary.BigEndian.Uint32(row[14:18])
		if row[5] != 0 || points < 21000 {
			continue
		}
		actual = append(actual, tailRow{points, binary.BigEndian.Uint32(row[10:14]), binary.BigEndian.Uint32(row[6:10]),
			binary.BigEndian.Uint16(row[1:3]), binary.BigEndian.Uint16(row[3:5]), row[0], row[18]})
	}
	want := []tailRow{
		{21000, 2, 99, 0x3694, 1, 7, 0},
		{21000, 100, 999, 0x3694, 2, 7, 1},
		{22000, 2, 99, 0, 100, 26, 1},
		{22000, 100, 999, 0, 200, 26, 1},
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("native HR boundary order = %+v, want %+v", actual, want)
	}
}
