package channelserver

import "erupe-ce/common/byteframe"

// udMilestones grant song activations (native target type 0). Keep their
// indices: the client displays each song's number as its wire slot + 1.
var udMilestones = []uint64{
	500000, 1000000, 2000000, 3000000, 5000000, 7000000, 10000000,
	15000000, 20000000, 30000000, 50000000, 70000000, 100000000,
}

// Server-wide prayer totals for shop unlock levels 1, 2 and 3. This is the
// operator's 2026-11-03 policy, separate from song activation milestones.
var udShopUnlockMilestones = [...]uint64{100000, 200000, 300000}

func divaTotalPointPayload(total uint64) []byte {
	var targets [64]uint64
	var types [64]uint8
	copy(targets[:], udMilestones)
	for i, threshold := range udShopUnlockMilestones {
		slot := len(udMilestones) + i
		targets[slot] = threshold
		types[slot] = uint8(i + 1)
	}

	// ZZ: u8 status, 64 x u64 target, 64 x u8 type, u64 total.
	// Types 1..3 grant the corresponding Koko bead/shop level, not a song
	// activation. The native UI sorts by target while retaining wire indices.
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0)
	for _, threshold := range targets {
		bf.WriteUint64(threshold)
	}
	bf.WriteBytes(types[:])
	bf.WriteUint64(total)
	return bf.Data()
}
