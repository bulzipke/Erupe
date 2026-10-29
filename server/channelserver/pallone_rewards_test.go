package channelserver

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
	"time"

	"erupe-ce/network/mhfpacket"
)

func TestPalloneRewardCatalogProvenanceAndEligibility(t *testing.T) {
	c, err := loadPalloneRewardCatalog(palloneRewardJSON)
	if err != nil {
		t.Fatal(err)
	}
	if c.ReferenceRound != 26 || len(c.Rules) < 100 {
		t.Fatal("incomplete reconstruction catalog")
	}
	p := PalloneRewardProgress{Score: 160000, Cleared: map[int32]bool{7: true}, Routes: []PalloneRoute{{Key: 7, Star: 0}, {Key: 8, Star: 0}}}
	rows := palloneEligibleRewards(c, p, true)
	gp, wisdom := false, false
	for _, r := range rows {
		if r.Key == "reach-gp-160000" && r.Quantity == 600 {
			gp = true
		}
		if r.Key == "reach-wisdom-160000" && r.Quantity == 10 {
			wisdom = true
		}
		if r.PresentType < 3301 || r.Key == "conquer-gp-0" {
			t.Fatalf("unearned or previous-round reward: %+v", r)
		}
	}
	if !gp || !wisdom {
		t.Fatal("earlier exact table with final-round x2 modifier lost")
	}
	p.Cleared[8] = true
	if !palloneConquered(p, 0) || palloneConquered(p, 1) {
		t.Fatal("conquered absent/uncompleted star")
	}
	p.Score = 815000
	p.RepeatThrough = 800000
	rows = palloneEligibleRewards(c, p, true)
	var repeat PalloneRewardOffer
	for _, r := range rows {
		if r.Key == "repeat:800000:815000" {
			repeat = r
		}
	}
	if repeat.Quantity != 3000 {
		t.Fatalf("repeat GP steps: %+v", repeat)
	}
	p.RepeatThrough = 815000
	p.Score = 820000
	for _, r := range palloneEligibleRewards(c, p, true) {
		if r.Key == "repeat:815000:820000" && r.Quantity != 1000 {
			t.Fatal("repeat overlap")
		}
	}
	old := PalloneRewardProgress{Ranks: []PalloneRewardRank{{Kind: 1, Rank: 1}, {Kind: 2, RouteKey: 7, Rank: 2}, {Kind: 3, Rank: 1}, {Kind: 4, RouteKey: 7, Rank: 1}}, GuildPoints: 22192, Contribution: 1520, ContributionRank: 1}
	found := map[uint32]bool{}
	for _, r := range palloneEligibleRewards(c, old, false) {
		if r.PresentType >= 3301 {
			t.Fatal("previous challenge leaked")
		}
		found[r.PresentType] = true
	}
	for _, typ := range []uint32{3201, 3202, 3203, 3204} {
		if !found[typ] {
			t.Fatalf("missing rank category %d", typ)
		}
	}
	old.Contribution = 1519
	for _, r := range palloneEligibleRewards(c, old, false) {
		if r.Key == "guild-level-ticket-1" {
			t.Fatal("guild minimum bypassed")
		}
	}
}

type palloneRunMemory struct {
	*mockPalloneRepo
	qualified  *PalloneRunReceipt
	submission *PalloneSubmission
	writeError error
}

func (r *palloneRunMemory) SavePalloneRunReport(receipt PalloneRunReceipt, v *PalloneSubmission, qualified bool, _ time.Time) (*PalloneReadyRun, error) {
	if r.writeError != nil {
		return nil, r.writeError
	}
	if qualified {
		copy := receipt
		r.qualified = &copy
	}
	if v != nil {
		copy := *v
		r.submission = &copy
	}
	if r.qualified != nil && r.submission != nil {
		return &PalloneReadyRun{Receipt: *r.qualified, Submission: *r.submission}, nil
	}
	return nil, nil
}
func (r *palloneRunMemory) ReadyPalloneRuns(time.Time) ([]PalloneReadyRun, error) { return nil, nil }
func (r *palloneRunMemory) RejectPalloneRunReport(uint32, string, string, time.Time) error {
	return nil
}

func palloneResultFixture(qid uint16, code byte) []byte {
	b := make([]byte, killLogHeaderSize+killLogMonsterCount)
	binary.LittleEndian.PutUint16(b, qid)
	b[questResultCodeOffset] = code
	binary.LittleEndian.PutUint32(b[questElapsedFramesOffset:], 300)
	return b
}

func TestPalloneNativeRunRequiresOrderedStagesAndSuccessfulResult(t *testing.T) {
	for _, scoreFirst := range []bool{false, true} {
		t.Run(map[bool]string{true: "score-before-result", false: "result-before-score"}[scoreFirst], func(t *testing.T) {
			s, base := palloneHandlerSession()
			r := &palloneRunMemory{mockPalloneRepo: base}
			s.server.palloneRepo = r
			s.server.palloneService = NewPalloneService(r)
			now := TimeAdjusted()
			plan := &palloneDeparturePlan{Event: *base.event, Routes: []PalloneRoute{{Key: 7, QuestIDs: []uint16{27438, 27479, 27348, 27365}}}}
			packet := &mhfpacket.MsgMhfCaravanMyScore{Unk0: 2, Unk1: 3, Unk2: 7, Unk3: 1000}
			if err := s.submitPalloneRunScore(packet, now); err == nil {
				t.Fatal("score authorized itself")
			}
			for i, q := range plan.Routes[0].QuestIDs {
				s.capturePalloneStage(q, "Qs1", now.Add(time.Duration(i)*time.Second), plan)
			}
			if s.palloneRun == nil || s.palloneRun.Stage != 3 {
				t.Fatal("no final-stage admission")
			}
			at := now.Add(time.Minute)
			if scoreFirst {
				if err := s.submitPalloneRunScore(packet, at); err != nil {
					t.Fatal(err)
				}
				if r.settled != 0 {
					t.Fatal("score credited before clear")
				}
			}
			if err := s.recordPalloneRunResult(palloneResultFixture(27365, 4), at); err != nil {
				t.Fatal(err)
			}
			if !scoreFirst {
				if r.settled != 0 {
					t.Fatal("invented score on clear")
				}
				if err := s.submitPalloneRunScore(packet, at); err != nil {
					t.Fatal(err)
				}
			}
			if r.settled != 1 || r.receipt.FinalQuestID != 27365 || r.receipt.EventID != 41 {
				t.Fatalf("settlement %+v", r.receipt)
			}
		})
	}
	for _, code := range []byte{6, 7, 0} {
		s, r := palloneHandlerSession()
		now := TimeAdjusted()
		plan := &palloneDeparturePlan{Event: *r.event, Routes: []PalloneRoute{{Key: 7, QuestIDs: []uint16{27438, 27365}}}}
		s.capturePalloneStage(27438, "Qs1", now, plan)
		s.capturePalloneStage(27365, "Qs1", now.Add(time.Second), plan)
		if err := s.recordPalloneRunResult(palloneResultFixture(27365, code), now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if s.palloneRun == nil || !s.palloneRun.Failed {
			t.Fatal("failed run remained scoreable")
		}
	}
}

func TestPalloneReportsMustBothArriveBeforeTheRoundEnds(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		scoreBefore, resultBefore bool
		want                      int
	}{
		{"both-before", true, true, 1}, {"score-after", false, true, 0}, {"result-after", true, false, 0}, {"both-after", false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, base := palloneHandlerSession()
			r := &palloneRunMemory{mockPalloneRepo: base}
			s.server.palloneRepo = r
			s.server.palloneService = NewPalloneService(r)
			now := TimeAdjusted()
			e := *base.event
			e.End = now.Add(time.Minute)
			plan := &palloneDeparturePlan{Event: e, Routes: []PalloneRoute{{Key: 7, QuestIDs: []uint16{27438, 27365}}}}
			s.capturePalloneStage(27438, "Qs1", now, plan)
			s.capturePalloneStage(27365, "Qs1", now.Add(time.Second), plan)
			before := e.End.Add(-time.Microsecond)
			scoreAt, resultAt := e.End, e.End
			if tc.scoreBefore {
				scoreAt = before
			}
			if tc.resultBefore {
				resultAt = before
			}
			if err := s.recordPalloneRunResult(palloneResultFixture(27365, 4), resultAt); err != nil {
				t.Fatal(err)
			}
			if err := s.submitPalloneRunScore(&mhfpacket.MsgMhfCaravanMyScore{Unk0: 2, Unk1: 3, Unk2: 7, Unk3: 1000}, scoreAt); err != nil {
				t.Fatal(err)
			}
			if r.settled != tc.want {
				t.Fatal("deadline changed round eligibility", r.settled, tc.want)
			}
		})
	}
}

func TestPalloneLateClearKeepsNormalQuestSuccessWithoutCreditingEitherRound(t *testing.T) {
	s, base := palloneHandlerSession()
	r := &palloneRunMemory{mockPalloneRepo: base}
	s.server.palloneRepo = r
	s.server.palloneService = NewPalloneService(r)
	stats := &mockQuestStatsRepo{}
	s.server.questStatsRepo = stats
	now := TimeAdjusted()
	old := *base.event
	old.End = now.Add(-time.Minute)
	plan := &palloneDeparturePlan{Event: old, Routes: []PalloneRoute{{Key: 7, QuestIDs: []uint16{27438, 27365}}}}
	s.capturePalloneStage(27438, "Qs1", now.Add(-10*time.Minute), plan)
	current := old
	current.ID++
	current.Start = old.End
	current.End = now.Add(21 * 24 * time.Hour)
	current.ClaimUntil = current.End.Add(21 * 24 * time.Hour)
	newPlan := &palloneDeparturePlan{Event: current, Routes: plan.Routes}
	// Continuing into the final quest after rollover still uses the old run.
	s.capturePalloneStage(27365, "Qs1", now, newPlan)
	packet := &mhfpacket.MsgMhfCaravanMyScore{AckHandle: 31, Unk0: 2, Unk1: 3, Unk2: 7, Unk3: 1000}
	handleMsgMhfCaravanMyScore(s, packet)
	if a := readAck(t, s); a.ErrorCode != 0 {
		t.Fatal("late score caused a client error", a)
	}
	handleMsgSysRecordLog(s, &mhfpacket.MsgSysRecordLog{AckHandle: 32, Data: palloneResultFixture(27365, 4)})
	if a := readAck(t, s); a.ErrorCode != 0 {
		t.Fatal("late clear caused a quest error", a)
	}
	if clear, failed := stats.counts(); clear != 1 || failed != 0 {
		t.Fatal("normal quest result was lost", clear, failed)
	}
	if r.settled != 0 || r.qualified != nil || r.submission != nil || s.palloneRun.Receipt.EventID != old.ID {
		t.Fatal("late run migrated or earned festival credit")
	}
	s.capturePalloneStage(27438, "Qs2", now, newPlan)
	s.capturePalloneStage(27365, "Qs2", now.Add(time.Second), newPlan)
	if err := s.recordPalloneRunResult(palloneResultFixture(27365, 4), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.submitPalloneRunScore(packet, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if r.settled != 1 || r.receipt.EventID != current.ID {
		t.Fatal("a fresh current-round departure did not count")
	}
}

func TestPalloneSkippedStagesAndFreshDepartureCannotReuseRun(t *testing.T) {
	s, r := palloneHandlerSession()
	now := TimeAdjusted()
	plan := &palloneDeparturePlan{Event: *r.event, Routes: []PalloneRoute{{Key: 7, QuestIDs: []uint16{27438, 27479, 27365}}}}
	s.capturePalloneStage(27438, "Qs1", now, plan)
	id := s.palloneRun.Receipt.RunID
	s.capturePalloneStage(27438, "Qs1", now, plan)
	if s.palloneRun.Receipt.RunID != id {
		t.Fatal("duplicate setup restarted a run")
	}
	s.capturePalloneStageLocked(27438, "Qs1", now, plan, true)
	if s.palloneRun.Receipt.RunID == id {
		t.Fatal("new departure reused a receipt")
	}
	s.capturePalloneStage(27365, "Qs1", now.Add(time.Second), plan)
	if s.palloneRun != nil {
		t.Fatal("skipped intermediate stage")
	}
}

func TestPalloneClaimStagingSeparatesItemsGPAndRetry(t *testing.T) {
	s, _ := palloneHandlerSession()
	item := PalloneRewardOffer{ID: 0xc001, CharID: s.charID, ItemType: palloneItem, ItemID: 0x22e7, Quantity: 10}
	gp := PalloneRewardOffer{ID: 0xc002, CharID: s.charID, ItemType: palloneGP, Quantity: 600}
	if err := s.stagePalloneRewardClaims([]PalloneRewardOffer{item, gp}); err != nil {
		t.Fatal(err)
	}
	s.completePalloneRewardClaims(palloneGP, []uint32{gp.ID})
	if len(s.pendingPalloneRewardClaims(palloneGP)) != 0 || !reflect.DeepEqual(s.pendingPalloneRewardClaims(palloneItem), []uint32{item.ID}) {
		t.Fatal("wrong save type consumed receipts")
	}
	changed := item
	changed.Quantity++
	if err := s.stagePalloneRewardClaims([]PalloneRewardOffer{changed}); !errors.Is(err, ErrPalloneConflict) {
		t.Fatal("retry changed an offered quantity")
	}
	claimed := gp
	claimed.Claimed = true
	if err := s.stagePalloneRewardClaims([]PalloneRewardOffer{claimed}); err != nil || len(s.pendingPalloneRewardClaims(palloneGP)) != 0 {
		t.Fatal("committed receipt staged twice")
	}
	rows := make([]PalloneRewardOffer, 513)
	for i := range rows {
		rows[i].ID = uint32(i + 1)
	}
	if p := palloneRewardPage(rows, 256); len(p) != 256 || p[0].ID != 257 {
		t.Fatal("page shifted after earlier claims")
	}
	if len(palloneRewardPage(rows, ^uint32(0))) != 0 {
		t.Fatal("page offset wrapped")
	}
}
