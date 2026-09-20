package channelserver

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"

	"erupe-ce/network/mhfpacket"
)

// Retain old rewards API without accidentally inheriting the optional repeat API.
type divaFiniteOnlyHandlerRepo struct {
	mockDivaRepo
	DivaRewardRepository
}

func TestDivaPrayerRotationHandlerDispatchAndStage(t *testing.T) {
	event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Add(-time.Hour).Unix())}
	s, repo := newDivaRewardHandlerTestSession(event)
	repo.progress = DivaRewardProgress{GR: 1, Points: 103000}
	item := divaPrayerRotationReward(0)
	offer := DivaRewardOffer{ID: 2345, EventID: event.ID, RewardType: 1,
		CatalogKey: item.Key, ItemType: item.ItemType, ItemID: item.ItemID, Quantity: item.Quantity}
	repo.offerOverride = []DivaRewardOffer{offer}
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 11, Unk0: 1, RewardType: 1})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, divaAvailableRewardPayload([]DivaRewardOffer{offer})) ||
		repo.rotationCalls != 1 || !reflect.DeepEqual(repo.rotationProgress, repo.progress) ||
		repo.offerEvent != event.ID || repo.offerChar != s.charID || s.hasPendingDivaRewardClaims() {
		t.Fatalf("prayer query did not use server progress and persisted offers: %+v", ack)
	}
	for _, row := range repo.candidates {
		if row.NormaRepeat || row.Key == item.Key {
			t.Fatal("display tail entered finite reward eligibility")
		}
	}
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 12, RewardType: 1, ItemIDCount: 1, RewardIDs: []uint32{offer.ID}})
	claim := readAck(t, s)
	if claim.ErrorCode != 0 || !s.hasPendingDivaRewardClaims() || repo.prepareCalls != 1 {
		t.Fatalf("repeat receipt did not enter normal save pipeline: %+v", claim)
	}
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 13, Unk0: 1, RewardType: 1})
	blocked := readAck(t, s)
	if blocked.ErrorCode != 0 || !bytes.Equal(blocked.Payload, []byte{0, 0}) || repo.rotationCalls != 1 {
		t.Fatal("pending save allowed another repeat offer")
	}
}

func TestDivaPrayerRotationHandlerFailsClosed(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "repository error", true: "missing rotation API"}[unavailable], func(t *testing.T) {
			event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Add(-time.Hour).Unix())}
			s, repo := newDivaRewardHandlerTestSession(event)
			repo.progress = DivaRewardProgress{GR: 1, Points: 103000}
			if unavailable {
				s.server.divaRepo = &divaFiniteOnlyHandlerRepo{mockDivaRepo: repo.mockDivaRepo, DivaRewardRepository: repo}
			} else {
				repo.offerErr = errors.New("rotation persistence unavailable")
			}
			handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 14, Unk0: 1, RewardType: 1})
			if ack := readAck(t, s); ack.ErrorCode == 0 || s.hasPendingDivaRewardClaims() {
				t.Fatalf("unsupported rotation was acknowledged as successful: %+v", ack)
			}
		})
	}
}

func TestDivaPrayerRotationDisplayRowsCannotBeOffered(t *testing.T) {
	row := divaPrayerRotationReward(0)
	if err := validateDivaRewardCatalog(1, []DivaRewardCatalogEntry{row}); err != nil {
		t.Fatal(err)
	}
	row.NormaRepeat = true
	if err := validateDivaRewardCatalog(1, []DivaRewardCatalogEntry{row}); err == nil {
		t.Fatal("display-only repeat marker accepted as a real finite entitlement")
	}
}

func TestDivaPrayerRotationFingerprint(t *testing.T) {
	original := divaPrayerRotationFingerprint(divaPrayerRotation())
	for name, mutate := range map[string]func(*DivaPrayerRotation){
		"start":    func(r *DivaPrayerRotation) { r.Start++ },
		"interval": func(r *DivaPrayerRotation) { r.Interval++ },
		"quantity": func(r *DivaPrayerRotation) { r.Items[0].Quantity++ },
		"item":     func(r *DivaPrayerRotation) { r.Items[0].ItemID++ },
		"order":    func(r *DivaPrayerRotation) { r.Items[0], r.Items[1] = r.Items[1], r.Items[0] },
		"count":    func(r *DivaPrayerRotation) { r.Items = append(r.Items, r.Items[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			rotation := divaPrayerRotation()
			mutate(&rotation)
			if divaPrayerRotationFingerprint(rotation) == original {
				t.Fatal("payout policy change retained the same fingerprint")
			}
		})
	}
	rotation := divaPrayerRotation()
	rotation.Items[0].Basis = "documentation clarified"
	if divaPrayerRotationFingerprint(rotation) != original {
		t.Fatal("source annotation change invalidated an unchanged payout policy")
	}
}
