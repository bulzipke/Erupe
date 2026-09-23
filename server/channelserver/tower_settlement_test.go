package channelserver

import (
	"errors"
	"erupe-ce/network/mhfpacket"
	"fmt"
	"testing"
)

func settlementAckError(t *testing.T, s *Session) byte {
	t.Helper()
	p := <-s.sendPackets
	// QueueSendMHF prepends uint16 opcode, then AckHandle/IsBuffer/ErrorCode.
	if len(p.data) < 8 {
		t.Fatalf("short ACK: %x", p.data)
	}
	return p.data[7]
}

func TestTowerSettlementFailureRetryAndDuplicate(t *testing.T) {
	srv := createMockServer()
	r := &mockTowerRepo{towerData: TowerData{TR: 1, TRP: 30, Block1: 10}, settlementErr: errors.New("injected transaction failure")}
	srv.towerRepo = r
	s := createMockSession(100, srv)
	s.questWeaponGeneration = 1
	p := &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 7, Unk6: 1, TR: 5, TRP: 2400, Cost: 2, Block1: 4}
	handleMsgMhfPostTowerInfo(s, p)
	if settlementAckError(t, s) == 0 || s.towerProgressGeneration != 0 || r.towerData.Block1 != 10 || r.towerData.TRP != 30 {
		t.Fatal("failure credited or acknowledged progress")
	}
	// A later report must not activate the old partial-write fallback.
	s.activeQuestID.Store(towerQuestZone1)
	handleMsgMhfPostTenrouirai(s, &mhfpacket.MsgMhfPostTenrouirai{Op: 1, Floors: 2, TRP: 200})
	if settlementAckError(t, s) != 0 || r.floorBlock != 0 || r.towerData.Block1 != 10 {
		t.Fatal("failed atomic result fell through to report-only credit")
	}
	key := s.towerSettlementRunID
	r.settlementErr = nil
	handleMsgMhfPostTowerInfo(s, p)
	if settlementAckError(t, s) != 0 || key != s.towerSettlementRunID || r.towerData.Block1 != 14 || r.towerData.TRP != 2430 {
		t.Fatal("retry did not commit once")
	}
	// Simulate lost success ACK, followed by an identical request with a new handle.
	p.AckHandle = 2
	handleMsgMhfPostTowerInfo(s, p)
	if settlementAckError(t, s) != 0 || r.settlementWrites != 1 || r.towerData.TRP != 2430 {
		t.Fatal("duplicate added rewards or failed ACK")
	}
	p.TRP++
	handleMsgMhfPostTowerInfo(s, p)
	if settlementAckError(t, s) == 0 || r.settlementWrites != 1 {
		t.Fatal("changed payload accepted as a retry")
	}
}

func TestTowerSettlementAcceptsCourseFloorsAndRejectsInvalidDeltas(t *testing.T) {
	for _, tc := range []struct {
		floor, cost int32
		ok          bool
	}{{6, 2, true}, {7, 2, false}, {2, -1, false}, {0, 0, true}} {
		srv := createMockServer()
		r := &mockTowerRepo{}
		srv.towerRepo = r
		s := createMockSession(100, srv)
		s.questWeaponGeneration = 1
		handleMsgMhfPostTowerInfo(s, &mhfpacket.MsgMhfPostTowerInfo{InfoType: 7, Unk6: 2, TR: 1, TRP: 30, Cost: tc.cost, Block1: tc.floor})
		if got := settlementAckError(t, s) == 0; got != tc.ok {
			t.Fatalf("%+v accepted=%v", tc, got)
		}
	}
}

func TestPlateDataFailuresReturnFailedAck(t *testing.T) {
	for _, which := range []string{"oversize", "load", "decompress", "diff", "write"} {
		t.Run(which, func(t *testing.T) {
			srv := createMockServer()
			r := newMockCharacterRepo()
			srv.charRepo = r
			srv.userBinary = NewUserBinaryStore()
			s := createMockSession(100, srv)
			p := &mhfpacket.MsgMhfSavePlateData{AckHandle: 1, RawDataPayload: []byte{0xff}, IsDataDiff: true}
			switch which {
			case "oversize":
				p.RawDataPayload = make([]byte, plateDataMaxPayload+1)
			case "load":
				r.loadColumnErr = errors.New("injected read failure")
			case "decompress":
				r.columns["platedata"] = make([]byte, plateDataMaxPayload+1)
			case "diff":
				p.RawDataPayload = []byte{1, 2}
			case "write":
				p.IsDataDiff = false
				r.saveErr = errors.New("injected write failure")
			}
			handleMsgMhfSavePlateData(s, p)
			if settlementAckError(t, s) == 0 {
				t.Fatal("unsaved data acknowledged as success")
			}
		})
	}
}

// D469: reproduce the reported 3/4 real floors + 2 course floors, and the
// missing urgent after an uncommitted client-side 4 -> 10 calculation.
func TestTowerSettlementReportedExitAndUrgent(t *testing.T) {
	for _, block := range []uint8{1, 2} {
		for _, gain := range []int32{5, 6} {
			t.Run(fmt.Sprintf("block%d_gain%d", block, gain), func(t *testing.T) {
				srv := createMockServer()
				before := int32(10) - gain
				td := TowerData{TR: 51, TRP: 30}
				urgent, normal := towerQuestMilestone1, towerQuestZone1
				if block == 1 {
					td.Block1 = before
				} else {
					td.Block2 = before
					urgent, normal = towerQuestMilestone2, towerQuestZone2
				}
				r := &mockTowerRepo{towerData: td, settlementErr: errors.New("injected save failure")}
				srv.towerRepo = r
				s := createMockSession(100, srv)
				s.questWeaponGeneration = 1
				packet := &mhfpacket.MsgMhfPostTowerInfo{InfoType: 7, Unk6: int32(block), TR: 51, TRP: 600, Cost: 1, Block1: gain}
				handleMsgMhfPostTowerInfo(s, packet)
				if settlementAckError(t, s) == 0 {
					t.Fatal("failed save got success ACK")
				}
				var cache *TowerData
				if s.allowsTowerQuest(EventQuest{QuestType: 55, QuestID: urgent}, &cache) {
					t.Fatal("unsaved milestone exposed urgent")
				}
				if !s.allowsTowerQuest(EventQuest{QuestType: 55, QuestID: normal}, &cache) {
					t.Fatal("failed save hid normal investigation")
				}
				r.settlementErr = nil
				handleMsgMhfPostTowerInfo(s, packet)
				if settlementAckError(t, s) != 0 {
					t.Fatal("5/6-floor exit rejected")
				}
				cache = nil // New NPC enumeration reads authoritative repository data.
				if !s.allowsTowerQuest(EventQuest{QuestType: 55, QuestID: urgent}, &cache) ||
					s.allowsTowerQuest(EventQuest{QuestType: 55, QuestID: normal}, &cache) {
					t.Fatal("committed floor 10 did not replace normal with urgent")
				}
				reached := r.towerData.Block1
				if block == 2 {
					reached = r.towerData.Block2
				}
				if reached != 10 || r.towerData.TRP != 630 {
					t.Fatal("incorrect saved progress")
				}
				handleMsgMhfPostTowerInfo(s, packet)
				if settlementAckError(t, s) != 0 || r.settlementWrites != 1 || r.towerData.TRP != 630 {
					t.Fatal("retry credited exit twice")
				}
			})
		}
	}
}
