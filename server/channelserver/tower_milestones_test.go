package channelserver

import (
	"encoding/binary"
	"errors"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestTowerMilestoneDeparture(t *testing.T) {
	for _, tc := range []struct {
		name     string
		qid      uint16
		block    int32
		floor    int32
		host     bool
		reserved bool
		want     bool
	}{
		{"bypass-before-entry", towerQuestGuardian1, 1, 10, true, true, true},
		{"failure-after-entry", towerQuestGuardian1, 1, 10, true, false, true},
		{"block-two", towerQuestGuardian2, 2, 40, true, true, true},
		{"guest", towerQuestGuardian1, 1, 10, false, true, false},
		{"wrong-block", towerQuestGuardian1, 2, 40, true, true, false},
		{"unreached-floor", towerQuestGuardian1, 1, 80, true, true, false},
		{"non-milestone", towerQuestGuardian1, 1, 11, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = cfg.ZZ
			srv.erupeConfig.EarthStatus = 21
			repo := &mockTowerRepo{towerData: TowerData{Block1: 10, Block2: 40}}
			srv.towerRepo = repo
			s := createMockSession(100, srv)
			guest := createMockSession(101, srv)
			stage := NewStage("sl1Qs100p0a0u0")
			stage.host = s
			if !tc.host {
				stage.host = guest
			}
			stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(tc.qid, 0)
			if tc.reserved {
				stage.reservedClientSlots[s.charID] = true
				s.reservationStage = stage
			} else {
				stage.clients[s] = s.charID
				s.stage = stage
			}
			// No kill receipt and no quest-clear result exists at departure.
			pkt := &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 6, Unk1: 1, Unk6: tc.block, Block1: tc.floor}
			handleMsgMhfPostTowerInfo(s, pkt)
			response := (<-s.sendPackets).data
			if len(response) < 8 {
				t.Fatal("missing acknowledgement")
			}
			if (response[7] == 0) != tc.want {
				t.Fatalf("ack error=%d want success=%v", response[7], tc.want)
			}
			if tc.want {
				if repo.passedChar != s.charID || repo.passedBlock != uint8(tc.block) || repo.passedFloor != tc.floor {
					t.Fatalf("milestone receipt: %d/%d/%d", repo.passedChar, repo.passedBlock, repo.passedFloor)
				}
				// A retry after a lost acknowledgement remains successful.
				if handled, err := s.postTowerMilestone(pkt); !handled || err != nil {
					t.Fatalf("retry: handled=%v err=%v", handled, err)
				}
				var cache *TowerData
				if s.allowsTowerQuest(EventQuest{QuestID: int(tc.qid)}, &cache) {
					t.Fatal("passed milestone is still offered")
				}
			} else if repo.passedChar != 0 {
				t.Fatal("invalid milestone credited")
			}
			if repo.floorBlock != 0 || s.towerMissionSubmissionReady.Load() {
				t.Fatal("departure IT6 changed floor/daily-mission state")
			}
		})
	}
}

func TestTowerMilestoneDepartureFailureAndOtherQuests(t *testing.T) {
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.erupeConfig.EarthStatus = 21
	repo := &mockTowerRepo{towerData: TowerData{Block1: 10}, passedErr: errors.New("db failed")}
	srv.towerRepo = repo
	s := createMockSession(100, srv)
	stage := NewStage("sl1Qs100p0a0u0")
	stage.host = s
	stage.reservedClientSlots[s.charID] = true
	s.reservationStage = stage
	pkt := &mhfpacket.MsgMhfPostTowerInfo{InfoType: 6, Unk1: 1, Unk6: 1, Block1: 10}
	stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(towerQuestGuardian1, 0)
	if handled, err := s.postTowerMilestone(pkt); !handled || err == nil {
		t.Fatalf("database error not surfaced: %v %v", handled, err)
	}
	for _, qid := range []uint16{towerQuestZone1, 21749, 21750} {
		stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(qid, 0)
		if handled, err := s.postTowerMilestone(pkt); handled || err != nil {
			t.Fatalf("ordinary/rare tower or Road quest %d treated as a milestone", qid)
		}
	}
	if repo.passedChar != 0 || s.towerMissionSubmissionReady.Load() {
		t.Fatal("unrelated or failed request credited")
	}
}

func TestGetTowerInfoPassedMilestones(t *testing.T) {
	for _, infoType := range []uint32{3, 5} {
		srv := createMockServer()
		srv.erupeConfig.RealClientMode = cfg.ZZ
		srv.towerRepo = &mockTowerRepo{towerData: TowerData{Block1: 80, Block2: 500, Guardian1: 40, Guardian2: 500}}
		s := createMockSession(100, srv)
		handleMsgMhfGetTowerInfo(s, &mhfpacket.MsgMhfGetTowerInfo{AckHandle: 1, InfoType: infoType})
		_, code, payload := parseAckBufData(t, (<-s.sendPackets).data)
		if code != 0 || len(payload) != 48 {
			t.Fatalf("IT%d code=%d len=%d", infoType, code, len(payload))
		}
		if binary.BigEndian.Uint32(payload[16:]) != 80 || binary.BigEndian.Uint32(payload[32:]) != 500 ||
			binary.BigEndian.Uint32(payload[24:]) != 40 || binary.BigEndian.Uint32(payload[40:]) != 500 {
			t.Fatalf("IT%d floor/passed fields: %x", infoType, payload)
		}
	}
}

func TestRepoTowerPassMilestone(t *testing.T) {
	repo, db, charID, _ := setupTowerRepo(t)
	if _, err := db.Exec(`INSERT INTO tower (char_id, block1, block2) VALUES ($1, 10, 40)`, charID); err != nil {
		t.Fatal(err)
	}
	for _, block := range []uint8{1, 1, 2} {
		floor := int32(10)
		if block == 2 {
			floor = 40
		}
		if err := repo.PassTowerMilestone(charID, block, floor); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.PassTowerMilestone(charID, 1, 80); err == nil {
		t.Fatal("unreached milestone accepted")
	}
	td, err := repo.GetTowerData(charID)
	if err != nil || td.Guardian1 != 10 || td.Guardian2 != 40 || td.Block1 != 10 || td.Block2 != 40 {
		t.Fatalf("milestone readback: %+v %v", td, err)
	}
}

func TestTowerMilestoneReplacesNormalInvestigation(t *testing.T) {
	for _, floor := range []int32{10, 40, 80, 500, 1000, 1040, 9960} {
		for _, block := range []int{1, 2} {
			for _, passed := range []bool{false, true} {
				srv := createMockServer()
				// Zero unlock thresholds do not disable the urgent replacement.
				srv.erupeConfig.TowerZone1UnlockFloor = 0
				srv.erupeConfig.TowerZone2UnlockFloor = 0
				srv.erupeConfig.TowerZone2UnlockTR = 0
				td := TowerData{Block1: 1, Block2: 1}
				normal, urgent, other := towerQuestZone1, towerQuestGuardian1, towerQuestZone2
				if block == 1 {
					td.Block1 = floor
					if passed {
						td.Guardian1 = floor
					}
				} else {
					td.Block2 = floor
					if passed {
						td.Guardian2 = floor
					}
					normal, urgent, other = towerQuestZone2, towerQuestGuardian2, towerQuestZone1
				}
				srv.towerRepo = &mockTowerRepo{towerData: td}
				s := createMockSession(100, srv)
				var cache *TowerData
				if s.allowsTowerQuest(EventQuest{QuestID: normal}, &cache) != passed ||
					s.allowsTowerQuest(EventQuest{QuestID: urgent}, &cache) == passed {
					t.Fatalf("block=%d floor=%d passed=%v: normal/urgent are not mutually exclusive", block, floor, passed)
				}
				if !s.allowsTowerQuest(EventQuest{QuestID: other}, &cache) ||
					!s.allowsTowerQuest(EventQuest{QuestID: towerQuestPrologue}, &cache) {
					t.Fatal("pending milestone hid another block or the prologue")
				}
			}
		}
	}
}
