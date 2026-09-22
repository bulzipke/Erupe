package channelserver

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func divaGuildTestPrizes() []DivaPrize {
	var result []DivaPrize
	for i, r := range divaApprovedGuildRewards() {
		result = append(result, DivaPrize{ID: i + 1, Type: "guild", PointsReq: int(r.Threshold),
			ItemType: int(r.ItemType), ItemID: int(r.ItemID), Quantity: int(r.Quantity), GR: true})
	}
	return result
}

func TestDivaGuildRewardApprovedThresholds(t *testing.T) {
	bases := make(map[string]int)
	for _, row := range divaApprovedGuildRewards() {
		bases[row.Basis]++
	}
	if bases["round40-direct"] != 6 || bases["round35-38-inferred-85"] != 6 {
		t.Fatalf("direct and inferred reward provenance was merged: %+v", bases)
	}
	for _, tt := range []struct {
		areas uint32
		count int
	}{
		{0, 0}, {1, 0}, {2, 1}, {3, 2}, {5, 3}, {10, 6},
		{20, 7}, {22, 10}, {24, 11}, {26, 12}, {30, 12}, {40, 12},
	} {
		rows, err := eligibleDivaGuildRewards(divaGuildTestPrizes(), 1, tt.areas)
		if err != nil || len(rows) != tt.count {
			t.Fatalf("areas=%d: got %d, expected %d: %v", tt.areas, len(rows), tt.count, err)
		}
		for _, r := range rows {
			if r.RewardType != 7 || r.Threshold > tt.areas || r.NormaRepeat {
				t.Fatalf("invalid guild reward: %+v", r)
			}
		}
	}
	if rows, err := eligibleDivaGuildRewards(divaGuildTestPrizes(), 0, 1000); err != nil || len(rows) != 0 {
		t.Fatalf("GR-only original rewards were given to HR: %+v %v", rows, err)
	}
	for _, change := range []func(*DivaPrize){
		func(p *DivaPrize) { p.Quantity++ }, func(p *DivaPrize) { p.ItemID++ },
		func(p *DivaPrize) { p.PointsReq = 1 }, func(p *DivaPrize) { p.Repeatable = true },
		func(p *DivaPrize) { p.GR = false }, func(p *DivaPrize) { p.Type = "personal" },
	} {
		prizes := divaGuildTestPrizes()
		change(&prizes[0])
		if _, err := eligibleDivaGuildRewards(prizes, 1, 1000); err == nil {
			t.Fatal("unapproved original-table change accepted")
		}
	}
	prizes := divaGuildTestPrizes()
	if _, err := eligibleDivaGuildRewards(append(prizes, prizes[0]), 1, 1000); err == nil {
		t.Fatal("duplicate catalog row accepted")
	}
	// Stable keys do not depend on serial IDs or the order in which rows load.
	a, _ := eligibleDivaGuildRewards(prizes, 1, 1000)
	for i := range prizes {
		prizes[i].ID += 500
	}
	b, _ := eligibleDivaGuildRewards(prizes, 1, 1000)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("database row IDs changed entitlements")
	}
}

// Handler-only fake. SQL eligibility, membership locks and persistent save
// consumption are tested separately against the parent's isolated database.
type divaGuildRewardHandlerFake struct {
	*divaRewardHandlerFakeRepo
	guildOffers                     []DivaRewardOffer
	guildErr                        error
	guildQueries, guildClaims       int
	charID                          uint32
	mode                            int
	treasureQueries, treasureClaims int
}

func (r *divaGuildRewardHandlerFake) OfferDivaTreasureRewards(charID uint32, mode int) ([]DivaRewardOffer, error) {
	r.treasureQueries++
	r.charID, r.mode = charID, mode
	return r.guildOffers, r.guildErr
}

func (r *divaGuildRewardHandlerFake) PrepareDivaTreasureRewardClaims(charID uint32, ids []uint32, mode int) ([]DivaRewardOffer, error) {
	r.treasureClaims++
	return r.PrepareDivaGuildRewardClaims(charID, ids, mode)
}

func (r *divaGuildRewardHandlerFake) OfferDivaGuildRewards(charID uint32, mode int) ([]DivaRewardOffer, error) {
	r.guildQueries++
	r.charID, r.mode = charID, mode
	return r.guildOffers, r.guildErr
}
func (r *divaGuildRewardHandlerFake) PrepareDivaGuildRewardClaims(charID uint32, ids []uint32, mode int) ([]DivaRewardOffer, error) {
	r.guildClaims++
	r.charID, r.mode = charID, mode
	if r.guildErr != nil {
		return nil, r.guildErr
	}
	var result []DivaRewardOffer
	for _, id := range ids {
		found := false
		for _, offer := range r.guildOffers {
			if offer.ID == id {
				result = append(result, offer)
				found = true
				break
			}
		}
		if !found {
			return nil, errInvalidDivaReward
		}
	}
	return result, nil
}

func newDivaGuildRewardHandlerTest() (*Session, *divaGuildRewardHandlerFake) {
	event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Add(-time.Hour).Unix())}
	s, base := newDivaRewardHandlerTestSession(event)
	repo := &divaGuildRewardHandlerFake{divaRewardHandlerFakeRepo: base}
	for i, row := range divaApprovedGuildRewards() {
		repo.guildOffers = append(repo.guildOffers, DivaRewardOffer{ID: uint32(700 + i), EventID: 41,
			RewardType: 7, CatalogKey: row.Key, ItemType: row.ItemType, ItemID: row.ItemID, Quantity: row.Quantity})
	}
	s.server.divaRepo = repo
	return s, repo
}

func TestDivaGuildRewardAcquireDispatchAndStage(t *testing.T) {
	s, repo := newDivaGuildRewardHandlerTest()
	// The current schedule is prayer; the repository still selects the previous
	// real interception, exactly as for individual type-6 rewards.
	s.server.erupeConfig.DebugOptions.DivaOverride = 1
	handleDivaRewardAcquire(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 9, Unk0: 1, RewardType: 7})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, divaAvailableRewardPayload(repo.guildOffers)) ||
		repo.guildQueries != 1 || repo.charID != s.charID || repo.mode != 1 || repo.offerCalls != 0 || s.hasPendingDivaRewardClaims() {
		t.Fatalf("wrong guild query dispatch: %+v %+v", ack, repo)
	}
	ids := []uint32{700, 711} // One item and the original 3,000 GP reward.
	handleDivaRewardAcquire(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 10, RewardType: 7, ItemIDCount: 2, RewardIDs: ids})
	ack = readAck(t, s)
	if ack.ErrorCode != 0 || repo.guildClaims != 1 || repo.prepareCalls != 0 ||
		!reflect.DeepEqual(s.pendingDivaRewardClaims(7), ids[:1]) || !reflect.DeepEqual(s.pendingDivaRewardClaims(26), ids[1:]) {
		t.Fatalf("claim did not stage independent save types: %+v", ack)
	}
	handleDivaRewardAcquire(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 11, Unk0: 1, RewardType: 7})
	ack = readAck(t, s)
	if ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, []byte{0, 0}) || repo.guildQueries != 1 {
		t.Fatal("pending item/GP saves allowed another reward batch")
	}
}

func TestDivaGuildRewardAcquireFailsClosed(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Session, *divaGuildRewardHandlerFake, *mhfpacket.MsgMhfAcquireUdItem)
		fail   bool
	}{
		{"database error", func(_ *Session, r *divaGuildRewardHandlerFake, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.guildErr = errors.New("offline")
		}, true},
		{"oversize query", func(_ *Session, r *divaGuildRewardHandlerFake, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.guildOffers = make([]DivaRewardOffer, 33)
		}, true},
		{"foreign receipt", func(_ *Session, _ *divaGuildRewardHandlerFake, p *mhfpacket.MsgMhfAcquireUdItem) {
			p.Unk0 = 0
			p.ItemIDCount = 1
			p.RewardIDs = []uint32{999}
		}, true},
		{"old client", func(s *Session, _ *divaGuildRewardHandlerFake, _ *mhfpacket.MsgMhfAcquireUdItem) {
			s.server.erupeConfig.RealClientMode = cfg.G1
		}, false},
		{"disabled", func(s *Session, _ *divaGuildRewardHandlerFake, _ *mhfpacket.MsgMhfAcquireUdItem) {
			s.server.erupeConfig.DebugOptions.DivaOverride = 0
		}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, r := newDivaGuildRewardHandlerTest()
			pkt := &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 1, Unk0: 1, RewardType: 7}
			tt.change(s, r, pkt)
			handleDivaRewardAcquire(s, pkt)
			ack := readAck(t, s)
			if (ack.ErrorCode != 0) != tt.fail || s.hasPendingDivaRewardClaims() {
				t.Fatalf("unexpected ACK/staged claim: %+v", ack)
			}
		})
	}
}

func TestDivaTreasureRewardAcquireAndCatalog(t *testing.T) {
	keys := make(map[string]bool)
	for _, number := range []uint16{1, 2, 65535} {
		for _, coordinate := range []uint16{407, 106} {
			rows := divaCustomBranchTreasureRewards(number, coordinate)
			if len(rows) != 2 || rows[0].RewardType != 5 || rows[0].ItemType != 7 || rows[0].ItemID != 1026 || rows[0].Quantity != 5 ||
				rows[1].RewardType != 5 || rows[1].ItemType != 26 || rows[1].ItemID != 0 || rows[1].Quantity != 500 {
				t.Fatalf("wrong custom treasure: %+v", rows)
			}
			for _, r := range rows {
				if keys[r.Key] {
					t.Fatal("map/branch treasures share a key")
				}
				keys[r.Key] = true
			}
		}
	}
	if len(divaCustomBranchTreasureRewards(0, 407)) != 0 || len(divaCustomBranchTreasureRewards(1, 999)) != 0 {
		t.Fatal("unknown map/branch was rewarded")
	}
	s, r := newDivaGuildRewardHandlerTest()
	r.guildOffers = nil
	for i, row := range divaCustomBranchTreasureRewards(1, 407) {
		r.guildOffers = append(r.guildOffers, DivaRewardOffer{ID: 800 + uint32(i), EventID: 41, RewardType: 5, CatalogKey: row.Key, ItemType: row.ItemType, ItemID: row.ItemID, Quantity: row.Quantity})
	}
	handleDivaRewardAcquire(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 1, Unk0: 1, RewardType: 5})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, divaAvailableRewardPayload(r.guildOffers)) || r.treasureQueries != 1 || r.guildQueries != 0 || r.offerCalls != 0 {
		t.Fatalf("treasure selector did not reach dedicated repository: %+v", ack)
	}
	handleDivaRewardAcquire(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 2, RewardType: 5, ItemIDCount: 2, RewardIDs: []uint32{800, 801}})
	ack = readAck(t, s)
	if ack.ErrorCode != 0 || r.treasureClaims != 1 || r.prepareCalls != 0 ||
		!reflect.DeepEqual(s.pendingDivaRewardClaims(7), []uint32{800}) || !reflect.DeepEqual(s.pendingDivaRewardClaims(26), []uint32{801}) {
		t.Fatalf("treasure claim not staged for item and GP saves: %+v", ack)
	}
}

func TestDivaSpecialHallPeriodBoundaries(t *testing.T) {
	event := DivaEvent{ID: 1, StartTime: 10000000}
	start := int64(event.StartTime) + divaPhaseDuration + divaWeekDuration + divaInterlude
	end := int64(event.StartTime) + divaPhaseDuration + 2*divaWeekDuration
	for _, tt := range []struct {
		at   int64
		want bool
	}{{int64(event.StartTime), false}, {start - 1, false}, {start, true}, {end - 1, true}, {end, false}} {
		if got := divaSpecialHallPeriod(event, time.Unix(tt.at, 0)); got != tt.want {
			t.Fatalf("at=%d got=%v expected=%v", tt.at, got, tt.want)
		}
	}
	if divaSpecialHallPeriod(DivaEvent{}, time.Unix(start, 0)) {
		t.Fatal("zero event enabled special hall")
	}
}

type divaSpecialHallFake struct {
	mockDivaRepo
	earned bool
	err    error
	calls  int
	char   uint32
}

func (r *divaSpecialHallFake) GetDivaSpecialHall(char, guild uint32, _ time.Time) (bool, error) {
	r.calls++
	r.char = char
	if guild != 10 {
		return false, errors.New("unexpected guild lookup")
	}
	return r.earned, r.err
}

func TestInfoGuildDivaSpecialHallFlag(t *testing.T) {
	for _, tt := range []struct {
		name                           string
		mode                           int
		client                         cfg.Mode
		own, joined, applicant, earned bool
		err                            error
		want                           bool
	}{
		{"earned welcome", -1, cfg.ZZ, true, true, false, true, nil, true},
		{"forced welcome", 3, cfg.ZZ, true, true, false, true, nil, true},
		{"not earned", -1, cfg.ZZ, true, true, false, false, nil, false},
		{"prayer override", 1, cfg.ZZ, true, true, false, true, nil, false},
		{"interception override", 2, cfg.ZZ, true, true, false, true, nil, false},
		{"disabled", 0, cfg.ZZ, true, true, false, true, nil, false},
		{"old client", -1, cfg.G1, true, true, false, true, nil, false},
		{"foreign guild", -1, cfg.ZZ, false, true, false, true, nil, false},
		{"applicant", -1, cfg.ZZ, true, true, true, true, nil, false},
		{"not joined", -1, cfg.ZZ, true, false, false, true, nil, false},
		{"database offline", -1, cfg.ZZ, true, true, false, true, errors.New("offline"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := guildInfoServer()
			srv.erupeConfig.RealClientMode = tt.client
			srv.erupeConfig.DebugOptions.DivaOverride = tt.mode
			now := TimeAdjusted()
			member := &GuildMember{GuildID: 10, IsApplicant: tt.applicant}
			if tt.joined {
				member.JoinedAt = &now
			}
			if !tt.own {
				member.GuildID = 20
			}
			g := &Guild{ID: 10, Name: "Guild", Comment: "Comment", CreatedAt: now, RoomExpiry: now}
			g.LeaderName = "Leader"
			srv.guildRepo = &mockGuildRepo{guild: g, membership: member}
			r := &divaSpecialHallFake{mockDivaRepo: mockDivaRepo{events: []DivaEvent{{ID: 41, StartTime: uint32(now.Unix() - divaPhaseDuration - divaWeekDuration - divaInterlude - 60)}}}, earned: tt.earned, err: tt.err}
			srv.divaRepo = r
			s := createMockSession(56, srv)
			handleMsgMhfInfoGuild(s, &mhfpacket.MsgMhfInfoGuild{AckHandle: 1, GuildID: 10})
			ack := readAck(t, s)
			offset := 45 + len(g.Name) + len(g.Comment) + len(g.LeaderName)
			if ack.ErrorCode != 0 || len(ack.Payload) <= offset || (ack.Payload[offset] != 0) != tt.want {
				t.Fatalf("special hall flag at %d: %x", offset, ack.Payload)
			}
			if tt.want && (r.calls != 1 || r.char != 56) {
				t.Fatal("special hall lookup did not use authenticated character")
			}
		})
	}
}
