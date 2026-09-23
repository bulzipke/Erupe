package channelserver

import (
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
)

const (
	towerPresentAdvance uint32 = 260001
	towerPresentDaily   uint32 = 260002 // inferred from adjacent Tower present types
	towerPresentFloor   uint32 = 260003
)

type towerPresentReward struct {
	kind        int32
	index       int32
	claimIndex  uint32
	presentType uint32
	itemID      uint16
	quantity    uint16
}

func towerDailyRewardIndex(dayStart time.Time, slot int) int32 {
	return int32(dayStart.UTC().Unix()/86400)*8 + int32(slot)
}

func towerClaimIndex(kind, index int32) uint32 {
	return uint32(kind*1_000_000 + index)
}

func towerPendingRewards(state TowerRewardState, requested []uint32) []towerPresentReward {
	requestedTypes := make(map[uint32]bool, len(requested))
	for _, presentType := range requested {
		requestedTypes[presentType] = true
	}
	var out []towerPresentReward
	add := func(kind, index int32, presentType uint32, itemID uint16, quantity uint16) {
		if !requestedTypes[presentType] || state.Claimed[towerRewardKey(kind, index)] {
			return
		}
		out = append(out, towerPresentReward{
			kind: kind, index: index, claimIndex: towerClaimIndex(kind, index),
			presentType: presentType, itemID: itemID, quantity: quantity,
		})
	}
	for _, reward := range towerFloorRewards {
		if reward.Unk0 > 0 && state.Floors >= reward.Unk0 {
			add(towerRewardFloor, reward.Unk0, towerPresentFloor,
				uint16(reward.Unk4), uint16(reward.Unk5))
		}
	}
	if state.AdvanceEligible {
		reward := towerAdvanceRewards[0]
		add(towerRewardAdvance, 1, towerPresentAdvance,
			uint16(reward.Unk4), uint16(reward.Unk5))
	}
	for _, day := range state.Daily {
		for slot, mission := range towerDailyMissions(day.Start) {
			if towerDailyMissionMet(day.Counters, mission) {
				add(towerRewardDaily, towerDailyRewardIndex(day.Start, slot+1),
					towerPresentDaily, mission.Reward1ID, uint16(mission.Reward1Quantity))
			}
		}
	}
	return out
}

func towerPresentFrames(rewards []towerPresentReward) []*byteframe.ByteFrame {
	frames := make([]*byteframe.ByteFrame, 0, len(rewards))
	for _, reward := range rewards {
		bf := byteframe.NewByteFrame()
		bf.WriteUint32(reward.claimIndex)
		bf.WriteInt32(int32(reward.presentType))
		for i := 0; i < 6; i++ {
			bf.WriteInt32(0)
		}
		bf.WriteInt32(7201) // Item
		bf.WriteInt32(int32(reward.itemID))
		bf.WriteInt32(int32(reward.quantity))
		frames = append(frames, bf)
	}
	return frames
}

// towerPresentPageSize is the number of rows the client reads per list page.
const towerPresentPageSize = 0x100

// towerPresentPage returns one list page. While receiving, the client lists at
// offset 0, claims what it stored, then lists again at offset+0x100 until a
// page comes back empty, so an offset past the end must yield no rows (a page
// that kept repeating rows the hunter had no room for would never end).
func towerPresentPage(rewards []towerPresentReward, offset uint32) []towerPresentReward {
	if uint64(offset) >= uint64(len(rewards)) {
		return nil
	}
	end := uint64(offset) + towerPresentPageSize
	if end > uint64(len(rewards)) {
		end = uint64(len(rewards))
	}
	return rewards[offset:end]
}

// towerClaimRequestRewards resolves the claim indexes of an Op=2 request: the
// first field of every list row the client stored. Rows it had no room for are
// left out by the client itself. Indexes that are not pending (already
// claimed, or never issued) are returned separately.
func towerClaimRequestRewards(ids []uint32, rewards []towerPresentReward) (claims []towerPresentReward, unknown []uint32) {
	byIndex := make(map[uint32]towerPresentReward, len(rewards))
	for _, reward := range rewards {
		byIndex[reward.claimIndex] = reward
	}
	seen := make(map[uint32]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if reward, ok := byIndex[id]; ok {
			claims = append(claims, reward)
		} else {
			unknown = append(unknown, id)
		}
	}
	return claims, unknown
}

// towerPresentAck answers Op=2/Op=3 with a simple ACK carrying value (the
// client registers those requests without a response buffer, so a buffer ACK
// fails them with error 0x12) and Op=1 with an empty list.
func towerPresentAck(s *Session, pkt *mhfpacket.MsgMhfPresentBox, value uint32, fail bool) {
	if pkt.Unk1 != 2 && pkt.Unk1 != 3 {
		doAckEarthSucceed(s, pkt.AckHandle, nil)
		return
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint32(value)
	if fail {
		doAckSimpleFail(s, pkt.AckHandle, bf.Data())
		return
	}
	doAckSimpleSucceed(s, pkt.AckHandle, bf.Data())
}

// handleTowerPresentBox serves the Tenrou contribution rewards
// (Mazetower::mt_PresentCommunicator, present types 260003 and 260001).
//
// ZZ client flow: opening the reward window (FUN_105c9bb0) sends Op=1, the
// list (buffer ACK of 44-byte rows), and Op=3, the number of receivable rewards
// (simple ACK; the window warns when it is 257 or more), together. Op=3 is not
// a receipt: treating it as one used to grant everything into the warehouse
// the moment the window opened and left the receive step an empty list.
// Receiving (FUN_1068fe00) lists with Op=1 at page offset Unk3, puts every row
// it has room for into the hunter's inventory itself, then sends Op=2 with the
// claim indexes of those rows (Unk2 = count, Unk7) alongside a savedata upload,
// and repeats at offset+0x100 until a page is empty. The server therefore only
// records receipts.
func handleTowerPresentBox(s *Session, pkt *mhfpacket.MsgMhfPresentBox) {
	if pkt.Unk1 != 1 && pkt.Unk1 != 2 && pkt.Unk1 != 3 {
		doAckEarthSucceed(s, pkt.AckHandle, nil)
		return
	}
	if s.server.towerRepo == nil {
		towerPresentAck(s, pkt, 0, false)
		return
	}
	earthID := s.server.erupeConfig.EarthID
	state, err := s.server.towerRepo.GetTowerRewardState(earthID, s.charID, towerDailyStart(TimeAdjusted()))
	if err != nil {
		s.logger.Error("Failed to list Tower rewards", zap.Error(err))
		towerPresentAck(s, pkt, 0, pkt.Unk1 == 2)
		return
	}
	if state.Claimed == nil {
		state.Claimed = make(map[uint64]bool)
	}
	switch pkt.Unk1 {
	case 1:
		pending := towerPendingRewards(state, pkt.Unk7)
		page := towerPresentPage(pending, pkt.Unk3)
		s.logger.Info("Tower present list", zap.Uint32("charID", s.charID),
			zap.Uint32s("presentTypes", pkt.Unk7), zap.Uint32("offset", pkt.Unk3),
			zap.Int("pending", len(pending)), zap.Int("rows", len(page)))
		doAckEarthSucceed(s, pkt.AckHandle, towerPresentFrames(page))
	case 3:
		pending := towerPendingRewards(state, pkt.Unk7)
		s.logger.Info("Tower present count", zap.Uint32("charID", s.charID),
			zap.Uint32s("presentTypes", pkt.Unk7), zap.Int("pending", len(pending)))
		towerPresentAck(s, pkt, uint32(len(pending)), false)
	case 2:
		// Claim indexes identify the reward, so match them against every type.
		pending := towerPendingRewards(state, []uint32{towerPresentFloor, towerPresentAdvance, towerPresentDaily})
		claims, unknown := towerClaimRequestRewards(pkt.Unk7, pending)
		recorded, repeated := 0, 0
		for _, reward := range claims {
			inserted, err := s.server.towerRepo.RecordTowerRewardClaim(earthID, s.charID,
				reward.kind, reward.index, reward.itemID, reward.quantity)
			if err != nil {
				s.logger.Error("Tower reward receipt failed", zap.Uint32("charID", s.charID),
					zap.Uint32("claimIndex", reward.claimIndex), zap.Error(err))
				towerPresentAck(s, pkt, 0, true)
				return
			}
			if inserted {
				recorded++
			} else {
				repeated++
			}
		}
		s.logger.Info("Tower present claim", zap.Uint32("charID", s.charID),
			zap.Int("requested", len(pkt.Unk7)), zap.Int("recorded", recorded),
			zap.Int("repeated", repeated), zap.Uint32s("notPending", unknown))
		towerPresentAck(s, pkt, 0, false)
	}
}
