package channelserver

import "erupe-ce/common/byteframe"

// Original emulator targets used by historical and forced rounds.
var udMilestones = []int64{
	500000, 1000000, 2000000, 3000000, 5000000, 7000000, 10000000,
	15000000, 20000000, 30000000, 50000000, 70000000, 100000000,
}

// Older 13-target rounds use the original fixed GP shop unlock levels.
var legacyDivaShopUnlockMilestones = [...]uint64{100000, 200000, 300000}

func divaTotalPointPayload(total uint64, songTargets []int64) []byte {
	var targets [64]uint64
	var types [64]uint8
	// The client displays each song's number as its wire slot + 1.
	for i, threshold := range songTargets {
		targets[i] = uint64(threshold)
	}
	shopTargets := legacyDivaShopUnlockMilestones
	if len(songTargets) == divaFinalSongLimit {
		// The native gauge uses the largest target as its endpoint. Put each
		// shop level halfway between the same song marks as in the retail UI.
		for i := range shopTargets {
			lower := songTargets[5*(i+1)-1]
			upper := songTargets[5*(i+2)-1]
			shopTargets[i] = uint64(lower + (upper-lower)/2)
		}
	}
	for i, threshold := range shopTargets {
		slot := len(songTargets) + i
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
