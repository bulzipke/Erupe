package channelserver

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

type palloneRewardRuntimeFake struct {
	*mockPalloneRotationRepo
	offeredEvent   int32
	offeredCurrent bool
	awards         []PalloneRPAward
}

func (r *palloneRewardRuntimeFake) OfferPalloneRewards(id int32, cid uint32, current bool, types []uint32, now time.Time) ([]PalloneRewardOffer, error) {
	r.offeredEvent = id
	r.offeredCurrent = current
	return []PalloneRewardOffer{{ID: 0xc000 + uint32(id), EventID: id, CharID: cid, Key: "fixture", PresentType: types[0], ItemType: palloneGP, Quantity: 600, Expires: now.Add(time.Hour)}}, nil
}
func (r *palloneRewardRuntimeFake) PreparePalloneRewardClaims(cid uint32, ids []uint32, now time.Time) ([]PalloneRewardOffer, error) {
	return []PalloneRewardOffer{{ID: ids[0], CharID: cid, ItemType: palloneGP, Quantity: 600, Expires: now.Add(time.Hour)}}, nil
}
func (r *palloneRewardRuntimeFake) FinalizePalloneEvent(int32, time.Time) (bool, error) {
	return true, nil
}
func (r *palloneRewardRuntimeFake) FinalizeEndedPalloneEvents(time.Time) error { return nil }
func (r *palloneRewardRuntimeFake) PalloneRewardCatalog(int32) (PalloneRewardCatalog, error) {
	return loadPalloneRewardCatalog(palloneRewardJSON)
}
func (r *palloneRewardRuntimeFake) PendingPalloneRP(uint32) ([]PalloneRPAward, error) {
	return r.awards, nil
}

func TestPalloneNativeRewardMenusBindCurrentChallengeAndPreviousRanks(t *testing.T) {
	s, _, rotation := palloneAutomaticSession(t)
	r := &palloneRewardRuntimeFake{mockPalloneRotationRepo: rotation}
	s.server.palloneRepo = r
	for _, tc := range []struct {
		types   []uint32
		event   uint32
		current bool
	}{{[]uint32{3301, 3302, 3303}, 100, true}, {[]uint32{3201, 3202, 3203, 3204}, 99, false}} {
		handleMsgMhfPresentBox(s, &mhfpacket.MsgMhfPresentBox{AckHandle: 71, Unk1: 1, Unk2: uint32(len(tc.types)), Unk7: tc.types})
		a := readAck(t, s)
		if a.ErrorCode != 0 || !a.IsBufferResponse || len(a.Payload) != 60 || binary.BigEndian.Uint32(a.Payload) != tc.event || binary.BigEndian.Uint32(a.Payload[12:]) != 1 || r.offeredEvent != int32(tc.event) || r.offeredCurrent != tc.current {
			t.Fatalf("wrong round or 44-byte row: %+v", a)
		}
		if binary.BigEndian.Uint32(a.Payload[48:]) != 7203 || binary.BigEndian.Uint32(a.Payload[56:]) != 600 {
			t.Fatal("wrong native GP grant layout")
		}
		if len(s.pendingPalloneRewardClaims(palloneGP)) != 0 {
			t.Fatal("opening menu staged claim")
		}
	}
	handleMsgMhfPresentBox(s, &mhfpacket.MsgMhfPresentBox{AckHandle: 72, Unk1: 3, Unk2: 3, Unk7: []uint32{3301, 3302, 3303}})
	a := readAck(t, s)
	if a.ErrorCode != 0 || a.IsBufferResponse || len(a.Payload) != 4 || binary.BigEndian.Uint32(a.Payload) != 1 {
		t.Fatal("count is not simple ACK", a)
	}
	if len(s.pendingPalloneRewardClaims(palloneGP)) != 0 {
		t.Fatal("count request claimed reward")
	}
	handleMsgMhfPresentBox(s, &mhfpacket.MsgMhfPresentBox{AckHandle: 73, Unk1: 1, Unk2: 2, Unk7: []uint32{3201, 3301}})
	if a := readAck(t, s); a.ErrorCode == 0 {
		t.Fatal("mixed rounds accepted")
	}
	if s.handlePallonePresentBox(&mhfpacket.MsgMhfPresentBox{Unk1: 1, Unk7: []uint32{260003}}) {
		t.Fatal("Pallone intercepted Tower rewards")
	}
	for _, id := range []uint32{0x00000001, 0x80000001, 0xffff2710, 0xffffbfff} {
		if s.handlePallonePresentBox(&mhfpacket.MsgMhfPresentBox{Unk1: 2, Unk2: 1, Unk7: []uint32{id}}) {
			t.Fatal("Pallone intercepted a Tower receipt", id)
		}
	}
	r.schedule.Previous = nil
	handleMsgMhfGetWeeklySeibatuRankingReward(s, &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{AckHandle: 74, Unk1: 5, Unk2: 240001})
	if a := readAck(t, s); a.ErrorCode != 0 || len(a.Payload) < 40 || binary.BigEndian.Uint32(a.Payload) != 100 {
		t.Fatal("first round ranking preview missing", a)
	}
}

func TestPalloneSystemRPSaveDoesNotConsumeClientItems(t *testing.T) {
	s, _, rotation := palloneAutomaticSession(t)
	r := &palloneRewardRuntimeFake{mockPalloneRotationRepo: rotation, awards: []PalloneRPAward{{EventID: 99, Quantity: 100}}}
	s.server.palloneRepo = r
	chars := &mockCharacterRepo{}
	s.server.charRepo = chars
	s.server.erupeConfig.GameplayOptions.MaximumRP = 65535
	save := &CharacterSaveData{CharID: s.charID, Name: "Reward", StoredName: "Reward", Mode: cfg.ZZ, Pointers: getPointers(cfg.ZZ), decompSave: make([]byte, 150000), RP: 20}
	copy(save.decompSave[88:], []byte("Reward"))
	item := PalloneRewardOffer{ID: 0xc001, CharID: s.charID, ItemType: palloneItem, ItemID: 0x22e7, Quantity: 10}
	if err := s.stagePalloneRewardClaims([]PalloneRewardOffer{item}); err != nil {
		t.Fatal(err)
	}
	chars.saveAtomicErr = errors.New("injected write failure")
	if err := s.applyPalloneRPOnLoad(save); err == nil || len(s.pendingPalloneRewardClaims(palloneItem)) != 1 {
		t.Fatal("failed RP save consumed inventory")
	}
	// The real handler reloads the durable snapshot after failure.
	save.RP = 20
	save.palloneRPEvents = nil
	chars.saveAtomicErr = nil
	if err := s.applyPalloneRPOnLoad(save); err != nil {
		t.Fatal(err)
	}
	last := chars.saveAtomicParams[len(chars.saveAtomicParams)-1]
	if save.RP != 120 || len(last.PalloneRPEvents) != 1 || last.PalloneRPEvents[0] != 99 || len(last.PalloneRewardIDs) != 0 || len(s.pendingPalloneRewardClaims(palloneItem)) != 1 {
		t.Fatal("system save consumed client reward or lost RP")
	}
}
