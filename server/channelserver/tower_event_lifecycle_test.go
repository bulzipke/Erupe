package channelserver

import (
	"errors"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"testing"
	"time"
)

func TestTowerEventExactBoundaries(t *testing.T) {
	start := time.Date(2026, 10, 7, 12, 0, 0, 0, divaLocation)
	e := TowerEvent{ID: 36, Start: start, End: start.Add(7 * 24 * time.Hour), ClaimUntil: start.Add(21 * 24 * time.Hour)}
	for _, tc := range []struct {
		now           time.Time
		active, claim bool
	}{
		{start.Add(-time.Nanosecond), false, false}, {start, true, true},
		{e.End.Add(-time.Nanosecond), true, true}, {e.End, false, true},
		{e.ClaimUntil.Add(-time.Nanosecond), false, true}, {e.ClaimUntil, false, false},
	} {
		if e.Active(tc.now) != tc.active || e.Claimable(tc.now) != tc.claim {
			t.Fatalf("boundary %v", tc)
		}
	}
}
func TestTowerRotationAnchorAndValidation(t *testing.T) {
	o := cfg.TowerRotationOptions{ActiveDays: 7, CycleDays: 21}
	now := time.Date(2026, 10, 4, 2, 0, 0, 0, time.UTC) // 11:00 JST
	start, err := towerInitialStart(now, o)
	want := time.Date(2026, 10, 3, 3, 0, 0, 0, time.UTC)
	if err != nil || !start.Equal(want) {
		t.Fatalf("noon anchor %v %v", start, err)
	}
	o.StartAt = "2026-08-26T12:00:00+09:00"
	start, err = towerInitialStart(now, o)
	if err != nil || start.After(now) || now.Sub(start) >= 21*24*time.Hour {
		t.Fatalf("historical anchor %v %v", start, err)
	}
	o.StartAt = "2026-10-07T12:00:00+09:00"
	start, err = towerInitialStart(now, o)
	if err != nil || !start.After(now) {
		t.Fatalf("future anchor %v %v", start, err)
	}
	for _, tc := range []cfg.TowerRotationOptions{
		{ActiveDays: 0, CycleDays: 21}, {ActiveDays: 7, CycleDays: 7}, {ActiveDays: 7, CycleDays: 366},
		{ActiveDays: 7, CycleDays: 21, StartAt: "not a date"},
	} {
		if _, err := towerInitialStart(now, tc); err == nil {
			t.Fatalf("invalid config accepted: %+v", tc)
		}
	}
}
func TestTowerRotationRewardIDsNeverCrossRounds(t *testing.T) {
	state := TowerRewardState{Floors: 20, AdvanceEligible: true, Daily: []TowerDailyDay{{
		Start: towerDailyStart(TimeAdjusted()), Counters: TowerDailyCounters{Floors: 4, TRP: 1500, Antiques: 3, Chests: 3, Slays: 3},
	}}}
	types := []uint32{towerPresentFloor, towerPresentAdvance, towerPresentDaily}
	old, err := towerRoundRewardIDs(TowerEvent{ID: 36}, towerPendingRewards(state, types))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := towerRoundRewardIDs(TowerEvent{ID: 37}, towerPendingRewards(state, types))
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]uint32, len(old))
	for i := range old {
		ids[i] = old[i].claimIndex
	}
	claims, unknown := towerClaimRequestRewards(ids, fresh)
	if len(claims) != 0 || len(unknown) != len(old) {
		t.Fatal("old receipt matched fresh round")
	}
	for i := range fresh {
		if old[i].itemID != fresh[i].itemID || old[i].quantity != fresh[i].quantity {
			t.Fatal("namespace changed reward table")
		}
	}
	_, err = towerRoundRewardIDs(TowerEvent{ID: 36}, []towerPresentReward{
		{kind: towerRewardDaily, index: 1}, {kind: towerRewardDaily, index: 1 + 32768},
	})
	if err == nil {
		t.Fatal("legacy daily collision silently accepted")
	}
}

type mockTowerRotationRepo struct {
	*mockTowerRepo
	event          TowerEvent
	err            error
	settledEarthID int32
}

func (r *mockTowerRotationRepo) EnsureTowerEvent(time.Time, cfg.TowerRotationOptions, int32) (TowerEvent, error) {
	return r.event, r.err
}
func (r *mockTowerRotationRepo) ForTowerRound(int32) TowerRepo { return r }
func (r *mockTowerRotationRepo) SettleTowerRun(cid uint32, run string, id int32, day time.Time, v TowerSettlement) (int32, error) {
	r.settledEarthID = id
	return r.mockTowerRepo.SettleTowerRun(cid, run, id, day, v)
}
func towerRotationSession() (*Session, *mockTowerRotationRepo) {
	srv := createMockServer()
	srv.erupeConfig.TowerRotation = cfg.TowerRotationOptions{Enabled: true, ActiveDays: 7, CycleDays: 21}
	srv.erupeConfig.RealClientMode = cfg.ZZ
	now := TimeAdjusted()
	r := &mockTowerRotationRepo{mockTowerRepo: &mockTowerRepo{}, event: TowerEvent{ID: 36, Start: now.Add(-time.Hour), End: now.Add(time.Hour), ClaimUntil: now.Add(14 * 24 * time.Hour)}}
	srv.towerRepo = r
	return createMockSession(100, srv), r
}
func TestTowerRotationResultUsesDepartureRound(t *testing.T) {
	s, r := towerRotationSession()
	s.questWeaponGeneration = 1
	s.pinTowerDeparture(r.event, TimeAdjusted())
	r.event = TowerEvent{ID: 37, Start: TimeAdjusted(), End: TimeAdjusted().Add(time.Hour), ClaimUntil: TimeAdjusted().Add(21 * 24 * time.Hour)}
	handleMsgMhfPostTowerInfo(s, &mhfpacket.MsgMhfPostTowerInfo{InfoType: 7, Unk6: 1, TR: 1, TRP: 30, Block1: 2})
	if settlementAckError(t, s) != 0 || r.settledEarthID != 36 {
		t.Fatalf("settled against %d", r.settledEarthID)
	}
}
func TestTowerRotationNoDepartureCannotPostResult(t *testing.T) {
	s, r := towerRotationSession()
	s.questWeaponGeneration = 1
	handleMsgMhfPostTowerInfo(s, &mhfpacket.MsgMhfPostTowerInfo{InfoType: 7, Unk6: 1, TR: 1, TRP: 30, Block1: 2})
	if settlementAckError(t, s) == 0 || r.settlementWrites != 0 {
		t.Fatal("unadmitted result credited")
	}
	handleMsgMhfPostTenrouirai(s, &mhfpacket.MsgMhfPostTenrouirai{Op: 1, Floors: 2})
	if settlementAckError(t, s) != 0 {
		t.Fatal("ignored investigation must succeed to avoid black screen")
	}
}
func TestTowerRotationInactiveListAndSharedRoadSetup(t *testing.T) {
	s, r := towerRotationSession()
	r.event.End = TimeAdjusted().Add(-time.Second)
	var cache *TowerData
	if s.allowsTowerQuest(EventQuest{QuestType: 55, QuestID: towerQuestPrologue}, &cache) {
		t.Fatal("inactive Tower listed")
	}
	if !s.allowsTowerQuest(EventQuest{QuestID: 12345}, &cache) {
		t.Fatal("other categories hidden")
	}
	setup := questRunStagePayload(towerQuestMilestone1, 0)
	if !towerSetupIsTower(towerQuestMilestone1, setup) {
		t.Fatal("Tower setup not detected")
	}
	setup[questRunStageQuestIDOffset+0x27] |= 2
	if towerSetupIsTower(towerQuestMilestone1, setup) {
		t.Fatal("Road mistaken for Tower")
	}
	r.err = errors.New("DB failure")
	if s.allowsTowerQuest(EventQuest{QuestType: 55, QuestID: towerQuestPrologue}, &cache) {
		t.Fatal("failed lifecycle exposed departure")
	}
}
func TestTowerRotationOldClaimCannotConsumeNewReward(t *testing.T) {
	s, r := towerRotationSession()
	r.rewardState = TowerRewardState{Floors: 1}
	old, _ := towerRoundRewardIDs(TowerEvent{ID: 35}, towerPendingRewards(r.rewardState, []uint32{towerPresentFloor}))
	handleTowerPresentBox(s, &mhfpacket.MsgMhfPresentBox{Unk1: 2, Unk7: []uint32{old[0].claimIndex}})
	if settlementAckError(t, s) != 0 || len(r.claimedRewards) != 0 {
		t.Fatal("old claim consumed new reward")
	}
}

func TestTowerRotationDepartureGate(t *testing.T) {
	for _, tc := range []struct {
		name               string
		active, road, want bool
	}{
		{"active Tower", true, false, true}, {"closed Tower", false, false, false}, {"Road while Tower closed", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r := towerRotationSession()
			if !tc.active {
				r.event.End = TimeAdjusted().Add(-time.Second)
			}
			stage := NewStage("sl2Qs200p0a1u0")
			stage.host = s
			setup := questRunStagePayload(towerQuestMilestone1, 0)
			if tc.road {
				setup[questRunStageQuestIDOffset+0x27] |= 2
			}
			stage.rawBinaryData[stageBinaryKey{1, 3}] = setup
			s.server.stages.Store(stage.id, stage)
			if got := doStageTransfer(s, 1, stage.id); got != tc.want {
				t.Fatalf("departure %v want %v", got, tc.want)
			}
			_, member := stage.clients[s]
			if member != tc.want {
				t.Fatal("rejected departure changed membership")
			}
			_, pinned := s.towerResultEvent()
			if pinned != (tc.active && !tc.road) {
				t.Fatalf("Tower admission %v", pinned)
			}
		})
	}
}
func TestTowerRotationLateSetupUsesEntryTime(t *testing.T) {
	s, r := towerRotationSession()
	stage := NewStage("sl2Qs200p0a1u0")
	stage.host = s
	s.server.stages.Store(stage.id, stage)
	if !doStageTransfer(s, 1, stage.id) {
		t.Fatal("entry failed")
	}
	// The host setup may arrive after the event closed; entry was already valid.
	r.event.End = TimeAdjusted().Add(-time.Second)
	handleMsgSysSetStageBinary(s, &mhfpacket.MsgSysSetStageBinary{StageID: stage.id, BinaryType0: 1, BinaryType1: 3, RawDataPayload: questRunStagePayload(towerQuestZone1, 0)})
	e, pinned := s.towerResultEvent()
	if !pinned || e.ID != 36 {
		t.Fatalf("late setup lost entry admission %+v %v", e, pinned)
	}
}
func TestTowerRotationRewardWindowAfterClose(t *testing.T) {
	s, r := towerRotationSession()
	r.rewardState = TowerRewardState{Floors: 1}
	r.event.End = TimeAdjusted().Add(-time.Second)
	handleTowerPresentBox(s, &mhfpacket.MsgMhfPresentBox{Unk1: 3, Unk7: []uint32{towerPresentFloor}})
	_, count := parseAckSimpleValue(t, (<-s.sendPackets).data)
	if count != 1 {
		t.Fatal("closed event lost unclaimed reward")
	}
	r.event.Start = TimeAdjusted().Add(time.Hour)
	handleTowerPresentBox(s, &mhfpacket.MsgMhfPresentBox{Unk1: 3, Unk7: []uint32{towerPresentFloor}})
	_, count = parseAckSimpleValue(t, (<-s.sendPackets).data)
	if count != 0 {
		t.Fatal("future event allowed claim")
	}
}

func TestTowerRotationNativeFloorReportDoesNotDoubleCount(t *testing.T) {
	s, r := towerRotationSession()
	s.server.guildRepo = &mockGuildRepo{}
	s.server.erupeConfig.TowerRankTRPPerRank = 600
	s.questWeaponGeneration = 1
	s.pinTowerDeparture(r.event, TimeAdjusted())
	s.activeQuestID.Store(towerQuestZone1)
	handleMsgMhfPostTowerInfo(s, &mhfpacket.MsgMhfPostTowerInfo{InfoType: 6, Unk1: 1, Unk6: 1, Block1: 2})
	if settlementAckError(t, s) != 0 || r.floorValue != 0 {
		t.Fatal("absolute report saved before atomic run")
	}
	report := &mhfpacket.MsgMhfPostTenrouirai{Op: 1, Floors: 2, TRP: 200}
	handleMsgMhfPostTenrouirai(s, report)
	if settlementAckError(t, s) != 0 || r.towerData.Block1 != 2 || r.addedTRP != 200 || r.settlementWrites != 1 {
		t.Fatal("report-only run was not recorded once")
	}
	handleMsgMhfPostTenrouirai(s, report)
	if settlementAckError(t, s) != 0 || r.towerData.Block1 != 2 || r.addedTRP != 200 || r.settlementWrites != 1 {
		t.Fatal("duplicate investigation credited again")
	}
}
