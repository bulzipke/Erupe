package channelserver

import (
	"sort"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
)

// Scenario represents scenario counter data.
type Scenario struct {
	MainID uint32
	// 0 = Basic
	// 1 = Veteran
	// 3 = Other
	// 6 = Pallone
	// 7 = Diva
	CategoryID uint8
}

func scenarioCounterDisplayOrder(scenario Scenario) uint32 {
	// Veteran retrospective: S82..S91 are years 1..10, while S81 is year 11.
	// Rotate that display range without changing IDs used by files/progress.
	if scenario.CategoryID == 1 {
		if scenario.MainID == 81 {
			return 91
		}
		if scenario.MainID >= 82 && scenario.MainID <= 91 {
			return scenario.MainID - 1
		}
	}
	return scenario.MainID
}

func handleMsgMhfInfoScenarioCounter(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfInfoScenarioCounter)
	scenarios, err := s.server.scenarioRepo.GetCounters()
	if err != nil {
		s.logger.Error("Failed to get scenario counter info from db", zap.Error(err))
		doAckBufSucceed(s, pkt.AckHandle, make([]byte, 1))
		return
	}

	// ZZ HD/non-HD parse an unsigned-byte count into 256 eight-byte slots.
	// Their menus use that count; 255 is the wire maximum, not 128. Keep the
	// legacy limit/order until the quest rollout is opted in, and for other
	// client versions that have not been audited.
	limit := 128
	if s.server.erupeConfig != nil && s.server.erupeConfig.QuestDelivery.Enabled && s.server.erupeConfig.RealClientMode == cfg.ZZ {
		limit = 255
		if len(scenarios) > limit {
			s.logger.Error("Scenario counter roster exceeds ZZ wire capacity", zap.Int("count", len(scenarios)))
			doAckBufFail(s, pkt.AckHandle, []byte{0})
			return
		}
		// Repositories may return a shared snapshot. Sort a detached copy so a
		// capped list is stable without modifying another session's source.
		scenarios = append([]Scenario(nil), scenarios...)
		sort.SliceStable(scenarios, func(i, j int) bool {
			if scenarios[i].CategoryID != scenarios[j].CategoryID {
				return scenarios[i].CategoryID < scenarios[j].CategoryID
			}
			return scenarioCounterDisplayOrder(scenarios[i]) < scenarioCounterDisplayOrder(scenarios[j])
		})
	}
	if len(scenarios) > limit {
		scenarios = scenarios[:limit]
	}

	bf := byteframe.NewByteFrame()
	bf.WriteUint8(uint8(len(scenarios)))
	for _, scenario := range scenarios {
		bf.WriteUint32(scenario.MainID)
		// If item exchange
		switch scenario.CategoryID {
		case 3, 6, 7:
			bf.WriteBool(true)
		default:
			bf.WriteBool(false)
		}
		bf.WriteUint8(scenario.CategoryID)
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}
