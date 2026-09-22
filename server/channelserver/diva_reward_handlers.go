package channelserver

import (
	"sort"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

func divaDailyRewardPayload(rewards []DivaRewardCatalogEntry) []byte {
	bf := byteframe.NewByteFrame()
	var rows []DivaRewardCatalogEntry
	for _, r := range rewards {
		if r.RewardType == 0 {
			rows = append(rows, r)
		}
	}
	bf.WriteUint16(uint16(len(rows)))
	for _, r := range rows {
		bf.WriteUint8(r.ItemType)
		bf.WriteUint16(r.ItemID)
		bf.WriteUint16(r.Quantity)
		if r.GR {
			bf.WriteUint8(1)
		} else {
			bf.WriteUint8(0)
		}
		lower, upper := uint32(1), uint32(999)
		if !r.GR {
			lower, upper = uint32(r.MinHR), uint32(r.MaxHR)
		}
		bf.WriteUint32(upper) // Native HR2-4/HR5-7 split is 99/100, not 4/5.
		bf.WriteUint32(lower)
		bf.WriteUint8(uint8(r.Threshold))
	}
	return bf.Data()
}

// ZZ 11535320 consumes 19-byte norma rows. The final byte is read by 1039def0
// in pagination's repeat-group detection. Finite milestones keep it zero.
// Known display limitation: 103b5ec0 always renders the last norma page as a
// repeating schedule, using its first two thresholds for start/interval.
// A finite-only catalog therefore displays an incorrect repeat header. The
// prayer display helper appends the same real rotation the server pays; no
// dummy rewards are used to influence pagination.
func divaNormaRewardPayload(rewards []DivaRewardCatalogEntry) []byte {
	bf := byteframe.NewByteFrame()
	var rows []DivaRewardCatalogEntry
	for _, r := range rewards {
		if r.RewardType == 1 {
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Threshold < rows[j].Threshold })
	bf.WriteUint16(uint16(len(rows)))
	for _, r := range rows {
		bf.WriteUint8(r.ItemType)
		bf.WriteUint16(r.ItemID)
		bf.WriteUint16(r.Quantity)
		if r.GR {
			bf.WriteUint8(1)
			bf.WriteUint32(999)
			bf.WriteUint32(1)
		} else {
			bf.WriteUint8(0)
			bf.WriteUint32(uint32(r.MaxHR))
			bf.WriteUint32(uint32(r.MinHR))
		}
		bf.WriteUint32(r.Threshold)
		if r.NormaRepeat {
			bf.WriteUint8(1)
		} else {
			bf.WriteUint8(0)
		}
	}
	return bf.Data()
}

func divaRankingRewardPayload(rewards []DivaRewardCatalogEntry) []byte {
	bf := byteframe.NewByteFrame()
	var rows []DivaRewardCatalogEntry
	for _, r := range rewards {
		if r.RewardType == 2 || r.RewardType == 3 {
			rows = append(rows, r)
		}
	}
	bf.WriteUint16(uint16(len(rows)))
	for _, r := range rows {
		bf.WriteUint8(r.ItemType)
		bf.WriteUint16(r.ItemID)
		bf.WriteUint16(r.Quantity)
		bf.WriteUint8(r.RewardType - 2) // Native selector: personal=0, guild=1.
		bf.WriteUint32(r.Upper)
		bf.WriteUint32(r.Lower)
	}
	return bf.Data()
}

func divaAvailableRewardPayload(offers []DivaRewardOffer) []byte {
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0)
	bf.WriteUint8(uint8(len(offers))) // Repository caps native query batches at 32.
	for _, r := range offers {
		bf.WriteUint32(r.ID)
		bf.WriteUint8(r.ItemType)
		bf.WriteUint16(r.ItemID)
		bf.WriteUint16(r.Quantity)
	}
	return bf.Data()
}

func eligibleDivaSongRewards(kind uint8, progress DivaRewardProgress) []DivaRewardCatalogEntry {
	var result []DivaRewardCatalogEntry
	if progress.Points <= 0 {
		return result
	}
	catalog := divaSongRewardCatalog()
	if kind == 1 {
		catalog = divaPrayerRewardCatalog()
	}
	for _, r := range catalog {
		if r.RewardType != kind {
			continue
		}
		switch kind {
		case 0:
			if progress.ParticipationDays < r.Threshold ||
				(r.GR && progress.GR == 0) || (!r.GR && (progress.GR > 0 || progress.HR < r.MinHR || progress.HR > r.MaxHR)) {
				continue
			}
		case 2:
			if progress.Rank < r.Lower || progress.Rank > r.Upper {
				continue
			}
		case 1:
			if !divaRewardRankMatches(r, progress.HR, progress.GR) || progress.Points < int64(r.Threshold) {
				continue
			}
		case 3:
			if progress.GuildRank < r.Lower || progress.GuildRank > r.Upper {
				continue
			}
		default:
			continue
		}
		result = append(result, r)
	}
	return result
}

func handleDivaRewardAcquire(s *Session, pkt *mhfpacket.MsgMhfAcquireUdItem) {
	if pkt.Unk0 > 1 || pkt.RewardType > 7 || len(pkt.RewardIDs) > 32 ||
		(pkt.Unk0 == 0 && int(pkt.ItemIDCount) != len(pkt.RewardIDs)) ||
		(pkt.Unk0 == 1 && len(pkt.RewardIDs) != 0) {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	empty := func() { doAckBufSucceed(s, pkt.AckHandle, divaAvailableRewardPayload(nil)) }
	if pkt.Unk0 == 0 && len(pkt.RewardIDs) == 0 {
		empty()
		return
	}
	if s.server.erupeConfig.RealClientMode != cfg.ZZ || s.charID == 0 || s.server.erupeConfig.DebugOptions.DivaOverride == 0 {
		if pkt.Unk0 == 1 {
			empty()
		} else {
			doAckBufFail(s, pkt.AckHandle, nil)
		}
		return
	}
	repo, ok := s.server.divaRepo.(DivaRewardRepository)
	if !ok {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	if pkt.Unk0 == 0 {
		var offers []DivaRewardOffer
		var err error
		if pkt.RewardType == 6 {
			// Publish the current schedule before a claim checks the next actual
			// interception boundary. Never derive expiry from a client receipt ID.
			if _, err := s.divaEvent(); err != nil {
				doAckBufFail(s, pkt.AckHandle, nil)
				return
			}
			windowRepo, ok := s.server.divaRepo.(DivaInterceptionRewardWindowRepository)
			if !ok {
				doAckBufFail(s, pkt.AckHandle, nil)
				return
			}
			offers, err = windowRepo.PrepareDivaInterceptionRewardClaims(s.charID, pkt.RewardIDs, s.server.erupeConfig.DebugOptions.DivaOverride)
		} else if pkt.RewardType == 5 {
			if _, err := s.divaEvent(); err != nil {
				doAckBufFail(s, pkt.AckHandle, nil)
				return
			}
			treasureRepo, ok := s.server.divaRepo.(DivaTreasureRewardRepository)
			if !ok {
				doAckBufFail(s, pkt.AckHandle, nil)
				return
			}
			offers, err = treasureRepo.PrepareDivaTreasureRewardClaims(s.charID, pkt.RewardIDs, s.server.erupeConfig.DebugOptions.DivaOverride)
		} else if pkt.RewardType == 7 {
			if _, err := s.divaEvent(); err != nil {
				doAckBufFail(s, pkt.AckHandle, nil)
				return
			}
			guildRepo, ok := s.server.divaRepo.(DivaGuildRewardRepository)
			if !ok {
				doAckBufFail(s, pkt.AckHandle, nil)
				return
			}
			offers, err = guildRepo.PrepareDivaGuildRewardClaims(s.charID, pkt.RewardIDs, s.server.erupeConfig.DebugOptions.DivaOverride)
		} else {
			offers, err = repo.PrepareDivaRewardClaims(s.charID, pkt.RewardType, pkt.RewardIDs)
		}
		if err == nil {
			err = s.stageDivaRewardClaims(offers)
		}
		if err != nil {
			s.logger.Warn("Rejected Diva reward receipt", zap.Uint32("charID", s.charID), zap.Error(err))
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		// The client queues claim BEFORE SAVEDATA and SAVE_MERCENARY. Acknowledge
		// receipt, but consume it only with the corresponding successful save.
		empty()
		return
	}
	if s.hasPendingDivaRewardClaims() {
		s.logger.Warn("Diva reward query before pending reward saves completed", zap.Uint32("charID", s.charID))
		empty()
		return
	}
	// Other catalogs do not yet have sufficiently verified eligibility state.
	if pkt.RewardType != 0 && pkt.RewardType != 1 && pkt.RewardType != 2 && pkt.RewardType != 3 && pkt.RewardType != 5 && pkt.RewardType != 6 && pkt.RewardType != 7 {
		empty()
		return
	}
	event, err := s.divaEvent()
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	now := TimeAdjusted()
	if pkt.RewardType == 5 {
		treasureRepo, ok := s.server.divaRepo.(DivaTreasureRewardRepository)
		if !ok {
			empty()
			return
		}
		offers, err := treasureRepo.OfferDivaTreasureRewards(s.charID, s.server.erupeConfig.DebugOptions.DivaOverride)
		if err != nil || len(offers) > 32 {
			s.logger.Warn("Failed to offer Diva branch treasures", zap.Error(err), zap.Int("count", len(offers)))
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		doAckBufSucceed(s, pkt.AckHandle, divaAvailableRewardPayload(offers))
		return
	}
	if pkt.RewardType == 7 {
		guildRepo, ok := s.server.divaRepo.(DivaGuildRewardRepository)
		if !ok {
			empty()
			return
		}
		offers, err := guildRepo.OfferDivaGuildRewards(s.charID, s.server.erupeConfig.DebugOptions.DivaOverride)
		if err != nil || len(offers) > 32 {
			s.logger.Warn("Failed to offer Diva guild rewards", zap.Error(err), zap.Int("count", len(offers)))
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		doAckBufSucceed(s, pkt.AckHandle, divaAvailableRewardPayload(offers))
		return
	}
	if pkt.RewardType == 6 {
		handleDivaInterceptionRewardQuery(s, pkt, repo, now)
		return
	}
	start := time.Unix(int64(event.StartTime), 0)
	songEnd := start.Add(time.Duration(divaPhaseDuration) * time.Second)
	if event.ID == 0 || now.Before(start) || now.Unix() >= int64(event.StartTime)+divaTotalLifespan {
		empty()
		return
	}
	cutoff := now.Add(time.Nanosecond)
	if cutoff.After(songEnd) {
		cutoff = songEnd
	}
	if (pkt.RewardType == 2 || pkt.RewardType == 3) && (now.Before(songEnd.Add(time.Duration(divaInterlude)*time.Second)) || s.server.erupeConfig.DebugOptions.DivaOverride == 1) {
		empty()
		return // Ranking is final only after the prayer phase has ended.
	}
	progress, err := repo.GetDivaRewardProgress(s.charID, event.ID, start, cutoff)
	if err != nil {
		s.logger.Warn("Failed to read Diva reward eligibility", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	var offers []DivaRewardOffer
	if pkt.RewardType == 1 {
		rotationRepo, ok := s.server.divaRepo.(DivaPrayerRotationRepository)
		if !ok {
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		offers, err = rotationRepo.OfferDivaPrayerRewards(s.charID, event.ID, progress)
	} else {
		eligible := eligibleDivaSongRewards(pkt.RewardType, progress)
		offers, err = repo.OfferDivaRewards(s.charID, event.ID, pkt.RewardType, eligible)
	}
	if err != nil || len(offers) > 32 {
		s.logger.Warn("Failed to offer Diva rewards", zap.Error(err), zap.Int("count", len(offers)))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	doAckBufSucceed(s, pkt.AckHandle, divaAvailableRewardPayload(offers))
}
