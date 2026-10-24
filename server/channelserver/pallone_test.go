package channelserver

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/mhfpallone"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

type mockPalloneRepo struct {
	event           *PalloneEvent
	routes          []PalloneRoute
	err             error
	scores          []mhfpallone.Score
	personal        []mhfpallone.PersonalRanking
	guilds          []mhfpallone.GuildRanking
	ranks           []mhfpallone.RankScore
	settled         int
	eventID         int32
	charID, guildID uint32
	key             *int32
	limit           int
	receipt         PalloneRunReceipt
	submission      PalloneSubmission
}

func (m *mockPalloneRepo) RegisterPalloneEvent(PalloneEvent, []PalloneRoute) error { return m.err }
func (m *mockPalloneRepo) GetPalloneEvent(id int32) (*PalloneEvent, error) {
	m.eventID = id
	return m.event, m.err
}
func (m *mockPalloneRepo) GetPalloneRoutes(id int32) ([]PalloneRoute, error) {
	m.eventID = id
	return m.routes, m.err
}
func (m *mockPalloneRepo) SettlePalloneResult(r PalloneRunReceipt, v PalloneSubmission, _ time.Time) (PalloneSettlement, error) {
	m.settled++
	m.receipt = r
	m.submission = v
	return PalloneSettlement{}, m.err
}
func (m *mockPalloneRepo) GetPalloneScores(id int32, c uint32) ([]mhfpallone.Score, error) {
	m.eventID = id
	m.charID = c
	return m.scores, m.err
}
func (m *mockPalloneRepo) GetPallonePersonalRanking(id int32, k *int32, g uint32, l int) ([]mhfpallone.PersonalRanking, error) {
	m.eventID = id
	m.key = k
	m.guildID = g
	m.limit = l
	return m.personal, m.err
}
func (m *mockPalloneRepo) GetPalloneGuildRanking(id int32, k *int32) ([]mhfpallone.GuildRanking, error) {
	m.eventID = id
	m.key = k
	return m.guilds, m.err
}
func (m *mockPalloneRepo) GetPalloneRanks(id int32, c, g uint32) ([]mhfpallone.RankScore, error) {
	m.eventID = id
	m.charID = c
	m.guildID = g
	return m.ranks, m.err
}

func palloneHandlerSession() (*Session, *mockPalloneRepo) {
	srv := createMockServer()
	now := TimeAdjusted()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.erupeConfig.EarthStatus = 11
	srv.erupeConfig.EarthID = 41
	r := &mockPalloneRepo{event: &PalloneEvent{ID: 41, Start: now.Add(-time.Hour), End: now.Add(time.Hour), ClaimUntil: now.Add(24 * time.Hour)}}
	srv.palloneRepo = r
	srv.palloneService = NewPalloneService(r)
	return createMockSession(100, srv), r
}

func TestPalloneNativeReadHandlers(t *testing.T) {
	s, r := palloneHandlerSession()
	r.scores = []mhfpallone.Score{{RouteKey: 7, Total: 3500, Best: 1000, Premium: 500}}
	handleMsgMhfCaravanMyScore(s, &mhfpacket.MsgMhfCaravanMyScore{AckHandle: 10, Unk0: 1})
	a := readAck(t, s)
	if a.ErrorCode != 0 || !a.IsBufferResponse || len(a.Payload) != 32 || binary.BigEndian.Uint32(a.Payload[16:]) != 7 || binary.BigEndian.Uint32(a.Payload[24:]) != 1000 || r.charID != 100 {
		t.Fatalf("score reply = %+v", a)
	}
	r.ranks = []mhfpallone.RankScore{{Key: 7, Rank: 2, Score: 1000}}
	handleMsgMhfCaravanMyRank(s, &mhfpacket.MsgMhfCaravanMyRank{AckHandle: 11, Unk1: 2, Unk2: 900})
	a = readAck(t, s)
	if a.ErrorCode != 0 || len(a.Payload) != 28 || binary.BigEndian.Uint32(a.Payload[20:]) != 2 || r.guildID != 900 {
		t.Fatalf("my-rank reply = %+v", a)
	}
	for _, selector := range []uint32{1, 2, 3, 4, 5} {
		t.Run(string(rune('0'+selector)), func(t *testing.T) {
			s, r := palloneHandlerSession()
			r.personal = []mhfpallone.PersonalRanking{{Score: 111, Name: "Hunter"}}
			r.guilds = []mhfpallone.GuildRanking{{Score: 222, GuildID: 900, Name: "Guild"}}
			p := &mhfpacket.MsgMhfCaravanRanking{AckHandle: 12, Unk1: selector}
			if selector == 1 || selector == 3 {
				p.Unk2 = 7
			}
			if selector == 5 {
				p.Unk2 = 900
			}
			handleMsgMhfCaravanRanking(s, p)
			a := readAck(t, s)
			if a.ErrorCode != 0 || !a.IsBufferResponse || binary.BigEndian.Uint32(a.Payload) != 41 {
				t.Fatalf("ranking ACK = %+v", a)
			}
			if selector == 3 || selector == 4 {
				if len(a.Payload) != 50 || binary.BigEndian.Uint32(a.Payload[16:]) != 222 || binary.BigEndian.Uint32(a.Payload[20:]) != 900 {
					t.Fatalf("guild row = %x", a.Payload)
				}
			} else {
				if len(a.Payload) != 34 || binary.BigEndian.Uint32(a.Payload[16:]) != 111 {
					t.Fatalf("personal row = %x", a.Payload)
				}
			}
			if (selector == 1 || selector == 3) && (r.key == nil || *r.key != 7) {
				t.Fatal("lost route selector")
			}
			if selector == 5 && (r.limit != 5 || r.guildID != 900 || r.key != nil) {
				t.Fatal("guild contributor query was not limited to five")
			}
		})
	}
}

func TestPalloneSubmissionRequiresServerReceipt(t *testing.T) {
	s, r := palloneHandlerSession()
	now := TimeAdjusted()
	p := &mhfpacket.MsgMhfCaravanMyScore{AckHandle: 99, Unk0: 2, Unk1: 3, Unk2: 7, Unk3: 1000, Unk4: 900, Unk5: 500, Unk6: 2500}
	handleMsgMhfCaravanMyScore(s, p)
	a := readAck(t, s)
	if a.ErrorCode == 0 || a.IsBufferResponse || r.settled != 0 {
		t.Fatal("client score packet issued its own receipt")
	}
	s.palloneReceipt = &PalloneRunReceipt{EventID: 41, CharID: 100, RouteKey: 7, RunID: strings.Repeat("a", 32), StartedAt: now.Add(-10 * time.Minute), CompletedAt: now.Add(-time.Minute), FinalQuestID: 27365}
	handleMsgMhfCaravanMyScore(s, p)
	a = readAck(t, s)
	if a.ErrorCode != 0 || a.IsBufferResponse || len(a.Payload) != 4 || r.settled != 1 || r.submission.BaseScore != 1000 || r.submission.GuildID != 900 || r.submission.ItemBonus != 2500 {
		t.Fatalf("qualified submission = %+v / %+v", a, r)
	}
	r.err = errors.New("write failure")
	handleMsgMhfCaravanMyScore(s, p)
	a = readAck(t, s)
	if a.ErrorCode == 0 || a.IsBufferResponse || s.palloneReceipt == nil {
		t.Fatal("failed write consumed the retry receipt or acknowledged success")
	}
	s.palloneReceipt.CharID = 101
	handleMsgMhfCaravanMyScore(s, p)
	a = readAck(t, s)
	if a.ErrorCode == 0 || r.settled != 2 {
		t.Fatal("accepted another character's receipt")
	}
}

func TestPalloneInvalidRequestsAndReadErrors(t *testing.T) {
	s, r := palloneHandlerSession()
	r.err = errors.New("read failure")
	handleMsgMhfCaravanMyScore(s, &mhfpacket.MsgMhfCaravanMyScore{AckHandle: 1, Unk0: 1})
	if a := readAck(t, s); a.ErrorCode == 0 || !a.IsBufferResponse {
		t.Fatal("database error became a successful empty read")
	}
	r.err = nil
	r.personal = make([]mhfpallone.PersonalRanking, 6)
	handleMsgMhfCaravanRanking(s, &mhfpacket.MsgMhfCaravanRanking{AckHandle: 2, Unk1: 5, Unk2: 900})
	if a := readAck(t, s); a.ErrorCode == 0 {
		t.Fatal("overflowed the native five-contributor buffer")
	}
	for _, p := range []*mhfpacket.MsgMhfCaravanRanking{{Unk1: 1}, {Unk1: 2, Unk2: 1}, {Unk1: 4, Unk2: 1}, {Unk1: 5}, {Unk1: 6}, {Unk0: 1, Unk1: 2}} {
		handleMsgMhfCaravanRanking(s, p)
		if a := readAck(t, s); a.ErrorCode == 0 {
			t.Fatalf("accepted invalid ranking selector %+v", p)
		}
	}
}

func TestPalloneInactiveAndTowerSelectionRetainEmptyReplies(t *testing.T) {
	for _, kind := range []string{"tower", "other phase", "other client", "future", "expired", "unregistered"} {
		t.Run(kind, func(t *testing.T) {
			s, r := palloneHandlerSession()
			now := TimeAdjusted()
			switch kind {
			case "tower":
				s.server.erupeConfig.TowerRotation.Enabled = true
			case "other phase":
				s.server.erupeConfig.EarthStatus = 21
			case "other client":
				s.server.erupeConfig.RealClientMode = cfg.G10
			case "future":
				r.event.Start = now.Add(time.Hour)
			case "expired":
				r.event.ClaimUntil = now.Add(-time.Hour)
			case "unregistered":
				r.event = nil
			}
			handleMsgMhfCaravanMyScore(s, &mhfpacket.MsgMhfCaravanMyScore{AckHandle: 1, Unk0: 1})
			a := readAck(t, s)
			if a.ErrorCode != 0 || !a.IsBufferResponse || len(a.Payload) != 16 || binary.BigEndian.Uint32(a.Payload[12:]) != 0 {
				t.Fatalf("legacy empty reply changed: %+v", a)
			}
		})
	}
}

func TestPalloneServiceRejectsInvalidValuesBeforeRepository(t *testing.T) {
	now := TimeAdjusted()
	valid := PalloneRunReceipt{EventID: 41, CharID: 100, RouteKey: 7, RunID: strings.Repeat("a", 32), StartedAt: now.Add(-time.Hour), CompletedAt: now.Add(-time.Minute), FinalQuestID: 27365}
	for _, test := range []struct {
		receipt func(*PalloneRunReceipt)
		value   PalloneSubmission
	}{
		{nil, PalloneSubmission{BaseScore: -1}},
		{nil, PalloneSubmission{BaseScore: 1, PremiumBonus: -1}},
		{nil, PalloneSubmission{BaseScore: 1, ItemBonus: -1}},
		{nil, PalloneSubmission{BaseScore: math.MaxInt32, PremiumBonus: 1}},
		{func(r *PalloneRunReceipt) { r.CharID = 101 }, PalloneSubmission{BaseScore: 1}},
		{func(r *PalloneRunReceipt) { r.RunID = "client-value" }, PalloneSubmission{BaseScore: 1}},
		{func(r *PalloneRunReceipt) { r.RouteKey = 0 }, PalloneSubmission{BaseScore: 1}},
		{func(r *PalloneRunReceipt) { r.CompletedAt = now.Add(time.Hour) }, PalloneSubmission{BaseScore: 1}},
	} {
		r := &mockPalloneRepo{}
		receipt := valid
		if test.receipt != nil {
			test.receipt(&receipt)
		}
		if _, err := NewPalloneService(r).Submit(100, receipt, test.value, now); !errors.Is(err, ErrPalloneInvalid) || r.settled != 0 {
			t.Fatalf("invalid value reached repository: %+v / %v", test, err)
		}
	}
}

func TestPalloneSelectedEventDatesAndPhases(t *testing.T) {
	for _, kind := range []string{"active", "rewards", "future", "expired"} {
		t.Run(kind, func(t *testing.T) {
			s, r := palloneHandlerSession()
			now := TimeAdjusted()
			s.server.erupeConfig.EarthMonsters = []int32{0, 0, 0, 0}
			want := uint32(11)
			switch kind {
			case "rewards":
				r.event.End = now.Add(-time.Minute)
				want = 12
			case "future":
				r.event.Start = now.Add(time.Minute)
				want = 0
			case "expired":
				r.event.Start = now.Add(-3 * time.Hour)
				r.event.End = now.Add(-2 * time.Hour)
				r.event.ClaimUntil = now.Add(-time.Hour)
				want = 0
			}
			handleMsgMhfGetEarthStatus(s, &mhfpacket.MsgMhfGetEarthStatus{AckHandle: 33})
			a := readAck(t, s)
			if a.ErrorCode != 0 || len(a.Payload) != 32 || binary.BigEndian.Uint32(a.Payload[8:]) != want || binary.BigEndian.Uint32(a.Payload[12:]) != 41 {
				t.Fatalf("phase %s: %+v", kind, a)
			}
			start, end := r.event.Start, r.event.End
			if want == 12 {
				start, end = r.event.End, r.event.ClaimUntil
			}
			if binary.BigEndian.Uint32(a.Payload) != uint32(start.Unix()) || binary.BigEndian.Uint32(a.Payload[4:]) != uint32(end.Unix()) {
				t.Fatal("Pallone dates were replaced with Tower/week boundaries")
			}
		})
	}
}

func TestPalloneGuildContributorIDRetainsAllWireBits(t *testing.T) {
	s, r := palloneHandlerSession()
	handleMsgMhfCaravanRanking(s, &mhfpacket.MsgMhfCaravanRanking{Unk1: 5, Unk2: math.MinInt32})
	a := readAck(t, s)
	if a.ErrorCode != 0 || r.guildID != uint32(1<<31) || r.limit != 5 {
		t.Fatalf("uint32 guild ID was narrowed: %+v / %d", a, r.guildID)
	}
}

func TestPalloneEventPlanBounds(t *testing.T) {
	now := TimeAdjusted()
	event := PalloneEvent{ID: 41, Start: now, End: now.Add(time.Hour), ClaimUntil: now.Add(24 * time.Hour)}
	valid := []PalloneRoute{{Key: 1, Star: 0, QuestIDs: []uint16{27438, 27365}}}
	if err := validatePalloneEvent(event, valid); err != nil {
		t.Fatal(err)
	}
	for _, routes := range [][]PalloneRoute{
		nil,
		{{Key: 0, QuestIDs: []uint16{27365}}},
		{{Key: 1, Star: 11, QuestIDs: []uint16{27365}}},
		{{Key: 1, QuestIDs: []uint16{0}}},
		{{Key: 1, QuestIDs: make([]uint16, 12)}},
		{{Key: 1, QuestIDs: []uint16{27365}}, {Key: 1, QuestIDs: []uint16{27438}}},
	} {
		if err := validatePalloneEvent(event, routes); !errors.Is(err, ErrPalloneInvalid) {
			t.Fatalf("invalid route plan accepted: %+v", routes)
		}
	}
	routes := make([]PalloneRoute, 13)
	for i := range routes {
		routes[i] = PalloneRoute{Key: int32(i + 1), QuestIDs: []uint16{27365}}
	}
	if err := validatePalloneEvent(event, routes); !errors.Is(err, ErrPalloneInvalid) {
		t.Fatal("overflowed native twelve-route star cache")
	}
	for _, bad := range []PalloneEvent{{ID: 0, Start: event.Start, End: event.End, ClaimUntil: event.ClaimUntil}, {ID: 41, Start: event.Start, End: event.Start, ClaimUntil: event.ClaimUntil}, {ID: 41, Start: event.Start, End: event.End, ClaimUntil: event.End}} {
		if err := validatePalloneEvent(bad, valid); !errors.Is(err, ErrPalloneInvalid) {
			t.Fatalf("invalid period accepted: %+v", bad)
		}
	}
}
