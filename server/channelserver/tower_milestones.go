package channelserver

import (
	"errors"
	"fmt"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

// postTowerMilestone distinguishes the restored departure IT6 from the native
// result IT6. Inspect the reserved Qs stage too: the G10 sender runs before the
// transition into combat. A kill/result receipt is neither needed nor sufficient.
// This restoration is ZZ-only; the setup decoder is verified for ZZ's 144 bytes.
func (s *Session) postTowerMilestone(pkt *mhfpacket.MsgMhfPostTowerInfo) (bool, error) {
	if s.server.erupeConfig.RealClientMode != cfg.ZZ {
		return false, nil
	}
	event, eventErr := s.server.towerEvent(TimeAdjusted())
	active := s.server.erupeConfig.EarthStatus == 21
	if s.server.erupeConfig.TowerRotation.Enabled {
		active = eventErr == nil && event.Active(TimeAdjusted())
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.Lock()
	reserved, current := s.reservationStage, s.stage
	s.Unlock()
	for _, stage := range []*Stage{reserved, current} {
		if stage == nil {
			continue
		}
		stage.RLock()
		setup := stage.rawBinaryData[stageBinaryKey{1, 3}]
		qid, _, floor, decoded := decodeQuestRunSetupFromStageBinary(stage.id, 1, 3, setup)
		// Road forces record+0x27 bit 1; Tower's two-floor recipe leaves it clear.
		milestone := decoded && (qid == towerQuestMilestone1 || qid == towerQuestMilestone2) &&
			setup[questRunStageQuestIDOffset+0x27]&2 == 0 && towerGuardianFloor(int32(floor))
		_, member := stage.clients[s]
		_, waiting := stage.reservedClientSlots[s.charID]
		host := stage.host == s
		stage.RUnlock()
		if !decoded || (!milestone && qid != towerQuestGuardian1 && qid != towerQuestGuardian2) {
			continue
		}
		// Treat a malformed/guest guardian IT6 as handled and fail it; never let
		// it fall through to the normal floor-report/daily-submission branch.
		block := uint8(1)
		if qid == towerQuestGuardian2 || qid == towerQuestMilestone2 {
			block = 2
		}
		if s.closed.Load() || !host || (!member && !waiting) || !active ||
			pkt.Unk1 != 1 || pkt.Unk6 != int32(block) || pkt.Block1 > maxTowerFloorReport || !towerGuardianFloor(pkt.Block1) || (milestone && int32(floor) != pkt.Block1) {
			return true, errors.New("invalid Tower milestone host, block or floor")
		}
		td, err := s.server.towerRepo.GetTowerData(s.charID)
		if err != nil {
			return true, err
		}
		reached := td.Block1
		if block == 2 {
			reached = td.Block2
		}
		if pkt.Block1 != reached {
			return true, fmt.Errorf("milestone floor %d does not match reached floor %d", pkt.Block1, reached)
		}
		return true, s.towerRoundRepo(event.ID).PassTowerMilestone(s.charID, block, reached)
	}
	return false, nil
}

// PassTowerMilestone is monotonic and idempotent. Re-check the current floor in
// the UPDATE so a concurrent progress report cannot consume an unrelated floor.
func (r *TowerRepository) PassTowerMilestone(charID uint32, block uint8, floor int32) error {
	if charID == 0 || (block != 1 && block != 2) || floor > maxTowerFloorReport || !towerGuardianFloor(floor) {
		return errors.New("invalid Tower milestone")
	}
	column, reached := "guardian1", "block1"
	if block == 2 {
		column, reached = "guardian2", "block2"
	}
	result, err := r.execTowerRound(`UPDATE tower SET `+column+`=GREATEST(`+column+`, $1)
		WHERE char_id=$2 AND COALESCE(`+reached+`, 0)=$1`, floor, charID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("missing or changed Tower milestone")
	}
	return nil
}
