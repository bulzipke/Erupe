package channelserver

import (
	"encoding/binary"
	"strings"
	"testing"

	"erupe-ce/common/stringsupport"
	"erupe-ce/network/mhfpacket"
)

func TestHandleMsgMhfGetTenrouirai_Type1(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetTenrouirai{
		AckHandle: 12345,
		Unk0:      1,
	}

	handleMsgMhfGetTenrouirai(session, pkt)

	// Verify response packet was queued
	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetTenrouirai_Default(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetTenrouirai{
		AckHandle: 12345,
		Unk0:      0,
		DataType:  0,
	}

	handleMsgMhfGetTenrouirai(session, pkt)

	// Verify response packet was queued
	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfPostTowerInfo(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPostTowerInfo{
		AckHandle: 12345,
	}

	handleMsgMhfPostTowerInfo(session, pkt)

	// Verify response packet was queued
	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfPostTenrouirai(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPostTenrouirai{
		AckHandle: 12345,
	}

	handleMsgMhfPostTenrouirai(session, pkt)

	// Verify response packet was queued
	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetBreakSeibatuLevelReward(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetBreakSeibatuLevelReward{
		AckHandle: 12345,
	}

	handleMsgMhfGetBreakSeibatuLevelReward(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetWeeklySeibatuRankingReward(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{
		AckHandle: 12345,
	}

	handleMsgMhfGetWeeklySeibatuRankingReward(session, pkt)

	// Verify response packet was queued
	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfPresentBox(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPresentBox{
		AckHandle: 12345,
	}

	handleMsgMhfPresentBox(session, pkt)

	// Verify response packet was queued
	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetTenrouirai_Type2_Rewards(t *testing.T) {
	srv := createMockServer()
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfGetTenrouirai{AckHandle: 1, DataType: 2}
	handleMsgMhfGetTenrouirai(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfGetTenrouirai_Type4_Progress(t *testing.T) {
	srv := createMockServer()
	srv.towerRepo = &mockTowerRepo{}
	ensureTowerService(srv)
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfGetTenrouirai{AckHandle: 1, DataType: 4, GuildID: 1}
	handleMsgMhfGetTenrouirai(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfGetTenrouirai_Type5_Scores(t *testing.T) {
	srv := createMockServer()
	srv.towerRepo = &mockTowerRepo{}
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfGetTenrouirai{AckHandle: 1, DataType: 5, GuildID: 1, MissionIndex: 0}
	handleMsgMhfGetTenrouirai(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfGetTenrouirai_Type6_RP(t *testing.T) {
	srv := createMockServer()
	srv.towerRepo = &mockTowerRepo{}
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfGetTenrouirai{AckHandle: 1, DataType: 6, GuildID: 1}
	handleMsgMhfGetTenrouirai(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfPostTowerInfo_SkillUpdate(t *testing.T) {
	srv := createMockServer()
	srv.towerRepo = &mockTowerRepo{}
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 2, Skill: 3, Cost: -10}
	handleMsgMhfPostTowerInfo(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfPostTowerInfo_ProgressUpdate(t *testing.T) {
	srv := createMockServer()
	srv.towerRepo = &mockTowerRepo{}
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 1, TR: 5, TRP: 100, Cost: -20, Block1: 1}
	handleMsgMhfPostTowerInfo(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfPostTowerInfo_ProgressType7(t *testing.T) {
	srv := createMockServer()
	srv.towerRepo = &mockTowerRepo{}
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 7, TR: 10, TRP: 200}
	handleMsgMhfPostTowerInfo(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfPostTowerInfo_QuestToolsDebug(t *testing.T) {
	srv := createMockServer()
	srv.towerRepo = &mockTowerRepo{}
	srv.erupeConfig.DebugOptions.QuestTools = true
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 2, Skill: 1}
	handleMsgMhfPostTowerInfo(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfPostTenrouirai_Op1(t *testing.T) {
	srv := createMockServer()
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfPostTenrouirai{AckHandle: 1, Op: 1}
	handleMsgMhfPostTenrouirai(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfPostTenrouirai_QuestToolsDebug(t *testing.T) {
	srv := createMockServer()
	srv.erupeConfig.DebugOptions.QuestTools = true
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfPostTenrouirai{AckHandle: 1, Op: 1, Floors: 10, Slays: 5}
	handleMsgMhfPostTenrouirai(s, pkt)
	<-s.sendPackets
}

// --- EmptyTowerCSV tests ---

func TestEmptyTowerCSV(t *testing.T) {
	result := EmptyTowerCSV(3)
	if result != "0,0,0" {
		t.Errorf("EmptyTowerCSV(3) = %q, want %q", result, "0,0,0")
	}

	result = EmptyTowerCSV(1)
	if result != "0" {
		t.Errorf("EmptyTowerCSV(1) = %q, want %q", result, "0")
	}

	result = EmptyTowerCSV(5)
	parts := strings.Split(result, ",")
	if len(parts) != 5 {
		t.Errorf("EmptyTowerCSV(5) has %d parts, want 5", len(parts))
	}
}

// --- handleMsgMhfGetTowerInfo tests ---

func TestGetTowerInfo_InfoType1_TRP(t *testing.T) {
	server := createMockServer()
	server.towerRepo = &mockTowerRepo{
		towerData: TowerData{TR: 10, TRP: 100},
	}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetTowerInfo{AckHandle: 100, InfoType: 1}
	handleMsgMhfGetTowerInfo(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestGetTowerInfo_InfoType2_Skills(t *testing.T) {
	server := createMockServer()
	server.towerRepo = &mockTowerRepo{
		towerData: TowerData{TSP: 50},
		skills:    "1,2,3",
	}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetTowerInfo{AckHandle: 100, InfoType: 2}
	handleMsgMhfGetTowerInfo(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestGetTowerInfo_InfoType3_Level(t *testing.T) {
	server := createMockServer()
	server.towerRepo = &mockTowerRepo{
		towerData: TowerData{Block1: 5, Block2: 3},
	}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetTowerInfo{AckHandle: 100, InfoType: 3}
	handleMsgMhfGetTowerInfo(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestGetTowerInfo_InfoType4_History(t *testing.T) {
	server := createMockServer()
	server.towerRepo = &mockTowerRepo{}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetTowerInfo{AckHandle: 100, InfoType: 4}
	handleMsgMhfGetTowerInfo(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestGetTowerInfo_InfoType5_Level(t *testing.T) {
	server := createMockServer()
	server.towerRepo = &mockTowerRepo{
		towerData: TowerData{Block1: 10, Block2: 7},
	}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetTowerInfo{AckHandle: 100, InfoType: 5}
	handleMsgMhfGetTowerInfo(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestGetTowerInfo_DBError(t *testing.T) {
	server := createMockServer()
	server.towerRepo = &mockTowerRepo{towerDataErr: errNotFound}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetTowerInfo{AckHandle: 100, InfoType: 1}
	handleMsgMhfGetTowerInfo(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

// --- handleMsgMhfGetGemInfo tests ---

func TestGetGemInfo_QueryType1_Gems(t *testing.T) {
	server := createMockServer()
	server.towerRepo = &mockTowerRepo{gems: "1,2,3,4,5"}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetGemInfo{AckHandle: 100, QueryType: 1}
	handleMsgMhfGetGemInfo(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestGetGemInfo_QueryType2_History(t *testing.T) {
	server := createMockServer()
	server.towerRepo = &mockTowerRepo{gems: "0,0,0"}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetGemInfo{AckHandle: 100, QueryType: 2}
	handleMsgMhfGetGemInfo(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestGetGemInfo_NoGems(t *testing.T) {
	server := createMockServer()
	server.towerRepo = &mockTowerRepo{gems: ""}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetGemInfo{AckHandle: 100, QueryType: 1}
	handleMsgMhfGetGemInfo(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

// --- handleMsgMhfPostGemInfo tests ---

func TestPostGemInfo_AddGem(t *testing.T) {
	server := createMockServer()
	server.towerRepo = &mockTowerRepo{gems: "0,0,0,0,0"}
	ensureTowerService(server)
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPostGemInfo{AckHandle: 100, Op: 1, Gem: 0x0101, Quantity: 5}
	handleMsgMhfPostGemInfo(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestPostGemInfo_Transfer(t *testing.T) {
	server := createMockServer()
	server.towerRepo = &mockTowerRepo{gems: "0,0,0,0,0"}
	ensureTowerService(server)
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPostGemInfo{AckHandle: 100, Op: 2, Gem: 0x0101, Quantity: 1}
	handleMsgMhfPostGemInfo(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestPostGemInfo_DebugMode(t *testing.T) {
	server := createMockServer()
	server.towerRepo = &mockTowerRepo{gems: "0,0,0,0,0"}
	server.erupeConfig.DebugOptions.QuestTools = true
	ensureTowerService(server)
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPostGemInfo{AckHandle: 100, Op: 1, Gem: 0x0101, Quantity: 3}
	handleMsgMhfPostGemInfo(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

// --- handleMsgMhfGetNotice / handleMsgMhfPostNotice tests ---

func TestGetNotice(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetNotice{AckHandle: 100}
	handleMsgMhfGetNotice(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestPostNotice(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfPostNotice{AckHandle: 100}
	handleMsgMhfPostNotice(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

// Tests consolidated from handlers_coverage3_test.go

func TestNonTrivialHandlers_TowerGo(t *testing.T) {
	server := createMockServer()

	tests := []struct {
		name string
		fn   func(s *Session)
	}{
		{"handleMsgMhfGetTenrouirai_Type1_C3", func(s *Session) {
			handleMsgMhfGetTenrouirai(s, &mhfpacket.MsgMhfGetTenrouirai{AckHandle: 1, Unk0: 1})
		}},
		{"handleMsgMhfGetTenrouirai_Unknown_C3", func(s *Session) {
			handleMsgMhfGetTenrouirai(s, &mhfpacket.MsgMhfGetTenrouirai{AckHandle: 1, Unk0: 0, DataType: 0})
		}},
		{"handleMsgMhfGetWeeklySeibatuRankingReward_C3", func(s *Session) {
			handleMsgMhfGetWeeklySeibatuRankingReward(s, &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{AckHandle: 1})
		}},
		{"handleMsgMhfPresentBox_C3", func(s *Session) {
			handleMsgMhfPresentBox(s, &mhfpacket.MsgMhfPresentBox{AckHandle: 1})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := createMockSession(1, server)
			tt.fn(session)
			select {
			case p := <-session.sendPackets:
				if len(p.data) == 0 {
					t.Errorf("%s: response should have data", tt.name)
				}
			default:
				t.Errorf("%s: no response queued", tt.name)
			}
		})
	}
}

func TestHandleMsgMhfPostTowerInfo_FloorReport(t *testing.T) {
	srv := createMockServer()
	repo := &mockTowerRepo{}
	srv.towerRepo = repo
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 6, Unk1: 1, Unk6: 2, Block1: 12}
	handleMsgMhfPostTowerInfo(s, pkt)
	<-s.sendPackets

	if repo.floorBlock != 2 || repo.floorValue != 12 {
		t.Fatalf("floor report not stored: block=%d floors=%d", repo.floorBlock, repo.floorValue)
	}
	if !s.towerMissionSubmissionReady.Load() {
		t.Fatal("floor report should arm the guild investigation submission")
	}
}

func TestHandleMsgMhfPostTowerInfo_FloorReportRejectsBadBlock(t *testing.T) {
	srv := createMockServer()
	repo := &mockTowerRepo{}
	srv.towerRepo = repo
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 6, Unk1: 1, Unk6: 3, Block1: 12}
	handleMsgMhfPostTowerInfo(s, pkt)
	<-s.sendPackets

	if repo.floorBlock != 0 || repo.floorValue != 0 {
		t.Fatalf("out-of-range block must not be stored: block=%d floors=%d", repo.floorBlock, repo.floorValue)
	}
	if s.towerMissionSubmissionReady.Load() {
		t.Fatal("rejected floor report must not arm the submission")
	}
}

func TestClaimTowerMissionSubmission_OncePerDeparture(t *testing.T) {
	srv := createMockServer()
	s := createMockSession(100, srv)

	if s.claimTowerMissionSubmission() {
		t.Fatal("no departure yet: nothing to claim")
	}
	s.questWeaponGeneration = 3
	if !s.claimTowerMissionSubmission() {
		t.Fatal("first submission of a departure must be accepted")
	}
	if s.claimTowerMissionSubmission() {
		t.Fatal("second submission of the same departure must be ignored")
	}
	s.towerMissionSubmissionReady.Store(true)
	if !s.claimTowerMissionSubmission() {
		t.Fatal("an explicitly armed submission must be accepted")
	}
	s.questWeaponGeneration = 4
	if !s.claimTowerMissionSubmission() {
		t.Fatal("a new departure must be accepted again")
	}
}

func TestAllowsTowerQuest_ZoneGate(t *testing.T) {
	srv := createMockServer()
	repo := &mockTowerRepo{towerData: TowerData{Block1: 3}}
	srv.towerRepo = repo
	srv.erupeConfig.TowerZone1UnlockFloor = 1
	srv.erupeConfig.TowerZone2UnlockFloor = 5
	s := createMockSession(100, srv)

	var cache *TowerData
	if !s.allowsTowerQuest(EventQuest{QuestID: 21729}, &cache) {
		t.Fatal("the prologue must always be listed")
	}
	if !s.allowsTowerQuest(EventQuest{QuestID: 21732}, &cache) {
		t.Fatal("zone 1 must be listed once the block-1 record reaches the threshold")
	}
	if s.allowsTowerQuest(EventQuest{QuestID: 21733}, &cache) {
		t.Fatal("zone 2 must stay hidden below its threshold")
	}
	srv.erupeConfig.TowerZone2UnlockFloor = 0
	if !s.allowsTowerQuest(EventQuest{QuestID: 21733}, &cache) {
		t.Fatal("a zero threshold disables the gate")
	}
}

func TestAllowsTowerQuest_GuardianMilestones(t *testing.T) {
	srv := createMockServer()
	repo := &mockTowerRepo{towerData: TowerData{Block1: 10, Block2: 41}}
	srv.towerRepo = repo
	s := createMockSession(100, srv)

	var cache *TowerData
	if !s.allowsTowerQuest(EventQuest{QuestID: 21731}, &cache) {
		t.Fatal("block-1 record on floor 10 must offer the block-1 Guardian arena")
	}
	if s.allowsTowerQuest(EventQuest{QuestID: 21746}, &cache) {
		t.Fatal("block-2 record on floor 41 is past the milestone: arena must be hidden")
	}
	for _, f := range []int32{40, 80, 120, 500} {
		if !towerGuardianFloor(f) {
			t.Fatalf("floor %d must be a Guardian milestone", f)
		}
	}
	for _, f := range []int32{0, 1, 9, 11, 39, 60, 499} {
		if towerGuardianFloor(f) {
			t.Fatalf("floor %d must not be a Guardian milestone", f)
		}
	}
}

func TestOrderTowerQuests(t *testing.T) {
	in := []EventQuest{{QuestID: 100}, {QuestID: 21729}, {QuestID: 21732}, {QuestID: 21733}, {QuestID: 21731}, {QuestID: 21746}, {QuestID: 200}}
	out := orderTowerQuests(in)
	got := make([]int, 0, len(out))
	for _, eq := range out {
		got = append(got, eq.QuestID)
	}
	want := []int{100, 21746, 21733, 21731, 21732, 21729, 200}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestAllowsTowerQuest_Zone2NeedsTowerRank(t *testing.T) {
	srv := createMockServer()
	repo := &mockTowerRepo{towerData: TowerData{TR: 50, Block1: 120}}
	srv.towerRepo = repo
	srv.erupeConfig.TowerZone2UnlockFloor = 1
	srv.erupeConfig.TowerZone2UnlockTR = 51
	s := createMockSession(100, srv)

	var cache *TowerData
	if s.allowsTowerQuest(EventQuest{QuestID: 21733}, &cache) {
		t.Fatal("zone 2 must stay hidden below Tower Rank 51")
	}
	repo.towerData.TR = 51
	cache = nil
	if !s.allowsTowerQuest(EventQuest{QuestID: 21733}, &cache) {
		t.Fatal("zone 2 must be listed at Tower Rank 51")
	}
}

func TestTowerHintTune(t *testing.T) {
	srv := createMockServer()
	s := createMockSession(100, srv)
	srv.erupeConfig.TowerHintSec = 1
	if got := towerHintTune(s); got != 1 {
		t.Fatalf("towerHintTune = %d, want 1", got)
	}
	srv.erupeConfig.TowerHintSec = 0
	if got := towerHintTune(s); got != 0 {
		t.Fatalf("towerHintTune(0) = %d, want 0 (client default)", got)
	}
}

func TestTowerProgressBlock(t *testing.T) {
	cases := []struct {
		infoType uint32
		unk6     int32
		want     uint8
	}{
		{7, 1, 1}, {7, 2, 2}, {7, 3, 3}, {7, 4, 4},
		{7, 0, 2}, {1, 0, 1}, {1, 9, 1},
	}
	for _, c := range cases {
		pkt := &mhfpacket.MsgMhfPostTowerInfo{InfoType: c.infoType, Unk6: c.unk6}
		if got := towerProgressBlock(pkt); got != c.want {
			t.Errorf("towerProgressBlock(InfoType %d, Unk6 %d) = %d, want %d", c.infoType, c.unk6, got, c.want)
		}
	}
}

func TestHandleMsgMhfPostTowerInfo_Type7RoutesFloorsByBlock(t *testing.T) {
	srv := createMockServer()
	repo := &mockTowerRepo{towerData: TowerData{TR: 3, Block1: 7, Block2: 4}}
	srv.towerRepo = repo
	s := createMockSession(100, srv)
	s.questWeaponGeneration = 5

	pkt := &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 7, TR: 4, TRP: 120, Cost: 2, Unk6: 2, Block1: 3}
	handleMsgMhfPostTowerInfo(s, pkt)
	<-s.sendPackets

	if repo.floorBlock != 2 || repo.floorValue != 7 {
		t.Fatalf("block-2 run must add its floors to block 2: block=%d floors=%d", repo.floorBlock, repo.floorValue)
	}
	if s.towerMissionBlock != 2 || s.towerProgressGeneration != 5 {
		t.Fatalf("progress must be recorded for this departure: block=%d generation=%d", s.towerMissionBlock, s.towerProgressGeneration)
	}
}

func TestHandleMsgMhfPostTenrouirai_SkipsTRPAfterProgress(t *testing.T) {
	srv := createMockServer()
	repo := &mockTowerRepo{}
	srv.towerRepo = repo
	srv.erupeConfig.TowerRankTRPPerRank = 100
	s := createMockSession(100, srv)
	s.questWeaponGeneration = 6
	s.towerProgressGeneration = 6

	pkt := &mhfpacket.MsgMhfPostTenrouirai{AckHandle: 1, Op: 1, Floors: 2, TRP: 50}
	handleMsgMhfPostTenrouirai(s, pkt)
	<-s.sendPackets

	if repo.addedTRP != 0 {
		t.Fatalf("TRP already posted through InfoType 7 must not be credited again, got %d", repo.addedTRP)
	}
}

func TestHandleMsgMhfPostTowerInfo_LearnChargesTSPOnlyForTowerStatus(t *testing.T) {
	for _, c := range []struct {
		unk1 uint32
		cost int32
	}{{0, 0}, {towerStatusLearnTag, 5}} {
		srv := createMockServer()
		repo := &mockTowerRepo{skills: EmptyTowerCSV(64)}
		srv.towerRepo = repo
		s := createMockSession(100, srv)

		pkt := &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 2, Unk1: c.unk1, Skill: 1, Cost: 5}
		handleMsgMhfPostTowerInfo(s, pkt)
		<-s.sendPackets

		// Skill 1 (Attack) is a common skill: its level lives in road_skills.
		if level := stringsupport.CSVGetIndex(repo.updatedRoadSkills, 1); repo.skillCost != c.cost || level != 1 {
			t.Errorf("Unk1 %#x: cost %d level %d, want cost %d level 1", c.unk1, repo.skillCost, level, c.cost)
		}
		if stringsupport.CSVGetIndex(repo.updatedSkills, 1) != 0 {
			t.Errorf("Unk1 %#x: a Road skill level was written to tower.skills", c.unk1)
		}
	}
}

func TestHandleMsgMhfPostTowerInfo_TowerOnlySkillGoesToTowerStore(t *testing.T) {
	srv := createMockServer()
	repo := &mockTowerRepo{skills: EmptyTowerCSV(64)}
	srv.towerRepo = repo
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 2, Unk1: towerStatusLearnTag, Skill: 10, Cost: 7}
	handleMsgMhfPostTowerInfo(s, pkt)
	<-s.sendPackets

	if level := stringsupport.CSVGetIndex(repo.updatedSkills, 10); level != 1 || repo.skillCost != 7 {
		t.Fatalf("Tower-only learn: level %d cost %d, want level 1 cost 7", level, repo.skillCost)
	}
	if repo.updatedRoadSkills != "" {
		t.Fatalf("Tower-only learn touched road_skills: %q", repo.updatedRoadSkills)
	}
}

func TestGetTowerInfo_InfoType2_MergesSkillStores(t *testing.T) {
	tower := EmptyTowerCSV(64)
	tower = stringsupport.CSVSetIndex(tower, 10, 2) // Tower-only: kept
	tower = stringsupport.CSVSetIndex(tower, 1, 5)  // stale Road level: ignored
	road := EmptyTowerCSV(64)
	road = stringsupport.CSVSetIndex(road, 1, 3)
	road = stringsupport.CSVSetIndex(road, 10, 9) // stale Tower level: ignored
	road = stringsupport.CSVSetIndex(road, 23, 1)
	srv := createMockServer()
	srv.towerRepo = &mockTowerRepo{towerData: TowerData{TSP: 4, Skills: tower}, roadSkills: road}
	s := createMockSession(1, srv)

	handleMsgMhfGetTowerInfo(s, &mhfpacket.MsgMhfGetTowerInfo{AckHandle: 1, InfoType: 2})
	_, code, payload := parseAckBufData(t, (<-s.sendPackets).data)
	if code != 0 || len(payload) < 16+4+128 {
		t.Fatalf("InfoType 2 code=%d len=%d", code, len(payload))
	}
	level := func(id int) int16 { return int16(binary.BigEndian.Uint16(payload[16+4+id*2:])) }
	if level(1) != 3 || level(10) != 2 || level(23) != 1 {
		t.Fatalf("merged levels 1=%d 10=%d 23=%d, want 3 2 1", level(1), level(10), level(23))
	}
}

func TestHandleMsgMhfPostTowerInfo_TowerStatusConvertAndReset(t *testing.T) {
	tower := EmptyTowerCSV(64)
	tower = stringsupport.CSVSetIndex(tower, 6, 3) // 1+2+4
	tower = stringsupport.CSVSetIndex(tower, 4, 2) // 3+7
	srv := createMockServer()
	repo := &mockTowerRepo{skills: tower, towerData: TowerData{TSP: 5}}
	srv.towerRepo = repo
	s := createMockSession(100, srv)

	handleMsgMhfPostTowerInfo(s, &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 5, Cost: 1})
	<-s.sendPackets
	if repo.addedTSP != 0 {
		t.Fatalf("an untagged (Hunting Road) conversion added %d TSP", repo.addedTSP)
	}
	handleMsgMhfPostTowerInfo(s, &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 2, InfoType: 5, Unk1: towerStatusLearnTag, Cost: 1})
	<-s.sendPackets
	if repo.addedTSP != 1 {
		t.Fatalf("Tower Status conversion added %d TSP, want 1", repo.addedTSP)
	}
	handleMsgMhfPostTowerInfo(s, &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 3, InfoType: 3, Unk1: towerStatusLearnTag, Cost: 22})
	<-s.sendPackets
	if !repo.towerReset || repo.towerResetRefund != 17 || repo.updatedRoadSkills != "" {
		t.Fatalf("Tower reset: done %v refund %d road %q, want refund 17 and no Road change",
			repo.towerReset, repo.towerResetRefund, repo.updatedRoadSkills)
	}
}

func TestHandleMsgMhfPostTowerInfo_RoadResetKeepsTowerSkills(t *testing.T) {
	srv := createMockServer()
	tower := stringsupport.CSVSetIndex(EmptyTowerCSV(64), 10, 1)
	road := EmptyTowerCSV(64)
	road = stringsupport.CSVSetIndex(road, 1, 2)
	road = stringsupport.CSVSetIndex(road, 23, 3)
	repo := &mockTowerRepo{skills: tower, roadSkills: road}
	srv.towerRepo = repo
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfPostTowerInfo{AckHandle: 1, InfoType: 3, Cost: 40}
	handleMsgMhfPostTowerInfo(s, pkt)
	<-s.sendPackets

	got := stringsupport.CSVElems(repo.updatedRoadSkills)
	if len(got) != 64 || got[1] != 0 || got[23] != 0 {
		t.Fatalf("a Road reset must clear every Road level: %v", got)
	}
	if repo.updatedSkills != "" || repo.skillCost != 0 {
		t.Fatalf("a Road reset touched tower.skills or TSP: %q cost %d", repo.updatedSkills, repo.skillCost)
	}
}
