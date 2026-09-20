package channelserver

import (
	"fmt"
	"sort"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

type divaBonusTarget struct {
	Color, Kind uint8
	TargetID    uint32
	Start, End  uint32 // Native comparison includes both endpoints.
	Percent     uint16
}

// ZZ FUN_11534a40: count u16 + rows of color/kind/target/start/end/percent.
// The native response buffer is 6402 bytes; storage is 8*4*10 entries.
func divaBonusPayload(targets []divaBonusTarget) []byte {
	bf := byteframe.NewByteFrame()
	bf.WriteUint16(uint16(len(targets)))
	for _, target := range targets {
		bf.WriteUint8(target.Color)
		bf.WriteUint8(target.Kind)
		bf.WriteUint32(target.TargetID)
		bf.WriteUint32(target.Start)
		bf.WriteUint32(target.End)
		bf.WriteUint16(target.Percent)
	}
	return bf.Data()
}

func divaBonusTargets(event DivaEvent, rules []cfg.DivaBonusTarget) ([]divaBonusTarget, error) {
	if err := cfg.ValidateDivaBonusTargets(rules); err != nil {
		return nil, err
	}
	if event.ID == 0 || int64(event.StartTime)+divaPhaseDuration > 0xffffffff {
		return nil, fmt.Errorf("invalid Diva event or overflowing bonus timestamps")
	}
	firstDay := divaNoon(time.Unix(int64(event.StartTime), 0)).Unix()
	var buckets [8][4]int
	result := make([]divaBonusTarget, 0, len(rules))
	for i, rule := range rules {
		start := int64(event.StartTime) + rule.StartOffsetSeconds
		end := int64(event.StartTime) + rule.EndOffsetSeconds - 1
		day := -1
		for d := range buckets {
			lo := firstDay + int64(d)*86400
			// Match the parser's FIRST inclusive noon window exactly. A row
			// starting at noon can belong to the preceding storage bucket.
			if start >= lo && start <= lo+86400 {
				day = d
				break
			}
		}
		if day < 0 {
			return nil, fmt.Errorf("Diva bonus entry %d falls outside the native eight-day storage window", i)
		}
		buckets[day][rule.Color-1]++
		if buckets[day][rule.Color-1] > 10 {
			return nil, fmt.Errorf("Diva bonus entry %d exceeds ten entries in day %d color %d", i, day+1, rule.Color)
		}
		kind, ok := rule.NativeKind()
		if !ok {
			return nil, fmt.Errorf("unsupported Diva bonus type %q", rule.TargetType)
		}
		result = append(result, divaBonusTarget{uint8(rule.Color), kind,
			uint32(rule.TargetID), uint32(start), uint32(end), uint16(rule.MultiplierPercent)})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Start != result[j].Start {
			return result[i].Start < result[j].Start
		}
		return result[i].Color < result[j].Color
	})
	return result, nil
}

func handleMsgMhfGetUdBonusQuestInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdBonusQuestInfo)
	options := s.server.erupeConfig
	rules := options.GameplayOptions.DivaBonusTargets
	random := options.GameplayOptions.DivaBonusRandom
	phase := options.DebugOptions.DivaOverride
	if (!random && len(rules) == 0) || phase == 0 || phase == 2 || phase == 3 {
		doAckBufSucceed(s, pkt.AckHandle, divaBonusPayload(nil))
		return
	}
	if random && len(rules) > 0 {
		s.logger.Warn("DivaBonusRandom and DivaBonusTargets cannot be enabled together")
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	if options.RealClientMode != cfg.ZZ || options.DebugOptions.InGameTimeOverrideHour != nil || s.server.divaRepo == nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	beads, err := s.server.divaRepo.GetBeads()
	if err != nil {
		s.logger.Warn("Failed to read Diva bonus bead colors", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	if len(beads) == 0 {
		beads = defaultBeadTypes
	}
	if random && len(beads) < 4 {
		s.logger.Warn("DivaBonusRandom requires four bead colors", zap.Int("colors", len(beads)))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	for _, rule := range rules {
		if rule.Color < 1 || rule.Color > min(len(beads), 4) {
			// FUN_1039dc70 maps an unknown color to index zero. Never let an
			// absent color accidentally grant a bonus to the first bead.
			s.logger.Warn("Diva bonus targets an unavailable bead color", zap.Int("color", rule.Color))
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
	}
	event, err := s.divaEvent()
	if err != nil {
		s.logger.Warn("Failed to read Diva bonus event", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	now := TimeAdjusted().Unix()
	if event.ID == 0 || now < int64(event.StartTime) || now >= int64(event.StartTime)+divaPhaseDuration {
		doAckBufSucceed(s, pkt.AckHandle, divaBonusPayload(nil))
		return
	}
	if random {
		rules, err = divaRandomBonusRules(event)
		if err != nil {
			s.logger.Warn("Failed to generate random Diva bonuses", zap.Error(err))
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
	}
	targets, err := divaBonusTargets(event, rules)
	if err != nil {
		s.logger.Warn("Rejected Diva bonus schedule", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	if err := validateDivaBonusData(options.BinPath, rules); err != nil {
		s.logger.Warn("Rejected unavailable Diva bonus target", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	// Send every color and the complete event schedule. The client checks the
	// selected color, hunt target and time, and includes the bonus in QuestPoints.
	s.logger.Debug("Diva bonus schedule sent", zap.Uint32("eventID", event.ID),
		zap.Uint32("startTime", event.StartTime), zap.Int("targets", len(targets)))
	doAckBufSucceed(s, pkt.AckHandle, divaBonusPayload(targets))
}
