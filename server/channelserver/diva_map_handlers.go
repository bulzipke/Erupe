package channelserver

import (
	"fmt"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

// FUN_103a8a80 treats 0xff as "refresh event info and retry", not failure.
// 0x01 exits to the native error dialog; the ACK itself must remain successful
// and buffered. A failed GET ACK instead starts a GENERATE request.
const divaMapNativeTerminalError byte = 0x01

func (s *Session) divaMapEvent() (DivaEvent, error) {
	if _, err := s.divaEvent(); err != nil {
		return DivaEvent{}, err
	}
	// Map-only activation preserves the personal-reward legacy cutover. Resolve
	// its window first; older in-memory repositories retain the existing path.
	if repo, ok := s.server.divaRepo.(DivaMapRewardWindowRepository); ok {
		return repo.GetDivaMapRewardEvent(TimeAdjusted())
	}
	repo, ok := s.server.divaRepo.(DivaInterceptionRewardWindowRepository)
	if !ok {
		return DivaEvent{}, fmt.Errorf("Diva interception window repository unavailable")
	}
	return repo.GetDivaInterceptionRewardEvent(TimeAdjusted())
}

func (s *Session) divaMapView(requestedGuildID uint32) (DivaMapView, error) {
	options := s.server.erupeConfig
	if options.RealClientMode != cfg.ZZ || options.DebugOptions.DivaOverride == 0 ||
		options.DebugOptions.InGameTimeOverrideHour != nil || s.charID == 0 || s.server.guildRepo == nil {
		return DivaMapView{}, nil
	}
	repo, ok := s.server.divaRepo.(DivaMapRepository)
	if !ok {
		return DivaMapView{}, nil
	}
	guildID, reason, err := resolveGuildMemberAccess(s, requestedGuildID)
	if err != nil {
		return DivaMapView{}, err
	}
	if reason != "" || guildID == 0 {
		return DivaMapView{}, nil
	}
	event, err := s.divaMapEvent()
	if err != nil || event.ID == 0 {
		return DivaMapView{}, err
	}
	return repo.GetDivaMap(s.charID, guildID, event.ID, TimeAdjusted())
}

func handleDivaMapQuery(s *Session, ackHandle uint32, generate bool) {
	view, err := s.divaMapView(0)
	if err != nil {
		s.logger.Warn("Failed to read Diva map", zap.Error(err))
	}
	// An unavailable/invalid map must stop the UI request cycle. Never substitute
	// a transport failure, 0xff refresh, 0xf7 generation, or empty map success.
	if err != nil || !view.Enabled {
		doAckBufSucceed(s, ackHandle, []byte{divaMapNativeTerminalError})
		return
	}
	if err := validateDivaInterceptionMap(view.Map); err != nil {
		s.logger.Error("Rejected unsafe Diva map", zap.Error(err))
		doAckBufSucceed(s, ackHandle, []byte{divaMapNativeTerminalError})
		return
	}
	if generate {
		current := view.Map.States[0]
		for _, definition := range view.Map.Definitions {
			if definition.ID != current.TemplateID {
				continue
			}
			index := divaInterceptionMapGoalIndex(current, definition)
			if index < 0 || current.Nodes[index].EarnedPoints == current.Nodes[index].RequiredPoints {
				// The event can end on a completed map. It must not enter the
				// native generate/query loop with a fake unchanged success.
				doAckBufSucceed(s, ackHandle, []byte{divaMapNativeTerminalError})
				return
			}
		}
		doAckBufSucceed(s, ackHandle, []byte{0})
		return
	}
	data, err := divaInterceptionMapPayload(view.Map)
	if err != nil {
		s.logger.Error("Rejected unsafe Diva map payload", zap.Error(err))
		doAckBufSucceed(s, ackHandle, []byte{divaMapNativeTerminalError})
		return
	}
	doAckBufSucceed(s, ackHandle, data)
}

func handleDivaAreaRanking(s *Session, pkt *mhfpacket.MsgMhfGetUdTacticsRanking) {
	empty := func() { doAckBufSucceed(s, pkt.AckHandle, divaEmptyTacticsRankingPayload()) }
	options := s.server.erupeConfig
	if options.RealClientMode != cfg.ZZ || options.DebugOptions.DivaOverride == 0 ||
		options.DebugOptions.InGameTimeOverrideHour != nil || s.charID == 0 {
		empty()
		return
	}
	repo, ok := s.server.divaRepo.(DivaMapRepository)
	if !ok {
		empty()
		return
	}
	if pkt.GuildID != 0 {
		if s.server.guildRepo == nil {
			empty()
			return
		}
		_, reason, err := resolveGuildMemberAccess(s, pkt.GuildID)
		if err != nil || reason != "" {
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
	}
	event, err := s.divaMapEvent()
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	if event.ID == 0 {
		empty()
		return
	}
	ranking, err := repo.GetDivaMapRanking(s.charID, event.ID, TimeAdjusted())
	if err != nil {
		s.logger.Warn("Failed to read Diva area ranks", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	if !ranking.Enabled {
		empty()
		return
	}
	data, err := divaAreaRankingPayload(ranking.Rows, ranking.Own)
	if err != nil {
		s.logger.Warn("Rejected unsafe Diva area ranks", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	doAckBufSucceed(s, pkt.AckHandle, data)
}

func handleDivaRemainingAreas(s *Session, pkt *mhfpacket.MsgMhfGetUdTacticsRemainingPoint) {
	view, err := s.divaMapView(pkt.Unk0)
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	remaining := uint32(1) // Round-40 hall eligibility needs one actual area.
	if view.Enabled && view.Map.AcquiredAreas > 0 {
		remaining = 0
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint32(remaining)
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

type divaMapSettler interface{ SettleDivaMaps(time.Time, int) error }

// Each channel may wake this idempotent worker. Event-scoped DB locks serialize
// all channels/processes. Shutdown ends the ticker; reads also catch up missed
// boundaries, so downtime does not require fabricating missed quest results.
func (s *Server) settleDivaMaps() {
	repo, ok := s.divaRepo.(divaMapSettler)
	if !ok || s.erupeConfig.RealClientMode != cfg.ZZ || s.erupeConfig.DebugOptions.DivaOverride == 0 ||
		s.erupeConfig.DebugOptions.InGameTimeOverrideHour != nil {
		return
	}
	run := func() {
		if err := repo.SettleDivaMaps(TimeAdjusted(), s.erupeConfig.DebugOptions.DivaOverride); err != nil {
			s.logger.Warn("Failed to settle Diva maps", zap.Error(err))
		}
	}
	run()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			run()
		}
	}
}
