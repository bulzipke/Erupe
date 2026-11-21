package channelserver

import (
	"encoding/binary"
	"testing"
	"time"

	"erupe-ce/common/mhfmon"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestTowerGuardianCuffRewardsAppearAndCanBeClaimedSeparately(t *testing.T) {
	repo := &mockTowerRepo{rewardState: TowerRewardState{
		AdvanceEligible:     true,
		GuardianCuffRewards: []towerGuardianCuffReward{{Block: 1, WeaponType: 4}, {Block: 2, WeaponType: 13}},
		Claimed:             make(map[uint64]bool),
	}}
	server := createMockServer()
	server.towerRepo = repo
	server.erupeConfig.EarthID = 36
	s := createMockSession(56, server)
	handleMsgMhfPresentBox(s, &mhfpacket.MsgMhfPresentBox{AckHandle: 1, Unk1: 1, Unk2: 1, Unk7: []uint32{towerPresentAdvance}})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || len(ack.Payload) != 16+7*44 {
		t.Fatalf("advance list: code=%d, length=%d", ack.ErrorCode, len(ack.Payload))
	}
	items := []uint16{0x2CC7, 0x3FEB, 0x3FEC, 0x3FED, 0x4009, 0x400A, 0x400B}
	quantities := []uint32{1, 1, 1, 1, 6, 6, 6}
	var ids []uint32
	for i, item := range items {
		row := ack.Payload[16+i*44:]
		if binary.BigEndian.Uint32(row[4:]) != towerPresentAdvance || binary.BigEndian.Uint32(row[36:]) != uint32(item) ||
			binary.BigEndian.Uint32(row[40:]) != quantities[i] {
			t.Fatalf("unexpected advance row %d: %x", i, row[:44])
		}
		ids = append(ids, binary.BigEndian.Uint32(row))
	}
	handleMsgMhfPresentBox(s, &mhfpacket.MsgMhfPresentBox{AckHandle: 2, Unk1: 2, Unk2: 3, Unk7: []uint32{ids[1], ids[4], ids[1]}})
	if code := readAck(t, s).ErrorCode; code != 0 || len(repo.claimedRewards) != 2 {
		t.Fatalf("receipt code %d, claims %v", code, repo.claimedRewards)
	}
	for _, key := range repo.claimedRewards {
		repo.rewardState.Claimed[key] = true
	}
	remaining := towerPendingRewards(repo.rewardState, []uint32{towerPresentAdvance})
	if len(remaining) != 5 || remaining[0].itemID != items[0] || remaining[3].itemID != items[5] {
		t.Fatalf("unexpected remaining rewards: %+v", remaining)
	}
	roundRows, err := towerRoundRewardIDs(TowerEvent{ID: 36}, remaining)
	if err != nil || roundRows[0].claimIndex != 36<<16|10000 || roundRows[3].claimIndex != 36<<16|10005 {
		t.Fatalf("unexpected round claim IDs: %+v, %v", roundRows, err)
	}
}

type towerGuardianLogRepo struct {
	mockTowerRepo
	weapons map[uint8]*uint8
}

func (r *towerGuardianLogRepo) RecordGuardianKill(_ int32, block uint8, _ string, _ uint32, weaponType *uint8) error {
	r.weapons[block] = weaponType
	return nil
}

func TestTowerGuardianCuffRewardsRequireMatchingClearedBoss(t *testing.T) {
	for _, tc := range []struct {
		name     string
		quest    uint16
		monster  int
		cleared  bool
		tower    bool
		wantZone uint8
	}{
		{"zone one guardian", towerQuestMilestone1, mhfmon.Block1Duremudira, true, true, 1},
		{"zone two guardian", towerQuestMilestone2, mhfmon.Block2Duremudira, true, true, 2},
		{"bypassed", towerQuestMilestone1, -1, true, true, 0},
		{"failed", towerQuestMilestone2, mhfmon.Block2Duremudira, false, true, 0},
		{"wrong guardian", towerQuestMilestone1, mhfmon.Block2Duremudira, true, true, 0},
		{"same quest ID in Road", towerQuestMilestone2, mhfmon.Block2Duremudira, true, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.RealClientMode = cfg.ZZ
			server.erupeConfig.EarthStatus = 21
			server.guildRepo = &mockGuildRepo{}
			repo := &towerGuardianLogRepo{weapons: make(map[uint8]*uint8)}
			server.towerRepo = repo
			s := createMockSession(1, server)
			s.stage = NewStage("sl1Ns200p0a0u1")
			s.questWeaponGeneration = 1
			if tc.tower {
				s.pinTowerDeparture(TowerEvent{}, TimeAdjusted())
			}
			s.towerGuardianRunID, s.towerGuardianQuestID = "guardian-run", tc.quest
			s.questWeaponState.Store(uint32(tc.quest)<<8 | 14)
			data := make([]byte, killLogHeaderSize+killLogMonsterCount)
			binary.LittleEndian.PutUint16(data[questIDOffset:], tc.quest)
			if tc.monster >= 0 {
				data[killLogHeaderSize+tc.monster] = 1
			}
			if tc.cleared {
				data[questResultCodeOffset] = questResultCodeCleared
			}
			handleMsgSysRecordLog(s, &mhfpacket.MsgSysRecordLog{AckHandle: 1, Data: data})
			var rewarded uint8
			for zone, weapon := range repo.weapons {
				if weapon != nil {
					if *weapon != 13 || rewarded != 0 {
						t.Fatalf("unexpected reward weapons: %+v", repo.weapons)
					}
					rewarded = zone
				}
			}
			if rewarded != tc.wantZone {
				t.Fatalf("rewarded zone %d, want %d", rewarded, tc.wantZone)
			}
		})
	}
}

func TestTowerGuardianCuffRewardsPostgresFirstKillPerRoundAndZone(t *testing.T) {
	db := towerD463DB(t)
	repo := NewTowerRepository(db)
	if _, err := db.Exec(`INSERT INTO tower_guardian_kills (earth_id,block,run_id,character_id,before_casual)
		SELECT 36,2,'prior-'||n,102,true FROM generate_series(1,4000) AS n`); err != nil {
		t.Fatal(err)
	}
	sword, hammer, magnet := uint8(4), uint8(2), uint8(13)
	for _, kill := range []struct {
		round  int32
		zone   uint8
		run    string
		charID uint32
		weapon *uint8
	}{
		{36, 1, "one", 100, &sword},
		{36, 1, "one", 100, &hammer},
		{36, 1, "one", 101, &magnet},
		{36, 1, "another", 100, &hammer},
		{36, 2, "two", 100, &magnet},
		{36, 2, "two", 101, &sword},
		{36, 2, "two", 102, &hammer},
		{37, 1, "next", 100, &hammer},
		{37, 2, "unknown-weapon", 102, nil},
	} {
		if err := repo.RecordGuardianKill(kill.round, kill.zone, kill.run, kill.charID, kill.weapon); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		round      int32
		charID     uint32
		want       []towerGuardianCuffReward
		wantLegacy bool
	}{
		{36, 100, []towerGuardianCuffReward{{1, sword}, {2, magnet}}, true},
		{36, 101, []towerGuardianCuffReward{{1, magnet}, {2, sword}}, true},
		{36, 102, []towerGuardianCuffReward{{2, hammer}}, false},
		{37, 100, []towerGuardianCuffReward{{1, hammer}}, true},
		{37, 102, nil, true},
	} {
		state, err := repo.GetTowerRewardState(tc.round, tc.charID, time.Time{})
		if err != nil || len(state.GuardianCuffRewards) != len(tc.want) || state.AdvanceEligible != tc.wantLegacy {
			t.Fatalf("round %d hunter %d state %+v, err=%v", tc.round, tc.charID, state, err)
		}
		for i, want := range tc.want {
			if got := state.GuardianCuffRewards[i]; got != want {
				t.Errorf("round %d hunter %d: reward %+v, want %+v", tc.round, tc.charID, got, want)
			}
		}
	}
}
