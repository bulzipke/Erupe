package channelserver

import cfg "erupe-ce/config"

// ZZ native event flag 0x271 shares the existing custom event word with the
// Tower introduction's 0x270. Native save+0xb2 is blob+0x102 (0x50-byte header).
// A zero bit means not yet encountered; no character migration is needed.
const (
	firstIntrusionSeenBlobOffset = 0x102
	firstIntrusionSeenMask       = byte(2)
)

func (save *CharacterSaveData) firstIntrusionSeen() bool {
	return save != nil && save.Mode == cfg.ZZ && len(save.decompSave) > firstIntrusionSeenBlobOffset &&
		save.decompSave[firstIntrusionSeenBlobOffset]&firstIntrusionSeenMask != 0
}

// Retain the previously saved encounter bit on both full and delta saves.
// Neighboring event flags, including the Tower introduction, remain exactly
// as supplied by the native client. This runs under the character-save lock.
func (save *CharacterSaveData) preserveFirstIntrusionSeen(previouslySeen bool) {
	if previouslySeen && save != nil && save.Mode == cfg.ZZ && len(save.decompSave) > firstIntrusionSeenBlobOffset {
		save.decompSave[firstIntrusionSeenBlobOffset] |= firstIntrusionSeenMask
	}
}
