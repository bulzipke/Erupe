package channelserver

import (
	"bytes"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

// Handler-only fake: no database connection or SQL transactions. The production
// repository has separate tests for membership, cutover and replay enforcement.
type divaInterceptionHandlerFakeRepo struct {
	divaRewardHandlerFakeRepo
	rewardPeriods                                                  []DivaEvent
	rewardWindowErr                                                error
	interception                                                   map[uint32]DivaInterceptionProgress
	interceptionErr, newAddErr, legacyErr, legacyAddErr, prizesErr error
	progressEvents                                                 []uint32
	legacy                                                         map[string]int
	legacyReads, legacyAdds, prizeReads                            int
	prizes                                                         []DivaPrize
	onProgress                                                     func()
	newAdds                                                        []divaInterceptionHandlerAdd
}

type divaInterceptionHandlerAdd struct {
	charID, eventID, points, guildID uint32
	questID                          uint16
	key                              string
	started, now                     time.Time
}

var _ DivaInterceptionRepository = (*divaInterceptionHandlerFakeRepo)(nil)
var _ DivaRewardRepository = (*divaInterceptionHandlerFakeRepo)(nil)

func (r *divaInterceptionHandlerFakeRepo) GetDivaInterceptionRewardEvent(now time.Time) (DivaEvent, error) {
	if r.rewardWindowErr != nil {
		return DivaEvent{}, r.rewardWindowErr
	}
	periods := r.rewardPeriods
	if periods == nil {
		periods = r.events
	}
	var latest DivaEvent
	for _, event := range periods {
		start, _ := divaInterceptionWindow(event)
		if !now.Before(start) && (event.StartTime > latest.StartTime || (event.StartTime == latest.StartTime && event.ID > latest.ID)) {
			latest = event
		}
	}
	if latest.ID != 0 && !r.interception[latest.ID].Enabled {
		return DivaEvent{}, nil
	}
	return latest, nil
}

func (r *divaInterceptionHandlerFakeRepo) OfferDivaInterceptionRewards(charID, eventID uint32, rows []DivaRewardCatalogEntry, _ int) ([]DivaRewardOffer, error) {
	return r.OfferDivaRewards(charID, eventID, 6, rows)
}

func (r *divaInterceptionHandlerFakeRepo) PrepareDivaInterceptionRewardClaims(charID uint32, ids []uint32, _ int) ([]DivaRewardOffer, error) {
	return r.PrepareDivaRewardClaims(charID, 6, ids)
}

func (r *divaInterceptionHandlerFakeRepo) GetDivaInterceptionProgress(charID, eventID uint32) (DivaInterceptionProgress, error) {
	r.progressEvents = append(r.progressEvents, eventID)
	if r.onProgress != nil {
		hook := r.onProgress
		r.onProgress = nil
		hook()
	}
	return r.interception[eventID], r.interceptionErr
}

func (r *divaInterceptionHandlerFakeRepo) AddDivaInterceptionPoints(charID, eventID uint32, questID uint16, points, guildID uint32, key string, started, now time.Time) error {
	r.newAdds = append(r.newAdds, divaInterceptionHandlerAdd{charID, eventID, points, guildID, questID, key, started, now})
	if r.newAddErr != nil {
		return r.newAddErr
	}
	if !validDivaInterceptionRunKey(key) {
		return ErrDivaInterceptionInvalid
	}
	progress := r.interception[eventID]
	if progress.QuestPoints == nil {
		progress.QuestPoints = make(map[uint16]int64)
	}
	progress.Points += int64(points)
	progress.QuestPoints[questID] += int64(points)
	r.interception[eventID] = progress
	return nil
}

func (r *divaInterceptionHandlerFakeRepo) GetCharacterInterceptionPoints(uint32) (map[string]int, error) {
	r.legacyReads++
	return r.legacy, r.legacyErr
}

func (r *divaInterceptionHandlerFakeRepo) AddInterceptionPoints(_ uint32, questID, points int) error {
	r.legacyAdds++
	if r.legacyAddErr != nil {
		return r.legacyAddErr
	}
	if r.legacy == nil {
		r.legacy = make(map[string]int)
	}
	r.legacy[strconv.Itoa(questID)] += points
	return nil
}

func (r *divaInterceptionHandlerFakeRepo) GetPersonalPrizes() ([]DivaPrize, error) {
	r.prizeReads++
	return r.prizes, r.prizesErr
}

func divaInterceptionHandlerTestPrizes() []DivaPrize {
	return []DivaPrize{
		{ID: 1, Type: "personal", PointsReq: 50, ItemType: 7, ItemID: 1, Quantity: 2},
		{ID: 2, Type: "personal", PointsReq: 100, GR: true, ItemType: 7, ItemID: 2, Quantity: 3},
		{ID: 3, Type: "personal", PointsReq: 200, ItemType: 7, ItemID: 3, Quantity: 4},
		{ID: 4, Type: "guild", PointsReq: 1, ItemType: 7, ItemID: 4, Quantity: 5},
		{ID: 5, Type: "personal", PointsReq: 1, Repeatable: true, ItemType: 7, ItemID: 5, Quantity: 6},
		{ID: 6, Type: "personal", PointsReq: 0, ItemType: 7, ItemID: 6, Quantity: 7},
		{ID: 7, Type: "personal", PointsReq: 100, ItemType: 26, Quantity: 250},
	}
}

func newDivaInterceptionHandlerTestSession() (*Session, *divaInterceptionHandlerFakeRepo, DivaEvent) {
	event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Unix() - divaPhaseDuration - divaInterlude - 3600)}
	s, rewards := newDivaRewardHandlerTestSession(event)
	s.server.erupeConfig.GameplayOptions.DisableDailyGachaCoins = true
	s.server.guildRepo = &mockGuildRepo{membership: &GuildMember{GuildID: 99}}
	repo := &divaInterceptionHandlerFakeRepo{divaRewardHandlerFakeRepo: *rewards,
		interception: map[uint32]DivaInterceptionProgress{event.ID: {
			Enabled: true, GR: 1, Points: 100, QuestPoints: map[uint16]int64{58043: 100},
		}}, legacy: map[string]int{"58044": 9999}, prizes: divaInterceptionHandlerTestPrizes()}
	s.server.divaRepo = repo
	return s, repo, event
}

func TestDivaInterceptionPersonalEligibility(t *testing.T) {
	for _, tt := range []struct {
		name    string
		enabled bool
		gr      uint16
		points  int64
		keys    []string
	}{
		{"new GR round", true, 1, 100, []string{"tactics-personal-1", "tactics-personal-2", "tactics-personal-7"}},
		{"HR excludes GR-only", true, 0, 100, []string{"tactics-personal-1", "tactics-personal-7"}},
		{"below first threshold", true, 1, 49, nil},
		{"exact first threshold", true, 1, 50, []string{"tactics-personal-1"}},
		{"below next threshold", true, 1, 99, []string{"tactics-personal-1"}},
		{"legacy is not eligible", false, 999, 999999, nil},
		{"zero points", true, 999, 0, nil},
		{"negative points", true, 999, -1, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := eligibleDivaInterceptionRewards(divaInterceptionHandlerTestPrizes(), DivaInterceptionProgress{Enabled: tt.enabled, GR: tt.gr, Points: tt.points})
			if err != nil {
				t.Fatal(err)
			}
			var keys []string
			for _, row := range rows {
				keys = append(keys, row.Key)
				if row.RewardType != 6 || int64(row.Threshold) > tt.points {
					t.Fatalf("wrong category/threshold: %+v", row)
				}
			}
			if !reflect.DeepEqual(keys, tt.keys) {
				t.Fatalf("eligible keys = %v, want %v", keys, tt.keys)
			}
		})
	}
	base := DivaPrize{ID: 1, Type: "personal", PointsReq: 1, ItemType: 7, ItemID: 1, Quantity: 1}
	for _, tt := range []struct {
		name   string
		change func(*DivaPrize)
	}{
		{"missing ID", func(p *DivaPrize) { p.ID = 0 }},
		{"negative item", func(p *DivaPrize) { p.ItemID = -1 }},
		{"wide item", func(p *DivaPrize) { p.ItemID = 65536 }},
		{"missing material", func(p *DivaPrize) { p.ItemID = 0 }},
		{"negative kind", func(p *DivaPrize) { p.ItemType = -1 }},
		{"wide kind", func(p *DivaPrize) { p.ItemType = 256 }},
		{"unsupported kind", func(p *DivaPrize) { p.ItemType = 6 }},
		{"GP with item ID", func(p *DivaPrize) { p.ItemType = 26 }},
		{"zero quantity", func(p *DivaPrize) { p.Quantity = 0 }},
		{"wide quantity", func(p *DivaPrize) { p.Quantity = 65536 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prize := base
			tt.change(&prize)
			if _, err := eligibleDivaInterceptionRewards([]DivaPrize{prize}, DivaInterceptionProgress{Enabled: true, GR: 1, Points: 1}); err == nil {
				t.Fatal("invalid eligible prize accepted")
			}
		})
	}
}

func TestDivaInterceptionRewardQueries(t *testing.T) {
	for _, tt := range []struct {
		name      string
		change    func(*Session, *divaInterceptionHandlerFakeRepo, DivaEvent, *mhfpacket.MsgMhfAcquireUdItem)
		wantCount int
		wantFail  bool
	}{
		{"new personal category 6", func(*Session, *divaInterceptionHandlerFakeRepo, DivaEvent, *mhfpacket.MsgMhfAcquireUdItem) {}, 3, false},
		{"HR subset", func(_ *Session, r *divaInterceptionHandlerFakeRepo, e DivaEvent, _ *mhfpacket.MsgMhfAcquireUdItem) {
			p := r.interception[e.ID]
			p.GR = 0
			r.interception[e.ID] = p
		}, 2, false},
		{"legacy round empty", func(_ *Session, r *divaInterceptionHandlerFakeRepo, e DivaEvent, _ *mhfpacket.MsgMhfAcquireUdItem) {
			p := r.interception[e.ID]
			p.Enabled = false
			r.interception[e.ID] = p
		}, 0, false},
		{"guild category 7 stays empty", func(_ *Session, _ *divaInterceptionHandlerFakeRepo, _ DivaEvent, p *mhfpacket.MsgMhfAcquireUdItem) {
			p.RewardType = 7
		}, 0, false},
		{"forced prayer retains prior interception rewards", func(s *Session, _ *divaInterceptionHandlerFakeRepo, _ DivaEvent, _ *mhfpacket.MsgMhfAcquireUdItem) {
			s.server.erupeConfig.DebugOptions.DivaOverride = 1
		}, 3, false},
		{"before interception", func(_ *Session, r *divaInterceptionHandlerFakeRepo, _ DivaEvent, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.events[0].StartTime = uint32(TimeAdjusted().Add(-time.Hour).Unix())
		}, 0, false},
		{"progress failure", func(_ *Session, r *divaInterceptionHandlerFakeRepo, _ DivaEvent, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.interceptionErr = errors.New("progress offline")
		}, 0, true},
		{"catalog failure", func(_ *Session, r *divaInterceptionHandlerFakeRepo, _ DivaEvent, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.prizesErr = errors.New("catalog offline")
		}, 0, true},
		{"invalid catalog item", func(_ *Session, r *divaInterceptionHandlerFakeRepo, _ DivaEvent, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.prizes[0].Quantity = 0
		}, 0, true},
		{"offer failure", func(_ *Session, r *divaInterceptionHandlerFakeRepo, _ DivaEvent, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.offerErr = errors.New("offers offline")
		}, 0, true},
		{"oversize offers", func(_ *Session, r *divaInterceptionHandlerFakeRepo, _ DivaEvent, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.offerOverride = make([]DivaRewardOffer, 33)
		}, 0, true},
		{"repository extension absent", func(s *Session, r *divaInterceptionHandlerFakeRepo, _ DivaEvent, _ *mhfpacket.MsgMhfAcquireUdItem) {
			s.server.divaRepo = &r.divaRewardHandlerFakeRepo
		}, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, repo, event := newDivaInterceptionHandlerTestSession()
			pkt := &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 1, Unk0: 1, RewardType: 6}
			tt.change(s, repo, event, pkt)
			handleMsgMhfAcquireUdItem(s, pkt)
			ack := readAck(t, s)
			if (ack.ErrorCode != 0) != tt.wantFail {
				t.Fatalf("wrong status: %+v", ack)
			}
			if tt.wantFail {
				if len(ack.Payload) != 0 {
					t.Fatal("failed eligibility leaked rewards")
				}
				return
			}
			if len(ack.Payload) != 2+tt.wantCount*9 || int(ack.Payload[1]) != tt.wantCount {
				t.Fatalf("wrong reward count: %+v", ack)
			}
			if tt.wantCount > 0 && (repo.offerKind != 6 || repo.offerEvent != event.ID || repo.offerChar != s.charID) {
				t.Fatal("reward offer lost round/character/category scope")
			}
			if pkt.RewardType == 7 && (repo.prizeReads != 0 || repo.offerCalls != 0 || len(repo.progressEvents) != 0) {
				t.Fatal("guild reward request consulted personal eligibility")
			}
			if repo.legacyReads != 0 || repo.legacyAdds != 0 || repo.prepareCalls != 0 || s.hasPendingDivaRewardClaims() {
				t.Fatal("query consumed rewards or consulted unversioned legacy totals")
			}
		})
	}
}

func TestDivaInterceptionRoundPointsAndLegacyFallback(t *testing.T) {
	s, repo, event := newDivaInterceptionHandlerTestSession()
	read := func(wantTotal uint32, wantIDs []uint16) {
		t.Helper()
		handleDivaTacticsPoint(s, &mhfpacket.MsgMhfGetUdTacticsPoint{AckHandle: 2})
		ack := readAck(t, s)
		if ack.ErrorCode != 0 || len(ack.Payload) != 0x204 {
			t.Fatalf("point query failed: %+v", ack)
		}
		total, ids := readDivaTacticsQuestPayload(t, ack.Payload, true)
		if total != wantTotal || !reflect.DeepEqual(ids[:len(wantIDs)], wantIDs) {
			t.Fatalf("round points/list = %d/%v, want %d/%v", total, ids[:len(wantIDs)], wantTotal, wantIDs)
		}
		for _, id := range ids[len(wantIDs):] {
			if id != 0 {
				t.Fatal("previous completed-quest slots were not cleared")
			}
		}
	}
	read(100, []uint16{58043})
	if repo.legacyReads != 0 {
		t.Fatal("enabled round imported legacy JSON points")
	}
	// A new event must show its own zero balance/list, never last round or JSON.
	next := DivaEvent{ID: event.ID + 1, StartTime: event.StartTime}
	repo.events = append(repo.events, next)
	repo.interception[next.ID] = DivaInterceptionProgress{Enabled: true, QuestPoints: map[uint16]int64{}}
	read(0, []uint16{})
	if repo.legacyReads != 0 || repo.progressEvents[len(repo.progressEvents)-1] != next.ID {
		t.Fatal("new round fell back to old data")
	}
	p := repo.interception[next.ID]
	p.Enabled = false
	repo.interception[next.ID] = p
	repo.legacy = map[string]int{"58043": 10, "58128": 20, "58044": -1, "1": 400, "65536": 500, "bad": 600}
	read(30, []uint16{58043, 58128})
	if repo.legacyReads != 1 {
		t.Fatal("legacy round no longer reads historical totals")
	}
	// A repository without the new extension still exposes its old totals.
	s.server.divaRepo = struct{ DivaRepo }{repo}
	read(30, []uint16{58043, 58128})
	if repo.legacyReads != 2 {
		t.Fatal("legacy-only repository did not fall back")
	}
}

func TestDivaInterceptionPointReadErrorsAndSaturation(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		s, repo, event := newDivaInterceptionHandlerTestSession()
		if legacy {
			p := repo.interception[event.ID]
			p.Enabled = false
			repo.interception[event.ID] = p
			repo.legacyErr = errors.New("legacy offline")
		} else {
			repo.interceptionErr = errors.New("round offline")
		}
		handleDivaTacticsPoint(s, &mhfpacket.MsgMhfGetUdTacticsPoint{AckHandle: 3})
		if ack := readAck(t, s); ack.ErrorCode == 0 || len(ack.Payload) != 0 {
			t.Fatalf("failed point read reported success: %+v", ack)
		}
	}
	s, repo, event := newDivaInterceptionHandlerTestSession()
	p := repo.interception[event.ID]
	p.QuestPoints = map[uint16]int64{58043: 0x7fffffffffffffff, 58044: 0x7fffffff, 58045: -1}
	repo.interception[event.ID] = p
	handleDivaTacticsPoint(s, &mhfpacket.MsgMhfGetUdTacticsPoint{AckHandle: 4})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 {
		t.Fatal("saturated point read failed")
	}
	total, ids := readDivaTacticsQuestPayload(t, ack.Payload, true)
	if total != 0x7fffffff || !reflect.DeepEqual(ids[:3], []uint16{58043, 58044, 0}) {
		t.Fatal("native signed points wrapped or negative quest count leaked")
	}
}

func TestDivaInterceptionAddRequiresRealBoundDeparture(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Session, *divaInterceptionHandlerFakeRepo, *mhfpacket.MsgMhfAddUdTacticsPoint)
	}{
		{"no departure", func(*Session, *divaInterceptionHandlerFakeRepo, *mhfpacket.MsgMhfAddUdTacticsPoint) {}},
		{"zero points", func(_ *Session, _ *divaInterceptionHandlerFakeRepo, p *mhfpacket.MsgMhfAddUdTacticsPoint) {
			p.TacticsPoints = 0
		}},
		{"invalid quest", func(_ *Session, _ *divaInterceptionHandlerFakeRepo, p *mhfpacket.MsgMhfAddUdTacticsPoint) {
			p.QuestID = 1
		}},
		{"wrong quest snapshot", func(s *Session, _ *divaInterceptionHandlerFakeRepo, _ *mhfpacket.MsgMhfAddUdTacticsPoint) {
			s.divaTacticsRun = divaInterceptionRun{QuestID: 58044, Key: "bound-other-quest"}
		}},
		{"unverified client", func(s *Session, _ *divaInterceptionHandlerFakeRepo, _ *mhfpacket.MsgMhfAddUdTacticsPoint) {
			s.server.erupeConfig.RealClientMode = cfg.Z2
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, repo, event := newDivaInterceptionHandlerTestSession()
			pkt := &mhfpacket.MsgMhfAddUdTacticsPoint{AckHandle: 5, QuestID: 58043, TacticsPoints: 200}
			tt.change(s, repo, pkt)
			handleDivaTacticsAdd(s, pkt)
			ack := readAck(t, s)
			if ack.ErrorCode != 0 || len(ack.Payload) != 0x200 {
				t.Fatalf("ignored report lost completion-list response: %+v", ack)
			}
			if len(repo.newAdds) != 0 || repo.legacyAdds != 0 || repo.interception[event.ID].Points != 100 || repo.legacy["58044"] != 9999 {
				t.Fatal("unbound report modified new-round or legacy totals")
			}
			_, ids := readDivaTacticsQuestPayload(t, ack.Payload, false)
			if !reflect.DeepEqual(ids[:2], []uint16{58043, 0}) {
				t.Fatal("ignored report returned old-round completed quests")
			}
		})
	}
}

func TestDivaInterceptionLegacyAddDoesNotCreateNewEntitlement(t *testing.T) {
	for _, tt := range []struct {
		name          string
		enabled, fail bool
		phase         int
		wantAdds      int
	}{
		{"old round accumulates only JSON", false, false, -1, 1},
		{"new round no binding is ignored", true, false, -1, 0},
		{"disabled phase", false, false, 0, 0},
		{"prayer phase", false, false, 1, 0},
		{"welcome phase", false, false, 3, 0},
		{"legacy write error", false, true, -1, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, repo, event := newDivaInterceptionHandlerTestSession()
			p := repo.interception[event.ID]
			p.Enabled = tt.enabled
			repo.interception[event.ID] = p
			s.server.erupeConfig.DebugOptions.DivaOverride = tt.phase
			if tt.fail {
				repo.legacyAddErr = errors.New("legacy write offline")
			}
			handleDivaTacticsAdd(s, &mhfpacket.MsgMhfAddUdTacticsPoint{AckHandle: 6, QuestID: 58044, TacticsPoints: 25})
			ack := readAck(t, s)
			if (ack.ErrorCode != 0) != tt.fail || repo.legacyAdds != tt.wantAdds || len(repo.newAdds) != 0 || repo.offerCalls != 0 {
				t.Fatalf("wrong legacy/add behavior: %+v", ack)
			}
			want := 9999
			if !tt.fail && tt.wantAdds != 0 {
				want += 25
			}
			if repo.legacy["58044"] != want || repo.interception[event.ID].Points != 100 {
				t.Fatal("legacy JSON and round-scoped ledger were mixed")
			}
		})
	}
}

func drainDivaInterceptionStagePackets(s *Session) {
	for len(s.sendPackets) > 0 {
		<-s.sendPackets
	}
}

func enterDivaInterceptionTestQuest(t *testing.T, s *Session, id string, questID uint16) {
	t.Helper()
	stage := NewStage(id)
	stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(questID, 0)
	s.server.stages.Store(id, stage)
	if !doStageTransfer(s, 100, id) {
		t.Fatal("validated quest entry failed")
	}
	drainDivaInterceptionStagePackets(s)
}

func TestDivaInterceptionDepartureSurvivesTownAndUsesBoundRound(t *testing.T) {
	s, repo, event := newDivaInterceptionHandlerTestSession()
	enterDivaInterceptionTestQuest(t, s, "sl2Qs200p0a1u0", 58043)
	bound := s.divaInterceptionRunSnapshot()
	if bound.QuestID != 58043 || bound.EventID != event.ID || bound.GuildID != 99 || bound.StartedAt.IsZero() || !validDivaInterceptionRunKey(bound.Key) {
		t.Fatalf("real departure did not bind a repository-compatible run: %+v", bound)
	}
	// A duplicate callback must reuse the original key, never arm a second run.
	s.captureDivaInterceptionDeparture(58043, bound.Generation)
	if got := s.divaInterceptionRunSnapshot(); got != bound {
		t.Fatal("repeat departure callback changed the run")
	}
	town := "sl2Ns200p0a1u0"
	s.server.stages.Store(town, NewStage(town))
	if !doStageTransfer(s, 101, town) {
		t.Fatal("return to town failed")
	}
	drainDivaInterceptionStagePackets(s)
	if got := s.divaInterceptionRunSnapshot(); got != bound {
		t.Fatal("return to town erased the late-report snapshot")
	}
	// Read current event changes without granting authority to reassign a
	// completed report; the real repository separately validates phase timing.
	next := DivaEvent{ID: event.ID + 1, StartTime: event.StartTime}
	repo.events = append(repo.events, next)
	repo.interception[next.ID] = DivaInterceptionProgress{Enabled: true, QuestPoints: map[uint16]int64{}}
	handleDivaTacticsAdd(s, &mhfpacket.MsgMhfAddUdTacticsPoint{AckHandle: 7, QuestID: 58043, TacticsPoints: 25})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || len(repo.newAdds) != 1 {
		t.Fatalf("bound report failed: %+v", ack)
	}
	_, completed := readDivaTacticsQuestPayload(t, ack.Payload, false)
	for _, questID := range completed {
		if questID != 0 {
			t.Fatal("old bound report restored old completed quests in the new round's client UI")
		}
	}
	add := repo.newAdds[0]
	if add.charID != s.charID || add.eventID != event.ID || add.questID != 58043 || add.points != 25 || add.guildID != 99 || add.key != bound.Key || !add.started.Equal(bound.StartedAt) || add.now.Before(add.started) {
		t.Fatalf("report lost departure scope: %+v", add)
	}
	if repo.interception[event.ID].Points != 125 || repo.interception[next.ID].Points != 0 || repo.legacyAdds != 0 {
		t.Fatal("report was credited to current round or legacy JSON")
	}
	if s.divaInterceptionRunSnapshot() != bound {
		t.Fatal("successful ACK removed the idempotent retry key")
	}
	// A fresh real departure replaces the old snapshot, including its event/key.
	enterDivaInterceptionTestQuest(t, s, "sl2Qs200p0a2u0", 58044)
	fresh := s.divaInterceptionRunSnapshot()
	if fresh.Generation <= bound.Generation || fresh.Key == bound.Key || fresh.EventID != next.ID || fresh.QuestID != 58044 || !validDivaInterceptionRunKey(fresh.Key) {
		t.Fatalf("new generation reused the old binding: %+v", fresh)
	}
}

func TestDivaInterceptionStaleBindingCannotOverwriteNewDeparture(t *testing.T) {
	s, repo, _ := newDivaInterceptionHandlerTestSession()
	started := TimeAdjusted()
	s.questWeaponGeneration = 1
	s.divaTacticsRun = divaInterceptionRun{Generation: 1, StartedAt: started}
	// Simulate another departure while a repository lookup is in flight. The
	// post-lookup generation check must discard the old result atomically.
	next := divaInterceptionRun{Generation: 2, StartedAt: started.Add(time.Second)}
	repo.onProgress = func() {
		s.lifecycleMu.Lock()
		s.questWeaponGeneration = 2
		s.divaTacticsRun = next
		s.lifecycleMu.Unlock()
	}
	s.captureDivaInterceptionDeparture(58043, 1)
	if got := s.divaInterceptionRunSnapshot(); got != next {
		t.Fatalf("late old-generation lookup overwrote newer departure: %+v", got)
	}
	queries := len(repo.progressEvents)
	s.captureDivaInterceptionDeparture(58043, 1)
	if len(repo.progressEvents) != queries || s.divaInterceptionRunSnapshot() != next {
		t.Fatal("stale generation initiated another binding")
	}
}

func TestDivaInterceptionBoundAddErrors(t *testing.T) {
	for _, which := range []string{"write", "read after write"} {
		t.Run(which, func(t *testing.T) {
			s, repo, _ := newDivaInterceptionHandlerTestSession()
			enterDivaInterceptionTestQuest(t, s, "sl2Qs200p0a1u0", 58043)
			bound := s.divaInterceptionRunSnapshot()
			if which == "write" {
				repo.newAddErr = errors.New("write offline")
			} else {
				repo.interceptionErr = errors.New("read offline")
			}
			handleDivaTacticsAdd(s, &mhfpacket.MsgMhfAddUdTacticsPoint{AckHandle: 8, QuestID: 58043, TacticsPoints: 25})
			ack := readAck(t, s)
			if ack.ErrorCode == 0 || len(ack.Payload) != 0 || len(repo.newAdds) != 1 || repo.legacyAdds != 0 || s.divaInterceptionRunSnapshot() != bound {
				t.Fatalf("failed save/read reported success or lost retry scope: %+v", ack)
			}
		})
	}
}

func TestDivaInterceptionDepartureNeedsEligibility(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Session, *divaInterceptionHandlerFakeRepo, DivaEvent)
	}{
		{"legacy event", func(_ *Session, r *divaInterceptionHandlerFakeRepo, e DivaEvent) {
			p := r.interception[e.ID]
			p.Enabled = false
			r.interception[e.ID] = p
		}},
		{"no guild", func(s *Session, _ *divaInterceptionHandlerFakeRepo, _ DivaEvent) {
			s.server.guildRepo = &mockGuildRepo{}
		}},
		{"applicant", func(s *Session, _ *divaInterceptionHandlerFakeRepo, _ DivaEvent) {
			s.server.guildRepo = &mockGuildRepo{membership: &GuildMember{GuildID: 99, IsApplicant: true}}
		}},
		{"lookup error", func(_ *Session, r *divaInterceptionHandlerFakeRepo, _ DivaEvent) {
			r.interceptionErr = errors.New("offline")
		}},
		{"before phase", func(s *Session, _ *divaInterceptionHandlerFakeRepo, e DivaEvent) {
			s.divaTacticsRun.StartedAt = time.Unix(int64(e.StartTime), 0)
		}},
		{"at phase end", func(s *Session, _ *divaInterceptionHandlerFakeRepo, e DivaEvent) {
			_, s.divaTacticsRun.StartedAt = divaInterceptionWindow(e)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, repo, event := newDivaInterceptionHandlerTestSession()
			s.questWeaponGeneration = 1
			s.divaTacticsRun = divaInterceptionRun{Generation: 1, StartedAt: TimeAdjusted()}
			tt.change(s, repo, event)
			s.captureDivaInterceptionDeparture(58043, 1)
			if run := s.divaInterceptionRunSnapshot(); run.Key != "" || run.EventID != 0 {
				t.Fatalf("ineligible departure bound: %+v", run)
			}
		})
	}
}

func TestDivaInterceptionGuildRewardAlwaysEmpty(t *testing.T) {
	s, repo, _ := newDivaInterceptionHandlerTestSession()
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 9, Unk0: 1, RewardType: 7})
	if ack := readAck(t, s); ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, []byte{0, 0}) || repo.offerCalls != 0 {
		t.Fatalf("unimplemented guild reward was granted: %+v", ack)
	}
}

func TestDivaInterceptionRewardsSurviveNextPrayerAndOldLifetime(t *testing.T) {
	for _, phase := range []int{-1, 1, 3} {
		t.Run(strconv.Itoa(phase), func(t *testing.T) {
			s, repo, old := newDivaInterceptionHandlerTestSession()
			now := TimeAdjusted()
			old.StartTime = uint32(now.Add(-40*24*time.Hour).Unix() - divaPhaseDuration - divaInterlude)
			next := DivaEvent{ID: old.ID + 1, StartTime: uint32(now.Add(-time.Hour).Unix())}
			repo.events = []DivaEvent{next}
			repo.rewardPeriods = []DivaEvent{old}
			s.server.erupeConfig.DebugOptions.DivaOverride = phase
			handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 91, Unk0: 1, RewardType: 6})
			ack := readAck(t, s)
			if ack.ErrorCode != 0 || len(ack.Payload) != 29 || ack.Payload[1] != 3 || repo.offerEvent != old.ID {
				t.Fatalf("prior round unavailable in phase %d: %+v offeredEvent=%d", phase, ack, repo.offerEvent)
			}
			handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 92, RewardType: 6, ItemIDCount: 1, RewardIDs: []uint32{repo.offers[0].ID}})
			if ack = readAck(t, s); ack.ErrorCode != 0 || !s.hasPendingDivaRewardClaims() {
				t.Fatalf("prior receipt could not stage: %+v", ack)
			}
		})
	}
}

func TestDivaInterceptionRewardsDoNotFallbackAfterNextStart(t *testing.T) {
	s, repo, old := newDivaInterceptionHandlerTestSession()
	next := DivaEvent{ID: old.ID + 1, StartTime: uint32(TimeAdjusted().Unix() - divaPhaseDuration - divaInterlude)}
	repo.events = []DivaEvent{next}
	repo.rewardPeriods = []DivaEvent{old, next}
	repo.interception[next.ID] = DivaInterceptionProgress{Enabled: true, GR: 1, QuestPoints: map[uint16]int64{}}
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 93, Unk0: 1, RewardType: 6})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, []byte{0, 0}) || repo.offerEvent != next.ID {
		t.Fatalf("new round fell back to old points: %+v offeredEvent=%d", ack, repo.offerEvent)
	}
	repo.rewardWindowErr = errors.New("period lookup failed")
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 94, Unk0: 1, RewardType: 6})
	if ack = readAck(t, s); ack.ErrorCode == 0 {
		t.Fatal("period failure silently served old rewards")
	}
}

func TestDivaInterceptionDisplayedPointsFollowRewardWindow(t *testing.T) {
	s, repo, old := newDivaInterceptionHandlerTestSession()
	next := DivaEvent{ID: old.ID + 1, StartTime: uint32(TimeAdjusted().Unix() - 3600)}
	repo.events = []DivaEvent{next}
	repo.rewardPeriods = []DivaEvent{old}
	repo.interception[next.ID] = DivaInterceptionProgress{Enabled: true, QuestPoints: map[uint16]int64{}}
	handleDivaTacticsPoint(s, &mhfpacket.MsgMhfGetUdTacticsPoint{AckHandle: 95})
	ack := readAck(t, s)
	total, ids := readDivaTacticsQuestPayload(t, ack.Payload, true)
	if ack.ErrorCode != 0 || total != 100 || ids[0] != 58043 {
		t.Fatalf("prayer UI lost prior total/IDs: total=%d IDs=%v ack=%+v", total, ids, ack)
	}
	next.StartTime = uint32(TimeAdjusted().Unix() - divaPhaseDuration - divaInterlude)
	repo.events = []DivaEvent{next}
	repo.rewardPeriods = []DivaEvent{old, next}
	handleDivaTacticsPoint(s, &mhfpacket.MsgMhfGetUdTacticsPoint{AckHandle: 96})
	ack = readAck(t, s)
	total, ids = readDivaTacticsQuestPayload(t, ack.Payload, true)
	if ack.ErrorCode != 0 || total != 0 || len(ids) != 255 {
		t.Fatalf("new period did not reset native state: total=%d len=%d", total, len(ids))
	}
	for _, id := range ids {
		if id != 0 {
			t.Fatal("previous completion survived into next interception")
		}
	}
}
