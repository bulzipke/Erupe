package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
	"time"

	"erupe-ce/common/mhfpallone"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func palloneCoexistenceSession() (*Session, *mockTowerRotationRepo, *mockPalloneRepo) {
	s, tower := towerRotationSession()
	c := s.server.erupeConfig
	c.EarthStatus, c.EarthID = 21, 7
	c.EarthMonsters = []int32{100, 200, 300, 400}
	c.PalloneFestival = cfg.PalloneFestivalOptions{Enabled: true, EventID: 99}
	now := TimeAdjusted()
	pallone := &mockPalloneRepo{event: &PalloneEvent{ID: 99, Start: now.Add(-time.Hour), End: now.Add(time.Hour), ClaimUntil: now.Add(2 * time.Hour)}}
	s.server.palloneRepo = pallone
	s.server.palloneService = NewPalloneService(pallone)
	return s, tower, pallone
}

// The native HD/SD cache derives count as payloadBytes >> 5 and searches by
// category plus date. No packet-count header and no shared event ID are used.
func nativeCalendarEvent(payload []byte, category uint32, now time.Time) uint32 {
	for i := 0; i < len(payload)>>5; i++ {
		row := payload[i*32 : (i+1)*32]
		if binary.BigEndian.Uint32(row[8:]) == category && binary.BigEndian.Uint32(row) < uint32(now.Unix()) && uint32(now.Unix()) < binary.BigEndian.Uint32(row[4:]) {
			return binary.BigEndian.Uint32(row[12:])
		}
	}
	return 0
}

func TestPalloneCalendarRetainsTowerBytesAndIndependentID(t *testing.T) {
	s, tower, pallone := palloneCoexistenceSession()
	c := s.server.erupeConfig
	rotation, status, id := c.TowerRotation, c.EarthStatus, c.EarthID
	c.PalloneFestival.Enabled = false
	handleMsgMhfGetEarthStatus(s, &mhfpacket.MsgMhfGetEarthStatus{AckHandle: 1})
	before := readAck(t, s)
	c.PalloneFestival.Enabled = true
	handleMsgMhfGetEarthStatus(s, &mhfpacket.MsgMhfGetEarthStatus{AckHandle: 2})
	after := readAck(t, s)
	if before.ErrorCode != 0 || after.ErrorCode != 0 || len(before.Payload) != 32 || len(after.Payload) != 96 || !bytes.Equal(before.Payload, after.Payload[:32]) {
		t.Fatalf("changed Tower row: %x / %x", before.Payload, after.Payload)
	}
	now := TimeAdjusted()
	if nativeCalendarEvent(after.Payload, 21, now) != uint32(tower.event.ID) || nativeCalendarEvent(after.Payload, 11, now) != uint32(pallone.event.ID) {
		t.Fatal("native client cannot select both independent rows")
	}
	if !reflect.DeepEqual(rotation, c.TowerRotation) || status != c.EarthStatus || id != c.EarthID || pallone.eventID != 99 {
		t.Fatal("mutated or borrowed Tower settings for the festival")
	}
}

func TestPalloneCalendarIndependentPhasesAndFailures(t *testing.T) {
	for _, kind := range []string{"active", "claim", "future", "expired", "unregistered", "festival read error", "invalid selector", "invalid window"} {
		t.Run(kind, func(t *testing.T) {
			s, tower, pallone := palloneCoexistenceSession()
			now := TimeAdjusted()
			count, category := 3, uint32(11)
			switch kind {
			case "claim":
				pallone.event.End = now.Add(-time.Minute)
				category = 12
			case "future":
				pallone.event.Start = now.Add(time.Minute)
				category = 0
			case "expired":
				pallone.event.Start = now.Add(-3 * time.Hour)
				pallone.event.End = now.Add(-2 * time.Hour)
				pallone.event.ClaimUntil = now.Add(-time.Minute)
				category = 0
			case "unregistered":
				pallone.event = nil
				count = 1
			case "festival read error":
				pallone.err = errors.New("festival database unavailable")
				count = 1
			case "invalid selector":
				s.server.erupeConfig.PalloneFestival.EventID = -1
				count = 1
			case "invalid window":
				pallone.event.End = pallone.event.Start
				count = 1
			}
			handleMsgMhfGetEarthStatus(s, &mhfpacket.MsgMhfGetEarthStatus{AckHandle: 3})
			a := readAck(t, s)
			if a.ErrorCode != 0 || len(a.Payload) != count*32 || nativeCalendarEvent(a.Payload, 21, now) != uint32(tower.event.ID) {
				t.Fatalf("festival phase/error changed Tower availability: %+v", a)
			}
			if count == 3 {
				if category != 0 && nativeCalendarEvent(a.Payload, category, now) != 99 {
					t.Fatal("wrong festival phase or ID")
				}
				if category == 0 && (nativeCalendarEvent(a.Payload, 11, now) != 0 || nativeCalendarEvent(a.Payload, 12, now) != 0) {
					t.Fatal("future/expired calendar opened a festival phase")
				}
				if binary.BigEndian.Uint32(a.Payload[32:]) != uint32(pallone.event.Start.Unix()) || binary.BigEndian.Uint32(a.Payload[36:]) != uint32(pallone.event.End.Unix()) ||
					binary.BigEndian.Uint32(a.Payload[64:]) != uint32(pallone.event.End.Unix()) || binary.BigEndian.Uint32(a.Payload[68:]) != uint32(pallone.event.ClaimUntil.Unix()) ||
					binary.BigEndian.Uint32(a.Payload[40:]) != 11 || binary.BigEndian.Uint32(a.Payload[72:]) != 12 {
					t.Fatal("festival inherited Tower dates")
				}
			}
			if kind == "festival read error" || kind == "invalid selector" {
				handleMsgMhfCaravanMyScore(s, &mhfpacket.MsgMhfCaravanMyScore{AckHandle: 4, Unk0: 1})
				if readAck(t, s).ErrorCode == 0 {
					t.Fatal("festival failure accepted its own read")
				}
			}
		})
	}
}

func TestPalloneCalendarDoesNotOpenClosedTowerOrReplaceItsFailure(t *testing.T) {
	s, tower, _ := palloneCoexistenceSession()
	tower.event.End = TimeAdjusted().Add(-time.Minute)
	handleMsgMhfGetEarthStatus(s, &mhfpacket.MsgMhfGetEarthStatus{AckHandle: 5})
	a := readAck(t, s)
	if a.ErrorCode != 0 || len(a.Payload) != 96 || binary.BigEndian.Uint32(a.Payload[8:]) != 0 || nativeCalendarEvent(a.Payload, 11, TimeAdjusted()) != 99 {
		t.Fatal("festival reopened Tower or closed itself")
	}
	var cache *TowerData
	if s.allowsTowerQuest(EventQuest{QuestType: 55, QuestID: towerQuestPrologue}, &cache) {
		t.Fatal("festival bypassed Tower's own date gate")
	}
	tower.err = errors.New("Tower lifecycle unavailable")
	handleMsgMhfGetEarthStatus(s, &mhfpacket.MsgMhfGetEarthStatus{AckHandle: 6})
	if readAck(t, s).ErrorCode == 0 {
		t.Fatal("hid primary Tower failure with a festival response")
	}
}

func TestPalloneReadsAndDeliveryKeepTheirIDWithTowerEnabled(t *testing.T) {
	s, _, pallone := palloneCoexistenceSession()
	installPalloneTestProfile(t, s, pallone)
	pallone.scores = []mhfpallone.Score{{RouteKey: 1, Total: 100, Best: 100}}
	handleMsgMhfCaravanMyScore(s, &mhfpacket.MsgMhfCaravanMyScore{AckHandle: 7, Unk0: 1})
	a := readAck(t, s)
	if a.ErrorCode != 0 || binary.BigEndian.Uint32(a.Payload) != 99 || pallone.eventID != 99 {
		t.Fatal("score read inherited Tower event")
	}
	handleMsgMhfGetWeeklySeibatuRankingReward(s, &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{AckHandle: 8, Unk1: 3})
	a = readAck(t, s)
	if a.ErrorCode != 0 || len(a.Payload) != 40 || binary.BigEndian.Uint32(a.Payload) != 99 || binary.BigEndian.Uint32(a.Payload[12:]) != 1 {
		t.Fatal("Tower selection suppressed or relabelled festival delivery")
	}
	if !s.server.erupeConfig.TowerRotation.Enabled || s.server.erupeConfig.EarthID != 7 || s.palloneReceipt != nil || pallone.settled != 0 {
		t.Fatal("delivery mutated Tower configuration or issued festival points")
	}
}

func TestPalloneEmptyReadsNeverBorrowTowerID(t *testing.T) {
	for _, kind := range []string{"future", "expired", "unregistered"} {
		t.Run(kind, func(t *testing.T) {
			s, _, p := palloneCoexistenceSession()
			switch kind {
			case "future":
				p.event.Start = TimeAdjusted().Add(time.Hour)
			case "expired":
				p.event.ClaimUntil = TimeAdjusted().Add(-time.Hour)
			case "unregistered":
				p.event = nil
			}
			calls := []func(){
				func() { handleMsgMhfCaravanMyScore(s, &mhfpacket.MsgMhfCaravanMyScore{AckHandle: 9, Unk0: 1}) },
				func() { handleMsgMhfCaravanRanking(s, &mhfpacket.MsgMhfCaravanRanking{AckHandle: 9, Unk1: 2}) },
				func() { handleMsgMhfCaravanMyRank(s, &mhfpacket.MsgMhfCaravanMyRank{AckHandle: 9, Unk1: 1}) },
				func() {
					handleMsgMhfGetWeeklySeibatuRankingReward(s, &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{AckHandle: 9, Unk1: 3})
				},
			}
			for _, call := range calls {
				call()
				a := readAck(t, s)
				if a.ErrorCode != 0 || len(a.Payload) != 16 || binary.BigEndian.Uint32(a.Payload) != 99 || binary.BigEndian.Uint32(a.Payload[12:]) != 0 {
					t.Fatalf("empty festival read inherited Tower ID: %+v", a)
				}
			}
		})
	}
}

func TestPalloneDoesNotChangeTowerDepartureSettlementAcrossRollover(t *testing.T) {
	s, tower, pallone := palloneCoexistenceSession()
	s.questWeaponGeneration = 1
	s.pinTowerDeparture(tower.event, TimeAdjusted())
	tower.event = TowerEvent{ID: 37, Start: TimeAdjusted().Add(-time.Minute), End: TimeAdjusted().Add(time.Hour), ClaimUntil: TimeAdjusted().Add(24 * time.Hour)}
	handleMsgMhfGetEarthStatus(s, &mhfpacket.MsgMhfGetEarthStatus{AckHandle: 10})
	a := readAck(t, s)
	if nativeCalendarEvent(a.Payload, 21, TimeAdjusted()) != 37 || nativeCalendarEvent(a.Payload, 11, TimeAdjusted()) != 99 {
		t.Fatal("Tower rollover moved the festival event")
	}
	handleMsgMhfPostTowerInfo(s, &mhfpacket.MsgMhfPostTowerInfo{InfoType: 7, Unk6: 1, TR: 1, TRP: 30, Block1: 2})
	if settlementAckError(t, s) != 0 || tower.settledEarthID != 36 || pallone.settled != 0 {
		t.Fatal("festival changed the admitted Tower result's round")
	}
}

func TestPalloneExplicitSelectionDoesNotPublishDuplicateLegacyCategory(t *testing.T) {
	s, _, _ := palloneCoexistenceSession()
	s.server.erupeConfig.TowerRotation.Enabled = false
	s.server.erupeConfig.EarthStatus = 11
	handleMsgMhfGetEarthStatus(s, &mhfpacket.MsgMhfGetEarthStatus{AckHandle: 11})
	a := readAck(t, s)
	if a.ErrorCode != 0 || len(a.Payload) != 96 || binary.BigEndian.Uint32(a.Payload[8:]) != 0 || nativeCalendarEvent(a.Payload, 11, TimeAdjusted()) != 99 {
		t.Fatal("legacy Earth ID shadowed independent festival selection")
	}
}

func TestPalloneCalendarCachesUpcomingPlayAndClaimWindows(t *testing.T) {
	s, _, pallone := palloneCoexistenceSession()
	now := TimeAdjusted()
	pallone.event.Start = now.Add(10 * time.Minute)
	pallone.event.End = now.Add(70 * time.Minute)
	pallone.event.ClaimUntil = now.Add(90 * time.Minute)
	handleMsgMhfGetEarthStatus(s, &mhfpacket.MsgMhfGetEarthStatus{AckHandle: 12})
	a := readAck(t, s)
	if a.ErrorCode != 0 || len(a.Payload) != 96 || nativeCalendarEvent(a.Payload, 11, now) != 0 {
		t.Fatal("upcoming festival was not cached with its real window")
	}
	// Re-use exactly the same login payload as the client clock advances.
	if nativeCalendarEvent(a.Payload, 11, pallone.event.Start.Add(time.Second)) != 99 ||
		nativeCalendarEvent(a.Payload, 12, pallone.event.End.Add(time.Second)) != 99 ||
		nativeCalendarEvent(a.Payload, 11, pallone.event.End.Add(time.Second)) != 0 ||
		nativeCalendarEvent(a.Payload, 12, pallone.event.ClaimUntil.Add(time.Second)) != 0 {
		t.Fatal("cached festival could not advance play/claim phases independently")
	}
}

func TestPalloneSelectionDoesNotRelabelOtherTowerReplies(t *testing.T) {
	for _, kind := range []string{"Earth values", "Tower history", "weekly Tower rewards"} {
		t.Run(kind, func(t *testing.T) {
			s, _, p := palloneCoexistenceSession()
			// Individual Tower replies must not depend on festival availability.
			p.err = errors.New("festival unavailable")
			call := func() {
				switch kind {
				case "Earth values":
					handleMsgMhfGetEarthValue(s, &mhfpacket.MsgMhfGetEarthValue{AckHandle: 13, ReqType: 3})
				case "Tower history":
					handleMsgMhfGetTowerInfo(s, &mhfpacket.MsgMhfGetTowerInfo{AckHandle: 13, InfoType: 4})
				case "weekly Tower rewards":
					handleMsgMhfGetWeeklySeibatuRankingReward(s, &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{AckHandle: 13, Unk1: 5, Unk2: 260003})
				}
			}
			s.server.erupeConfig.PalloneFestival.Enabled = false
			call()
			before := readAck(t, s)
			s.server.erupeConfig.PalloneFestival.Enabled = true
			call()
			after := readAck(t, s)
			if before.ErrorCode != 0 || after.ErrorCode != 0 || !bytes.Equal(before.Payload, after.Payload) || p.eventID != 0 {
				t.Fatal("independent festival modified or took over a Tower reply")
			}
		})
	}
}
