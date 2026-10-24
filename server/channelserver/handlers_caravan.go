package channelserver

import (
	"erupe-ce/common/byteframe"
	"erupe-ce/common/mhfpallone"
	"erupe-ce/common/stringsupport"
	"erupe-ce/network/mhfpacket"
	"time"

	"go.uber.org/zap"
)

// RyoudamaCharInfo represents one entry of the personal caravan ranking.
// Unk0 is populated with the character's caravan points -- this mirrors the
// pre-existing struct shape (CID/Unk0/Name) rather than a byte-confirmed
// field semantic.
type RyoudamaCharInfo struct {
	CID  uint32
	Unk0 int32
	Name string
}

// RyoudamaBoostInfo represents caravan boost status. No data source or wire
// layout is known for this yet -- always empty.
type RyoudamaBoostInfo struct {
	Start time.Time
	End   time.Time
}

func handleMsgMhfGetRyoudama(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetRyoudama)
	var data []*byteframe.ByteFrame
	switch pkt.Request2 {
	case 4:
		// Note: CharacterSaveData.CP (model_character.go) is a separate,
		// already-shipped "caravan points" value read/written directly from
		// the ZZ save blob -- it is NOT synced with caravanRepo's points
		// here. They likely represent the same real-world value but were
		// deliberately left unreconciled in this pass.
		points, err := s.server.caravanRepo.GetPoints(s.charID)
		if err != nil {
			s.logger.Error("Failed to get caravan points", zap.Error(err))
		}
		bf := byteframe.NewByteFrame()
		bf.WriteInt32(points.Points)
		data = append(data, bf)
	case 5:
		ranking, err := s.server.caravanRepo.GetPersonalRanking()
		if err != nil {
			s.logger.Error("Failed to get caravan personal ranking", zap.Error(err))
		}
		for _, entry := range ranking {
			bf := byteframe.NewByteFrame()
			bf.WriteUint32(entry.CharID)
			bf.WriteInt32(entry.Points)
			bf.WriteBytes(stringsupport.PaddedString(entry.Name, 14, true))
			data = append(data, bf)
		}
	case 6:
		// No known data source for caravan "boost" status; left empty.
	}
	doAckEarthSucceed(s, pkt.AckHandle, data)
}

func handleMsgMhfPostRyoudama(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfPostRyoudama)
	// Request payload beyond AckHandle is unconfirmed, so this doesn't
	// trust client-submitted values. The caravan points credit path is not
	// connected to quest completion yet. Previously this handler sent no ACK at
	// all, which is a likely softlock source for any client awaiting this
	// response.
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

// towerDailyBinSize is the client's Tower daily-mission progress: timetable
// end, six mission counters and the cleared-mission bits (8 x uint32 in client
// byte order). The client loads it with GET 0/1/1 (ZZ FUN_113ac8d0, a reply of
// any other size resets its progress) and saves it with POST 0/1/1/1 after a
// quest (ZZ FUN_10b77c10). It discards the counters itself when the timetable
// changes, so the server keeps the blob verbatim.
const towerDailyBinSize = 0x20

func isTowerDailyBin(k0, k1, k2 uint8) bool {
	return k0 == 0 && k1 == 1 && k2 == 1
}

func handleMsgMhfGetTinyBin(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetTinyBin)
	if isTowerDailyBin(pkt.Unk0, pkt.Unk1, pkt.Unk2) && s.server.towerRepo != nil {
		if _, err := s.server.towerEvent(TimeAdjusted()); err != nil {
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		data, err := s.server.towerRepo.GetTowerDailyBin(s.charID)
		if err != nil {
			s.logger.Error("Failed to read tower daily progress", zap.Error(err))
		} else if len(data) == towerDailyBinSize {
			doAckBufSucceed(s, pkt.AckHandle, data)
			return
		}
	}
	// requested after conquest quests
	doAckBufSucceed(s, pkt.AckHandle, []byte{})
}

func handleMsgMhfPostTinyBin(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfPostTinyBin)
	if isTowerDailyBin(pkt.Unk0, pkt.Unk1, pkt.Unk2) && len(pkt.Data) == towerDailyBinSize && s.server.towerRepo != nil {
		if s.server.erupeConfig.TowerRotation.Enabled {
			if _, err := s.server.towerEvent(TimeAdjusted()); err != nil {
				doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
				return
			}
		}
		event, valid := s.towerResultEvent()
		if !valid {
			doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if err := s.towerRoundRepo(event.ID).SaveTowerDailyBin(s.charID, pkt.Data); err != nil {
			s.logger.Error("Failed to save tower daily progress", zap.Error(err))
		}
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

// Festival data comes only from an explicitly registered and selected event.
// Unconfigured servers retain their native empty replies. A submit additionally
// needs a server-qualified final-route receipt; its native departure/stage/result
// capture remains unarmed pending live verification. See docs/pallone-festival.md.

func handleMsgMhfCaravanMyScore(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfCaravanMyScore)
	s.logger.Debug("Pallone score request",
		zap.Uint32("operation", pkt.Unk0), zap.Uint32("selector", pkt.Unk1),
		zap.Int32("routeKey", pkt.Unk2), zap.Int32("baseScore", pkt.Unk3),
		zap.Uint32("guildID", pkt.Unk4), zap.Int32("premiumBonus", pkt.Unk5), zap.Int32("itemBonus", pkt.Unk6))
	if pkt.Unk0 == 2 {
		s.observePallone("score_submit_reported", zap.Uint32("ackHandle", pkt.AckHandle),
			zap.Uint32("selector", pkt.Unk1), zap.Int32("routeKey", pkt.Unk2),
			zap.Int32("baseScore", pkt.Unk3), zap.Int32("premiumBonus", pkt.Unk5), zap.Int32("itemBonus", pkt.Unk6))
		s.lifecycleMu.Lock()
		var receipt PalloneRunReceipt
		if s.palloneReceipt != nil {
			receipt = *s.palloneReceipt
		}
		s.lifecycleMu.Unlock()
		if pkt.Unk1 != 3 || s.server.palloneService == nil || receipt.RunID == "" || receipt.RouteKey != pkt.Unk2 {
			s.observePallone("score_submit_rejected", zap.Uint32("ackHandle", pkt.AckHandle), zap.String("reason", "qualified_receipt_required"))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		result, err := s.server.palloneService.Submit(s.charID, receipt, PalloneSubmission{
			BaseScore: pkt.Unk3, PremiumBonus: pkt.Unk5, ItemBonus: pkt.Unk6, GuildID: pkt.Unk4,
		}, TimeAdjusted())
		if err != nil {
			s.logger.Warn("Pallone score submission rejected", zap.Error(err))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		bf := byteframe.NewByteFrame()
		bf.WriteUint32(result.Multiplier)
		doAckSimpleSucceed(s, pkt.AckHandle, bf.Data())
		return
	}
	e, err := s.server.palloneEvent(TimeAdjusted())
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	if e != nil {
		if pkt.Unk0 != 1 || pkt.Unk1 != 0 || pkt.Unk2 != 0 || pkt.Unk3 != 0 || pkt.Unk4 != 0 || pkt.Unk5 != 0 || pkt.Unk6 != 0 {
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		rows, err := s.server.palloneRepo.GetPalloneScores(e.ID, s.charID)
		if err != nil {
			s.logger.Error("Pallone score read failed", zap.Error(err))
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		data, err := mhfpallone.EncodeScores(e.ID, rows)
		if err != nil {
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		doAckBufSucceed(s, pkt.AckHandle, data)
		return
	}
	var data []*byteframe.ByteFrame
	doAckEarthSucceed(s, pkt.AckHandle, data)
}

func handleMsgMhfCaravanRanking(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfCaravanRanking)
	e, err := s.server.palloneEvent(TimeAdjusted())
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	if e != nil {
		var key *int32
		if pkt.Unk1 == 1 || pkt.Unk1 == 3 {
			key = &pkt.Unk2
		}
		if pkt.Unk0 != 0 || (key != nil && *key <= 0) || ((pkt.Unk1 == 2 || pkt.Unk1 == 4) && pkt.Unk2 != 0) {
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		var data []byte
		switch pkt.Unk1 {
		case 1, 2, 5:
			limit := mhfpallone.MaxRankingRows
			var guildID uint32
			if pkt.Unk1 == 5 {
				if pkt.Unk2 == 0 {
					doAckBufFail(s, pkt.AckHandle, nil)
					return
				}
				limit = mhfpallone.MaxGuildContributors
				guildID = uint32(pkt.Unk2)
			}
			var rows []mhfpallone.PersonalRanking
			rows, err = s.server.palloneRepo.GetPallonePersonalRanking(e.ID, key, guildID, limit)
			if err == nil {
				data, err = mhfpallone.EncodePersonalRanking(e.ID, rows, limit)
			}
		case 3, 4:
			var rows []mhfpallone.GuildRanking
			rows, err = s.server.palloneRepo.GetPalloneGuildRanking(e.ID, key)
			if err == nil {
				data, err = mhfpallone.EncodeGuildRanking(e.ID, rows)
			}
		default:
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		if err != nil {
			s.logger.Error("Pallone ranking read failed", zap.Error(err))
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		doAckBufSucceed(s, pkt.AckHandle, data)
		return
	}
	var data []*byteframe.ByteFrame
	doAckEarthSucceed(s, pkt.AckHandle, data)
}

func handleMsgMhfCaravanMyRank(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfCaravanMyRank)
	e, err := s.server.palloneEvent(TimeAdjusted())
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	if e != nil {
		if pkt.Unk0 != 0 || (pkt.Unk1 != 1 && pkt.Unk1 != 2) || (pkt.Unk1 == 1 && pkt.Unk2 != 0) || (pkt.Unk1 == 2 && pkt.Unk2 == 0) {
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		rows, err := s.server.palloneRepo.GetPalloneRanks(e.ID, s.charID, pkt.Unk2)
		if err != nil {
			s.logger.Error("Pallone personal rank read failed", zap.Error(err))
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		data, err := mhfpallone.EncodeRanks(e.ID, rows)
		if err != nil {
			doAckBufFail(s, pkt.AckHandle, nil)
			return
		}
		doAckBufSucceed(s, pkt.AckHandle, data)
		return
	}
	var data []*byteframe.ByteFrame
	doAckEarthSucceed(s, pkt.AckHandle, data)
}
