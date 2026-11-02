package channelserver

import cfg "erupe-ce/config"

// The original DAT key records carry completion-bit indices at +2:
// 22037 -> 12, 22040 -> 11. Preserve the pre-save value, including an already
// completed key, rather than clearing it or restoring the whole progress map.
func intrusionKeyCompletionBit(parent, child uint16) (uint, bool) {
	switch {
	case parent == 22037 && child == 26611:
		return 12, true
	case parent == 22040 && child == 26623:
		return 11, true
	default:
		return 0, false
	}
}

// Called for both full and delta saves under the existing character-save lock.
// A pinned intrusion receipt survives returning to town, so abandoning or
// clearing the intrusion cannot turn its parent into a completed key quest.
func (s *Session) preserveIntrusionKeyProgress(save *CharacterSaveData, previous []byte) {
	if s == nil || s.server == nil || s.server.erupeConfig == nil ||
		s.server.erupeConfig.RealClientMode != cfg.ZZ || save == nil || save.CharID != s.charID {
		return
	}
	receipt := s.questDeliveryReceipt.Load()
	if receipt == nil {
		return
	}
	bit, ok := intrusionKeyCompletionBit(receipt.questID, receipt.nativeIntrusionResultID)
	if !ok {
		return
	}
	index, mask := int(bit/8), byte(1<<uint(bit%8))
	if index >= len(previous) || index >= len(save.KQF) {
		return
	}
	save.KQF[index] = save.KQF[index]&^mask | previous[index]&mask
}
