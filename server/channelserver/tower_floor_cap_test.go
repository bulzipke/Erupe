package channelserver

import (
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestTowerFloorReportCap(t *testing.T) {
	for _, block := range []int32{1, 2} {
		for _, floor := range []int32{-1, 0, 1, 1000, 1001, 1040, 9960, 9999, 10000, 20000} {
			srv := createMockServer()
			repo := &mockTowerRepo{}
			srv.towerRepo = repo
			s := createMockSession(100, srv)
			handleMsgMhfPostTowerInfo(s, &mhfpacket.MsgMhfPostTowerInfo{InfoType: 6, Unk1: 1, Unk6: block, Block1: floor})
			<-s.sendPackets
			want := floor > 0 && floor <= 9999
			if (repo.floorBlock != 0) != want || (want && (repo.floorBlock != uint8(block) || repo.floorValue != floor)) {
				t.Fatalf("block=%d floor=%d: recorded %d/%d, want accepted=%v", block, floor, repo.floorBlock, repo.floorValue, want)
			}
		}
	}
}

type floorCapRepo struct {
	mockTowerRepo
	runFloors uint16
	runBlock  uint8
	runs      int
}

func (r *floorCapRepo) RecordTowerRun(_ int32, _ uint32, block uint8, _ time.Time, stats TowerMissionStats) error {
	r.runFloors += stats.Floors
	r.runBlock = block
	r.runs++
	return nil
}

func TestTowerCumulativeFloorCap(t *testing.T) {
	for _, block := range []uint8{1, 2} {
		for _, it7 := range []bool{false, true} {
			for _, tc := range []struct{ reached, gain, want int32 }{
				{0, 4, 4}, {1000, 4, 4}, {9996, 3, 3}, {9998, 4, 1}, {9999, 4, 0},
			} {
				srv := createMockServer()
				repo := &floorCapRepo{mockTowerRepo: mockTowerRepo{towerData: TowerData{Block1: tc.reached, Block2: tc.reached}}}
				srv.towerRepo = repo
				s := createMockSession(100, srv)
				s.questWeaponGeneration = 1
				qid := towerQuestZone1
				if block == 2 {
					qid = towerQuestZone2
				}
				s.activeQuestID.Store(uint32(qid))
				if it7 {
					pkt := &mhfpacket.MsgMhfPostTowerInfo{InfoType: 7, TR: 1, TRP: 30, Unk6: int32(block), Block1: tc.gain}
					handleMsgMhfPostTowerInfo(s, pkt)
					<-s.sendPackets
					// A duplicate result must not credit even the clipped gain again.
					handleMsgMhfPostTowerInfo(s, pkt)
					<-s.sendPackets
				} else {
					towerRecordReportedFloors(s, TowerMissionStats{Floors: uint16(tc.gain), TRP: 30}, time.Now())
				}
				if tc.want == 0 {
					if repo.floorBlock != 0 || repo.runs != 0 {
						t.Fatal("capped record must not add floors or history")
					}
				} else if repo.floorBlock != block || repo.floorValue != tc.reached+tc.want ||
					repo.runFloors != uint16(tc.want) || repo.runBlock != block || repo.runs != 1 {
					t.Fatalf("block=%d IT7=%v reached=%d gain=%d: floor=%d history=%d, want %d/%d", block, it7, tc.reached, tc.gain, repo.floorValue, repo.runFloors, tc.reached+tc.want, tc.want)
				}
			}
		}
	}
}

func TestTowerMilestoneAbove1000(t *testing.T) {
	for _, block := range []uint8{1, 2} {
		for _, floor := range []int32{1040, 9960} {
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = cfg.ZZ
			srv.erupeConfig.EarthStatus = 21
			repo := &mockTowerRepo{towerData: TowerData{Block1: floor, Block2: floor}}
			srv.towerRepo = repo
			s := createMockSession(100, srv)
			stage := NewStage("sl1Qs100p0a0u0")
			stage.host = s
			stage.reservedClientSlots[s.charID] = true
			s.reservationStage = stage
			qid := uint16(towerQuestGuardian1)
			if block == 2 {
				qid = towerQuestGuardian2
			}
			stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(qid, 0)
			pkt := &mhfpacket.MsgMhfPostTowerInfo{InfoType: 6, Unk1: 1, Unk6: int32(block), Block1: floor}
			if handled, err := s.postTowerMilestone(pkt); !handled || err != nil || repo.passedFloor != floor || repo.passedBlock != block {
				t.Fatalf("block=%d floor=%d: handled=%v err=%v receipt=%d/%d", block, floor, handled, err, repo.passedBlock, repo.passedFloor)
			}
		}
	}
	for _, floor := range []int32{9999, 10000, 10040} {
		if towerGuardianFloor(floor) {
			t.Fatalf("%d is not an in-range Guardian milestone", floor)
		}
	}
}

func TestRepoTowerFloorRangeRejectsBeforeWrite(t *testing.T) {
	repo := &TowerRepository{} // No DB: invalid input must be rejected before a write.
	for _, block := range []uint8{1, 2} {
		for _, floor := range []int32{-1, 10000, 20000} {
			if err := repo.UpdateBlockFloors(100, block, floor); err == nil {
				t.Fatalf("block=%d accepted invalid floor %d", block, floor)
			}
		}
	}
}

func (r *floorCapRepo) SettleTowerRun(charID uint32, key string, earth int32, day time.Time, v TowerSettlement) (int32, error) {
	before := r.settlementWrites
	gain, err := r.mockTowerRepo.SettleTowerRun(charID, key, earth, day, v)
	if err == nil && gain > 0 && r.settlementWrites > before {
		_ = r.RecordTowerRun(earth, charID, v.Block, day, TowerMissionStats{Floors: uint16(gain), TRP: uint16(v.TRP)})
	}
	return gain, err
}
