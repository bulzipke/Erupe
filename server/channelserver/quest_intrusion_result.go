package channelserver

import cfg "erupe-ce/config"

// This closed compatibility roster is derived from both original ZZ selectors
// and producers for all 207 current Ferias HR Hunters IDs. The 15 intrusion-only
// IDs, retired-only IDs and event controls grant no parent authority. It does
// not enable encounters: admission must already contain native flag 0x20.
func nativeIntrusionResultForParent(questID uint16) uint16 {
	switch questID {
	case 21132:
		return 26611
	case 26748:
		return 26612
	case 21475:
		return 26613
	case 21093, 21095, 21096, 21097, 21099, 21780, 21782, 21783, 21793, 21797:
		return 26614
	case 21805:
		return 26615
	case 21474, 21479, 26303, 26306, 26323, 26347, 26352:
		return 26616
	case 21079, 21082, 21083, 21759, 21767, 21771:
		return 26617
	case 21472, 21480, 26256, 26262, 26299, 26338, 26588:
		return 26619
	case 26168, 26224, 26229, 26230, 26253:
		return 26620
	case 21476, 22089, 22090, 26268, 26276:
		return 26622
	case 21490, 26402, 26403, 26404:
		return 26624
	}
	return 0
}

// Called only when the first validated setup is pinned. No file/repository
// reads or mutable shared maps are used under the stage/lifecycle locks.
func (s *Session) nativeIntrusionResultFromSetup(stageID string, setup []byte) uint16 {
	if s == nil || s.server == nil || s.server.erupeConfig == nil || s.server.erupeConfig.RealClientMode != cfg.ZZ {
		return 0
	}
	questID, mode, _, valid := decodeQuestRunSetupFromStageBinary(stageID, 1, 3, setup)
	if !valid || mode != questRunModeNormal || setup[questRunStageHardcoreFlagsOffset]&0x20 == 0 {
		return 0
	}
	return nativeIntrusionResultForParent(questID)
}
