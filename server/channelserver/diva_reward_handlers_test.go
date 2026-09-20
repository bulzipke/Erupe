package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestDivaRewardPayloadGolden(t *testing.T) {
	daily := DivaRewardCatalogEntry{RewardType: 0, Threshold: 1, GR: true, ItemType: 7, ItemID: 0x3694, Quantity: 5}
	ranking := DivaRewardCatalogEntry{RewardType: 2, Lower: 1, Upper: 100, ItemType: 26, Quantity: 12000}
	rows := []DivaRewardCatalogEntry{daily, ranking, {RewardType: 1, ItemType: 7, ItemID: 1, Quantity: 999}}
	// u16 count + 15-byte row: item kind/id/qty, GR, upper, lower, day.
	wantDaily := []byte{0, 1, 7, 0x36, 0x94, 0, 5, 1, 0, 0, 3, 0xe7, 0, 0, 0, 1, 1}
	if got := divaDailyRewardPayload(rows); !bytes.Equal(got, wantDaily) {
		t.Fatalf("daily wire = %x, want %x", got, wantDaily)
	}
	// u16 count + 14-byte row: GP kind/id/qty, personal kind, upper, lower.
	wantRanking := []byte{0, 1, 26, 0, 0, 0x2e, 0xe0, 0, 0, 0, 0, 100, 0, 0, 0, 1}
	if got := divaRankingRewardPayload(rows); !bytes.Equal(got, wantRanking) {
		t.Fatalf("ranking wire = %x, want %x", got, wantRanking)
	}
	// Query offers use opaque entitlement IDs, not item IDs.
	wantOffer := []byte{0, 1, 0x11, 0x22, 0x33, 0x44, 7, 0x36, 0x94, 0, 5}
	if got := divaAvailableRewardPayload([]DivaRewardOffer{{ID: 0x11223344, ItemType: 7, ItemID: 0x3694, Quantity: 5}}); !bytes.Equal(got, wantOffer) {
		t.Fatalf("available reward wire = %x, want %x", got, wantOffer)
	}
	for name, payload := range map[string][]byte{
		"daily": divaDailyRewardPayload(nil), "ranking": divaRankingRewardPayload(nil), "available": divaAvailableRewardPayload(nil),
	} {
		if !bytes.Equal(payload, []byte{0, 0}) {
			t.Fatalf("%s empty wire = %x", name, payload)
		}
	}
}

func TestDivaRewardCatalogKeepsOriginalCategories(t *testing.T) {
	if len(diva40SongRewards) != 24 {
		t.Fatalf("directly established catalog size = %d, want 24", len(diva40SongRewards))
	}
	counts := make(map[uint8]int)
	keys := make(map[string]bool)
	for _, reward := range diva40SongRewards {
		counts[reward.RewardType]++
		if reward.Key == "" || keys[reward.Key] || reward.Basis != "round40-direct" {
			t.Fatalf("duplicate/missing key or unverified catalog entry: %+v", reward)
		}
		keys[reward.Key] = true
		if reward.RewardType != 0 && reward.RewardType != 2 {
			t.Fatalf("daily/personal-rank row remapped into another original category: %+v", reward)
		}
	}
	if counts[0] != 12 || counts[2] != 12 {
		t.Fatalf("wrong catalog categories: %v", counts)
	}
	for _, kind := range []uint8{4, 5, 6, 7} {
		if got := eligibleDivaSongRewards(kind, DivaRewardProgress{GR: 999, Points: 999999, ParticipationDays: 7, Rank: 1}); len(got) != 0 {
			t.Fatalf("unrestored category %d reused another category's rewards: %+v", kind, got)
		}
	}
}

func TestDivaRewardEligibility(t *testing.T) {
	for _, tt := range []struct {
		name     string
		progress DivaRewardProgress
		want     int
	}{
		{"HR cannot get GR daily", DivaRewardProgress{Points: 1, ParticipationDays: 7}, 0},
		{"no points", DivaRewardProgress{GR: 1, ParticipationDays: 7}, 0},
		{"negative points", DivaRewardProgress{GR: 1, Points: -1, ParticipationDays: 7}, 0},
		{"no participation day", DivaRewardProgress{GR: 1, Points: 1}, 0},
		{"day one", DivaRewardProgress{GR: 1, Points: 1, ParticipationDays: 1}, 1},
		{"day two", DivaRewardProgress{GR: 1, Points: 1, ParticipationDays: 2}, 2},
		{"day four", DivaRewardProgress{GR: 1, Points: 1, ParticipationDays: 4}, 4},
		{"day five bundles", DivaRewardProgress{GR: 1, Points: 1, ParticipationDays: 5}, 9},
		{"day six", DivaRewardProgress{GR: 999, Points: 1, ParticipationDays: 6}, 10},
		{"day seven", DivaRewardProgress{GR: 999, Points: 1, ParticipationDays: 7}, 12},
		{"after day seven", DivaRewardProgress{GR: 999, Points: 1, ParticipationDays: 8}, 12},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := eligibleDivaSongRewards(0, tt.progress)
			if len(got) != tt.want {
				t.Fatalf("eligible daily count = %d, want %d", len(got), tt.want)
			}
			for _, row := range got {
				if row.RewardType != 0 || !row.GR || row.Threshold > tt.progress.ParticipationDays {
					t.Fatalf("ineligible daily row included: %+v", row)
				}
			}
		})
	}
	for _, tt := range []struct {
		rank, lower, upper uint32
		gp                 uint16
	}{
		{0, 0, 0, 0}, {1, 1, 100, 12000}, {100, 1, 100, 12000},
		{101, 101, 1000, 6000}, {1000, 101, 1000, 6000},
		{1001, 1001, 10000, 3000}, {10000, 1001, 10000, 3000}, {10001, 0, 0, 0},
	} {
		t.Run(fmt.Sprintf("rank=%d", tt.rank), func(t *testing.T) {
			// Personal ranking has no GR-only condition or participation-day minimum.
			got := eligibleDivaSongRewards(2, DivaRewardProgress{Points: 1, Rank: tt.rank})
			if tt.gp == 0 {
				if len(got) != 0 {
					t.Fatalf("out-of-range rank got rewards: %+v", got)
				}
				return
			}
			if len(got) != 4 {
				t.Fatalf("rank group size = %d, want 4", len(got))
			}
			for _, row := range got {
				if row.RewardType != 2 || row.Lower != tt.lower || row.Upper != tt.upper {
					t.Fatalf("wrong ranking tier: %+v", row)
				}
			}
			if got[0].ItemType != 26 || got[0].ItemID != 0 || got[0].Quantity != tt.gp {
				t.Fatalf("GP reward changed category/value: %+v", got[0])
			}
			if got := eligibleDivaSongRewards(2, DivaRewardProgress{Rank: tt.rank}); len(got) != 0 {
				t.Fatal("zero-point character qualified for ranking rewards")
			}
		})
	}
}

func TestDivaRewardListHandlers(t *testing.T) {
	for _, zz := range []bool{false, true} {
		t.Run(fmt.Sprintf("ZZ=%t", zz), func(t *testing.T) {
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = cfg.Z2
			if zz {
				srv.erupeConfig.RealClientMode = cfg.ZZ
			}
			srv.divaRepo = nil // Static catalogs must not access a database.
			s := createMockSession(56, srv)
			handleMsgMhfGetUdDailyPresentList(s, &mhfpacket.MsgMhfGetUdDailyPresentList{AckHandle: 1})
			daily := readAck(t, s)
			handleMsgMhfGetUdRankingRewardList(s, &mhfpacket.MsgMhfGetUdRankingRewardList{AckHandle: 2})
			ranking := readAck(t, s)
			if daily.ErrorCode != 0 || ranking.ErrorCode != 0 || daily.AckHandle != 1 || ranking.AckHandle != 2 {
				t.Fatal("catalog handler returned an error or incorrect ACK handle")
			}
			if !zz {
				if !bytes.Equal(daily.Payload, []byte{0, 0}) || !bytes.Equal(ranking.Payload, []byte{0, 0}) {
					t.Fatal("ZZ catalog sent to older client")
				}
				return
			}
			if len(daily.Payload) != 2+26*15 || len(ranking.Payload) != 2+16*14 ||
				binary.BigEndian.Uint16(daily.Payload[:2]) != 26 || binary.BigEndian.Uint16(ranking.Payload[:2]) != 16 {
				t.Fatalf("catalog length/count mismatch: daily=%x rank=%x", daily.Payload, ranking.Payload)
			}
			if !bytes.Equal(daily.Payload[2:17], []byte{7, 0x36, 0x94, 0, 5, 1, 0, 0, 3, 0xe7, 0, 0, 0, 1, 1}) {
				t.Fatalf("first daily row differs from native golden: %x", daily.Payload[2:17])
			}
			if !bytes.Equal(ranking.Payload[2:16], []byte{26, 0, 0, 0x2e, 0xe0, 0, 0, 0, 0, 100, 0, 0, 0, 1}) {
				t.Fatalf("first ranking row differs from native golden: %x", ranking.Payload[2:16])
			}
		})
	}
}

// Independent mock-only reward repository. It records the arguments supplied
// by handlers; this is not a test of SQL ownership checks or save transactions.
type divaRewardHandlerFakeRepo struct {
	mockDivaRepo
	rotationCalls                                                   int
	rotationProgress                                                DivaRewardProgress
	progress                                                        DivaRewardProgress
	progressErr, offerErr, prepareErr                               error
	progressCalls, offerCalls, prepareCalls                         int
	progressChar, progressEvent, offerChar, offerEvent, prepareChar uint32
	progressStart, progressEnd                                      time.Time
	offerKind, prepareKind                                          uint8
	candidates                                                      []DivaRewardCatalogEntry
	offers, offerOverride                                           []DivaRewardOffer
	prepareIDs                                                      []uint32
}

var _ DivaRewardRepository = (*divaRewardHandlerFakeRepo)(nil)
var _ DivaPrayerRotationRepository = (*divaRewardHandlerFakeRepo)(nil)

func (r *divaRewardHandlerFakeRepo) OfferDivaPrayerRewards(charID, eventID uint32, progress DivaRewardProgress) ([]DivaRewardOffer, error) {
	r.rotationCalls++
	r.rotationProgress = progress
	// This fake checks handler dispatch only. Real rotation minting, batching,
	// rank checks and persistence are covered by the PostgreSQL repository tests.
	return r.OfferDivaRewards(charID, eventID, 1, eligibleDivaSongRewards(1, progress))
}

func (r *divaRewardHandlerFakeRepo) GetDivaRewardProgress(charID, eventID uint32, start, end time.Time) (DivaRewardProgress, error) {
	r.progressCalls++
	r.progressChar, r.progressEvent, r.progressStart, r.progressEnd = charID, eventID, start, end
	return r.progress, r.progressErr
}

func (r *divaRewardHandlerFakeRepo) OfferDivaRewards(charID, eventID uint32, kind uint8, rewards []DivaRewardCatalogEntry) ([]DivaRewardOffer, error) {
	r.offerCalls++
	r.offerChar, r.offerEvent, r.offerKind = charID, eventID, kind
	r.candidates = append([]DivaRewardCatalogEntry(nil), rewards...)
	if r.offerErr != nil {
		return nil, r.offerErr
	}
	if r.offerOverride != nil {
		r.offers = append([]DivaRewardOffer(nil), r.offerOverride...)
		return r.offers, nil
	}
	r.offers = nil
	for i, reward := range rewards {
		r.offers = append(r.offers, DivaRewardOffer{ID: 10000 + uint32(kind)*100 + uint32(i+1), EventID: eventID, RewardType: kind,
			CatalogKey: reward.Key, ItemType: reward.ItemType, ItemID: reward.ItemID, Quantity: reward.Quantity})
	}
	return r.offers, nil
}

func (r *divaRewardHandlerFakeRepo) PrepareDivaRewardClaims(charID uint32, kind uint8, ids []uint32) ([]DivaRewardOffer, error) {
	r.prepareCalls++
	r.prepareChar, r.prepareKind = charID, kind
	r.prepareIDs = append([]uint32(nil), ids...)
	if r.prepareErr != nil {
		return nil, r.prepareErr
	}
	var result []DivaRewardOffer
	for _, id := range ids {
		found := false
		for _, offer := range r.offers {
			if charID == r.offerChar && offer.ID == id && offer.RewardType == kind {
				result = append(result, offer)
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("foreign or unavailable reward entitlement")
		}
	}
	return result, nil
}

func newDivaRewardHandlerTestSession(event DivaEvent) (*Session, *divaRewardHandlerFakeRepo) {
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.erupeConfig.DebugOptions.DivaOverride = -1
	repo := &divaRewardHandlerFakeRepo{mockDivaRepo: mockDivaRepo{events: []DivaEvent{event}},
		progress: DivaRewardProgress{GR: 1, Points: 60, ParticipationDays: 5, Rank: 100}}
	srv.divaRepo = repo
	return createMockSession(56, srv), repo
}

func TestDivaRewardAcquireQueryThenStage(t *testing.T) {
	event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Add(-24 * time.Hour).Unix())}
	s, repo := newDivaRewardHandlerTestSession(event)
	before := TimeAdjusted()
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 7, Unk0: 1, RewardType: 0, ItemIDCount: 255})
	after := TimeAdjusted().Add(time.Nanosecond)
	query := readAck(t, s)
	if query.ErrorCode != 0 || len(query.Payload) != 2+9*9 || query.Payload[0] != 0 || query.Payload[1] != 9 {
		t.Fatalf("daily eligibility query failed: %+v", query)
	}
	if repo.progressCalls != 1 || repo.offerCalls != 1 || repo.prepareCalls != 0 || s.hasPendingDivaRewardClaims() {
		t.Fatal("query prepared or staged a claim, or skipped eligibility")
	}
	if repo.progressChar != 56 || repo.progressEvent != event.ID || repo.offerChar != 56 || repo.offerEvent != event.ID || repo.offerKind != 0 ||
		!repo.progressStart.Equal(time.Unix(int64(event.StartTime), 0)) || repo.progressEnd.Before(before) || repo.progressEnd.After(after) {
		t.Fatalf("wrong eligibility/offer scope: %+v", repo)
	}
	var ids []uint32
	for i, offer := range repo.offers {
		row := query.Payload[2+i*9 : 2+(i+1)*9]
		if binary.BigEndian.Uint32(row[:4]) != offer.ID || row[4] != offer.ItemType ||
			binary.BigEndian.Uint16(row[5:7]) != offer.ItemID || binary.BigEndian.Uint16(row[7:9]) != offer.Quantity {
			t.Fatalf("wrong entitlement wire row: %x", row)
		}
		ids = append(ids, offer.ID)
	}
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 8, RewardType: 0, ItemIDCount: uint8(len(ids)), RewardIDs: ids})
	claim := readAck(t, s)
	if claim.ErrorCode != 0 || claim.AckHandle != 8 || !bytes.Equal(claim.Payload, []byte{0, 0}) || !s.hasPendingDivaRewardClaims() {
		t.Fatalf("receipt did not stage rewards: %+v", claim)
	}
	if repo.prepareCalls != 1 || repo.prepareChar != 56 || repo.prepareKind != 0 || !reflect.DeepEqual(repo.prepareIDs, ids) {
		t.Fatal("receipt did not forward the same opaque IDs and character/category to the repository")
	}
	// A second availability query cannot re-offer pending receipts before their
	// corresponding character/mercenary saves finish. No saves occur in this test.
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 9, Unk0: 1, RewardType: 0})
	pending := readAck(t, s)
	if pending.ErrorCode != 0 || !bytes.Equal(pending.Payload, []byte{0, 0}) || repo.offerCalls != 1 || repo.progressCalls != 1 {
		t.Fatal("query bypassed the pending-reward guard")
	}
}

func TestDivaRewardRankingFinalization(t *testing.T) {
	for _, tt := range []struct {
		name  string
		age   int64
		phase int
		want  int
	}{
		{"during prayer", 3600, -1, 0},
		{"during tally interlude", divaPhaseDuration + 60, -1, 0},
		{"after final tally", divaPhaseDuration + divaInterlude + 60, -1, 4},
		{"forced prayer suppresses final ranking", divaPhaseDuration + divaInterlude + 60, 1, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Unix() - tt.age)}
			s, repo := newDivaRewardHandlerTestSession(event)
			s.server.erupeConfig.DebugOptions.DivaOverride = tt.phase
			repo.progress.GR, repo.progress.ParticipationDays = 0, 0
			handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 10, Unk0: 1, RewardType: 2})
			ack := readAck(t, s)
			if ack.ErrorCode != 0 || len(ack.Payload) != 2+tt.want*9 || int(ack.Payload[1]) != tt.want {
				t.Fatalf("wrong ranking eligibility response: %+v", ack)
			}
			if tt.want == 0 {
				if repo.progressCalls != 0 || repo.offerCalls != 0 {
					t.Fatal("unfinished ranking consulted or offered final rewards")
				}
				return
			}
			if repo.offerKind != 2 || repo.progressCalls != 1 || repo.offerCalls != 1 ||
				!repo.progressEnd.Equal(time.Unix(int64(event.StartTime)+divaPhaseDuration, 0)) {
				t.Fatal("ranking eligibility did not use original kind 2 and prayer-end cutoff")
			}
			if s.hasPendingDivaRewardClaims() || repo.prepareCalls != 0 {
				t.Fatal("ranking query staged or consumed an entitlement")
			}
		})
	}
}

func TestDivaRewardAcquireGuards(t *testing.T) {
	active := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Add(-24 * time.Hour).Unix())}
	for _, tt := range []struct {
		name     string
		change   func(*Session, *divaRewardHandlerFakeRepo, *mhfpacket.MsgMhfAcquireUdItem)
		wantFail bool
	}{
		{"invalid query flag", func(_ *Session, _ *divaRewardHandlerFakeRepo, p *mhfpacket.MsgMhfAcquireUdItem) { p.Unk0 = 2 }, true},
		{"invalid category", func(_ *Session, _ *divaRewardHandlerFakeRepo, p *mhfpacket.MsgMhfAcquireUdItem) { p.RewardType = 8 }, true},
		{"query cannot contain IDs", func(_ *Session, _ *divaRewardHandlerFakeRepo, p *mhfpacket.MsgMhfAcquireUdItem) {
			p.RewardIDs = []uint32{1}
		}, true},
		{"33 claims rejected", func(_ *Session, _ *divaRewardHandlerFakeRepo, p *mhfpacket.MsgMhfAcquireUdItem) {
			p.Unk0 = 0
			p.ItemIDCount = 33
			p.RewardIDs = make([]uint32, 33)
		}, true},
		{"receipt count mismatch", func(_ *Session, _ *divaRewardHandlerFakeRepo, p *mhfpacket.MsgMhfAcquireUdItem) {
			p.Unk0 = 0
			p.ItemIDCount = 2
			p.RewardIDs = []uint32{1}
		}, true},
		{"old client query empty", func(s *Session, _ *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) {
			s.server.erupeConfig.RealClientMode = cfg.Z2
		}, false},
		{"no character", func(s *Session, _ *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) { s.charID = 0 }, false},
		{"disabled event", func(s *Session, _ *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) {
			s.server.erupeConfig.DebugOptions.DivaOverride = 0
		}, false},
		{"repository lacks rewards API", func(s *Session, _ *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) {
			s.server.divaRepo = &mockDivaRepo{}
		}, true},
		{"missing event", func(_ *Session, r *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) { r.events = nil }, false},
		{"future event", func(_ *Session, r *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.events[0].StartTime = uint32(TimeAdjusted().Add(time.Hour).Unix())
		}, false},
		{"expired event", func(_ *Session, r *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.events[0].StartTime = uint32(TimeAdjusted().Unix() - divaTotalLifespan - 60)
		}, false},
		{"event query failure", func(_ *Session, r *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.eventsErr = errors.New("event offline")
		}, true},
		{"progress query failure", func(_ *Session, r *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.progressErr = errors.New("progress offline")
		}, true},
		{"offer query failure", func(_ *Session, r *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.offerErr = errors.New("offer offline")
		}, true},
		{"repository 33 offers rejected", func(_ *Session, r *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.offerOverride = make([]DivaRewardOffer, 33)
		}, true},
		{"zero points", func(_ *Session, r *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.progress.Points = 0
		}, false},
		{"HR daily empty", func(_ *Session, r *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) { r.progress.GR = 0 }, false},
		{"no participation", func(_ *Session, r *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.progress.ParticipationDays = 0
		}, false},
		{"empty claim is a no-op", func(_ *Session, _ *divaRewardHandlerFakeRepo, p *mhfpacket.MsgMhfAcquireUdItem) { p.Unk0 = 0 }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, repo := newDivaRewardHandlerTestSession(active)
			pkt := &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 11, Unk0: 1, RewardType: 0}
			tt.change(s, repo, pkt)
			handleMsgMhfAcquireUdItem(s, pkt)
			ack := readAck(t, s)
			if (ack.ErrorCode != 0) != tt.wantFail || ack.AckHandle != 11 {
				t.Fatalf("wrong guard status: %+v", ack)
			}
			if tt.wantFail {
				if len(ack.Payload) != 0 {
					t.Fatal("error leaked a partial reward payload")
				}
			} else if !bytes.Equal(ack.Payload, []byte{0, 0}) {
				t.Fatalf("ineligible query is not empty: %x", ack.Payload)
			}
			if s.hasPendingDivaRewardClaims() || repo.prepareCalls != 0 {
				t.Fatal("guard failure/empty query prepared a receipt")
			}
		})
	}
	for _, kind := range []uint8{4, 5, 7} {
		s, repo := newDivaRewardHandlerTestSession(active)
		handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 12, Unk0: 1, RewardType: kind})
		ack := readAck(t, s)
		if ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, []byte{0, 0}) || repo.progressCalls != 0 || repo.offerCalls != 0 {
			t.Fatalf("unrestored category %d was remapped or queried: %+v", kind, ack)
		}
	}
}

func TestDivaRewardAcquireRejectsForeignReceipts(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Session, *divaRewardHandlerFakeRepo, *mhfpacket.MsgMhfAcquireUdItem)
	}{
		{"unknown ID", func(_ *Session, _ *divaRewardHandlerFakeRepo, p *mhfpacket.MsgMhfAcquireUdItem) {
			p.RewardIDs[0] = 999999
		}},
		{"other character", func(s *Session, _ *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) { s.charID = 57 }},
		{"other category", func(_ *Session, _ *divaRewardHandlerFakeRepo, p *mhfpacket.MsgMhfAcquireUdItem) { p.RewardType = 2 }},
		{"prepare repository error", func(_ *Session, r *divaRewardHandlerFakeRepo, _ *mhfpacket.MsgMhfAcquireUdItem) {
			r.prepareErr = errors.New("prepare offline")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Add(-24 * time.Hour).Unix())}
			s, repo := newDivaRewardHandlerTestSession(event)
			handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 13, Unk0: 1, RewardType: 0})
			if ack := readAck(t, s); ack.ErrorCode != 0 {
				t.Fatal("fixture offer failed")
			}
			pkt := &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 14, RewardType: 0, ItemIDCount: 1, RewardIDs: []uint32{repo.offers[0].ID}}
			tt.change(s, repo, pkt)
			handleMsgMhfAcquireUdItem(s, pkt)
			ack := readAck(t, s)
			if ack.ErrorCode == 0 || len(ack.Payload) != 0 || repo.prepareCalls != 1 || s.hasPendingDivaRewardClaims() {
				t.Fatalf("repository rejection did not prevent staging: %+v", ack)
			}
		})
	}
}

func TestDivaRewardAcquireMaximumBatch(t *testing.T) {
	event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Add(-24 * time.Hour).Unix())}
	s, repo := newDivaRewardHandlerTestSession(event)
	var ids []uint32
	for i := 0; i < 32; i++ {
		id := uint32(400 + i)
		ids = append(ids, id)
		repo.offerOverride = append(repo.offerOverride, DivaRewardOffer{ID: id, EventID: event.ID, RewardType: 0,
			CatalogKey: fmt.Sprintf("fixture-%d", i), ItemType: 7, ItemID: 0x3694, Quantity: 1})
	}
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 15, Unk0: 1, RewardType: 0})
	query := readAck(t, s)
	if query.ErrorCode != 0 || len(query.Payload) != 290 || query.Payload[1] != 32 || s.hasPendingDivaRewardClaims() {
		t.Fatalf("maximum native offer batch not sent: %+v", query)
	}
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 16, RewardType: 0, ItemIDCount: 32, RewardIDs: ids})
	claim := readAck(t, s)
	if claim.ErrorCode != 0 || !reflect.DeepEqual(s.pendingDivaRewardClaims(7), ids) || repo.prepareCalls != 1 {
		t.Fatalf("maximum native claim batch not staged: %+v", claim)
	}
}

func TestDivaRewardRankingStagesItemsAndGPSeparately(t *testing.T) {
	event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Unix() - divaPhaseDuration - divaInterlude - 60)}
	s, repo := newDivaRewardHandlerTestSession(event)
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 17, Unk0: 1, RewardType: 2})
	query := readAck(t, s)
	if query.ErrorCode != 0 || len(repo.offers) != 4 {
		t.Fatal("ranking offer fixture failed")
	}
	var all, items, gp []uint32
	for _, offer := range repo.offers {
		all = append(all, offer.ID)
		if offer.ItemType == 7 {
			items = append(items, offer.ID)
		} else if offer.ItemType == 26 {
			gp = append(gp, offer.ID)
		}
	}
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 18, RewardType: 2, ItemIDCount: 4, RewardIDs: all})
	claim := readAck(t, s)
	if claim.ErrorCode != 0 || repo.prepareKind != 2 || len(items) != 3 || len(gp) != 1 ||
		!reflect.DeepEqual(s.pendingDivaRewardClaims(7), items) || !reflect.DeepEqual(s.pendingDivaRewardClaims(26), gp) {
		t.Fatalf("ranking receipt did not retain separate item/GP save snapshots: %+v", claim)
	}
	// Pending rewards suppress queries across categories, not just their own kind.
	handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 19, Unk0: 1, RewardType: 0})
	if ack := readAck(t, s); ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, []byte{0, 0}) || repo.offerCalls != 1 {
		t.Fatal("another category bypassed pending ranking reward saves")
	}
}

func TestDivaRewardReceiptRequiresSupportedSession(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Session)
	}{
		{"old client", func(s *Session) { s.server.erupeConfig.RealClientMode = cfg.Z2 }},
		{"no character", func(s *Session) { s.charID = 0 }},
		{"disabled event", func(s *Session) { s.server.erupeConfig.DebugOptions.DivaOverride = 0 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Add(-24 * time.Hour).Unix())}
			s, repo := newDivaRewardHandlerTestSession(event)
			tt.change(s)
			handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 20, RewardType: 0, ItemIDCount: 1, RewardIDs: []uint32{10001}})
			if ack := readAck(t, s); ack.ErrorCode == 0 || len(ack.Payload) != 0 || repo.prepareCalls != 0 || s.hasPendingDivaRewardClaims() {
				t.Fatalf("unsupported session reached claim preparation: %+v", ack)
			}
		})
	}
}
