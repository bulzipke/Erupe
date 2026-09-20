package channelserver

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

func divaTacticsQuestIDs(points map[uint16]int64) []uint16 {
	ids := make([]uint16, 0, len(points))
	for id, n := range points {
		if n > 0 && id >= udTacticsQuestMin && id <= udTacticsQuestMax {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func currentDivaTacticsPoints(s *Session) (map[uint16]int64, error) {
	if repo, ok := s.server.divaRepo.(DivaInterceptionRepository); ok {
		event, err := s.divaEvent()
		if err != nil {
			return nil, err
		}
		if windows, ok := s.server.divaRepo.(DivaInterceptionRewardWindowRepository); ok {
			event, err = windows.GetDivaInterceptionRewardEvent(TimeAdjusted())
			if err != nil {
				return nil, err
			}
		}
		if event.ID != 0 {
			progress, err := repo.GetDivaInterceptionProgress(s.charID, event.ID)
			if err != nil {
				return nil, err
			}
			if progress.Enabled {
				return progress.QuestPoints, nil
			}
		}
	}
	// Historical totals remain readable, never copied into a new round.
	legacy, err := s.server.divaRepo.GetCharacterInterceptionPoints(s.charID)
	if err != nil {
		return nil, err
	}
	points := make(map[uint16]int64)
	for key, value := range legacy {
		id, err := strconv.ParseUint(key, 10, 16)
		if err == nil && id >= udTacticsQuestMin && id <= udTacticsQuestMax && value > 0 {
			points[uint16(id)] = int64(value)
		}
	}
	return points, nil
}

func handleDivaTacticsPoint(s *Session, pkt *mhfpacket.MsgMhfGetUdTacticsPoint) {
	points, err := currentDivaTacticsPoints(s)
	if err != nil {
		s.logger.Warn("Failed to read round-scoped Diva interception points", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	var total int64
	for _, pts := range points {
		// The native display is signed; saturate instead of wrapping a total.
		total += min(max(pts, 0), int64(0x7fffffff)-total)
	}
	doAckBufSucceed(s, pkt.AckHandle, divaTacticsPointPayload(uint32(total), divaTacticsQuestIDs(points)))
}

func handleDivaTacticsAdd(s *Session, pkt *mhfpacket.MsgMhfAddUdTacticsPoint) {
	repo, ok := s.server.divaRepo.(DivaInterceptionRepository)
	run := s.divaInterceptionRunSnapshot()
	if !ok || s.server.erupeConfig.RealClientMode != cfg.ZZ || pkt.TacticsPoints == 0 ||
		run.Key == "" || run.QuestID != pkt.QuestID {
		// Before the cutover, preserve the existing accumulation without making
		// it reward-eligible. New rounds require a bound real departure.
		if ok && pkt.TacticsPoints > 0 && pkt.QuestID >= udTacticsQuestMin && pkt.QuestID <= udTacticsQuestMax {
			event, err := s.divaEvent()
			if err != nil {
				doAckBufFail(s, pkt.AckHandle, nil)
				return
			}
			if event.ID != 0 {
				progress, err := repo.GetDivaInterceptionProgress(s.charID, event.ID)
				if err != nil {
					doAckBufFail(s, pkt.AckHandle, nil)
					return
				}
				start, end := divaInterceptionWindow(event)
				now := TimeAdjusted()
				phase := s.server.erupeConfig.DebugOptions.DivaOverride
				if !progress.Enabled && phase != 0 && phase != 1 && phase != 3 && !now.Before(start) && now.Before(end) {
					if err := s.server.divaRepo.AddInterceptionPoints(s.charID, int(pkt.QuestID), int(pkt.TacticsPoints)); err != nil {
						doAckBufFail(s, pkt.AckHandle, nil)
						return
					}
				}
			}
		}
		points, err := currentDivaTacticsPoints(s)
		if err != nil {
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		doAckBufSucceed(s, pkt.AckHandle, divaTacticsAddPayload(divaTacticsQuestIDs(points)))
		return
	}
	err := repo.AddDivaInterceptionPoints(s.charID, run.EventID, run.QuestID, pkt.TacticsPoints, run.GuildID, run.Key, run.StartedAt, TimeAdjusted())
	if err != nil {
		s.logger.Warn("Failed to record Diva interception run", zap.Uint32("eventID", run.EventID), zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	// A retry of an already committed old-round report may arrive after rollover.
	// Keep its old ledger attribution, but replace the UI with CURRENT-round IDs.
	points, err := currentDivaTacticsPoints(s)
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	s.logger.Info("Diva interception points saved", zap.Uint32("eventID", run.EventID), zap.Uint32("charID", s.charID), zap.Uint32("points", pkt.TacticsPoints))
	doAckBufSucceed(s, pkt.AckHandle, divaTacticsAddPayload(divaTacticsQuestIDs(points)))
}

func eligibleDivaInterceptionRewards(prizes []DivaPrize, progress DivaInterceptionProgress) ([]DivaRewardCatalogEntry, error) {
	if !progress.Enabled || progress.Points <= 0 {
		return nil, nil
	}
	var rows []DivaRewardCatalogEntry
	for _, prize := range prizes {
		if prize.Type != "personal" || prize.Repeatable || prize.PointsReq <= 0 || int64(prize.PointsReq) > progress.Points || (prize.GR && progress.GR == 0) {
			continue
		}
		if prize.ID <= 0 || prize.ItemType < 0 || prize.ItemType > 255 || prize.ItemID < 0 || prize.ItemID > 65535 || prize.Quantity <= 0 || prize.Quantity > 65535 {
			return nil, fmt.Errorf("invalid Diva personal prize %d", prize.ID)
		}
		if err := validateDivaRewardItem(uint8(prize.ItemType), uint16(prize.ItemID), uint16(prize.Quantity)); err != nil {
			return nil, err
		}
		rows = append(rows, DivaRewardCatalogEntry{Key: fmt.Sprintf("tactics-personal-%d", prize.ID), RewardType: 6, Threshold: uint32(prize.PointsReq), GR: prize.GR, ItemType: uint8(prize.ItemType), ItemID: uint16(prize.ItemID), Quantity: uint16(prize.Quantity)})
	}
	for _, r := range divaHRMilestoneRewards {
		if r.RewardType == 6 && int64(r.Threshold) <= progress.Points && divaRewardRankMatches(r, progress.HR, progress.GR) {
			rows = append(rows, r)
		}
	}
	return rows, nil
}

func handleDivaInterceptionRewardQuery(s *Session, pkt *mhfpacket.MsgMhfAcquireUdItem, repo DivaRewardRepository, now time.Time) {
	windows, ok := s.server.divaRepo.(DivaInterceptionRewardWindowRepository)
	if !ok {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	event, err := windows.GetDivaInterceptionRewardEvent(now)
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	if event.ID == 0 {
		doAckBufSucceed(s, pkt.AckHandle, divaAvailableRewardPayload(nil))
		return
	}
	interception, ok := s.server.divaRepo.(DivaInterceptionRepository)
	if !ok {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	progress, err := interception.GetDivaInterceptionProgress(s.charID, event.ID)
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	prizes, err := s.server.divaRepo.GetPersonalPrizes()
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	eligible, err := eligibleDivaInterceptionRewards(prizes, progress)
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	offers, err := windows.OfferDivaInterceptionRewards(s.charID, event.ID, eligible, s.server.erupeConfig.DebugOptions.DivaOverride)
	if err != nil || len(offers) > 32 {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	doAckBufSucceed(s, pkt.AckHandle, divaAvailableRewardPayload(offers))
}
