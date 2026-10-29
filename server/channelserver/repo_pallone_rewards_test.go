package channelserver

import (
	"bytes"
	"errors"
	"erupe-ce/network/mhfpacket"
	"math"
	"sync"
	"testing"
	"time"
)

func TestPallonePostgresRewardIDsNeverAliasTowerAcrossRounds(t *testing.T) {
	db := pallonePostgres(t)
	for _, tc := range []struct {
		ordinal int64
		want    uint32
	}{{1, 0xc000}, {16384, 0xffff}, {16385, 0x1c000}, {1073741824, 0xffffffff}} {
		if _, err := db.Exec(`SELECT setval('pallone_reward_offer_id_seq',$1,false)`, tc.ordinal); err != nil {
			t.Fatal(err)
		}
		var id uint32
		if err := db.Get(&id, `SELECT pallone_next_reward_id()`); err != nil || id != tc.want || !palloneRewardID(id) {
			t.Fatal("invalid reserved ID", id, tc.want, err)
		}
	}
	var id uint32
	if err := db.Get(&id, `SELECT pallone_next_reward_id()`); err == nil {
		t.Fatal("receipt IDs wrapped and could be replayed")
	}
}

func TestPallonePostgresAcceptedGuildSurvivesTransferAndInvalidRunCannotBlockFinalization(t *testing.T) {
	r, db, e, now := palloneRepoFixture(t)
	receipt := palloneReceipt(e, 100, 1, 20, now)
	v := PalloneSubmission{BaseScore: 1000, GuildID: 10}
	if _, err := r.SavePalloneRunReport(receipt, &v, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SavePalloneRunReport(receipt, nil, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE guild_characters SET guild_id=20 WHERE character_id=100`); err != nil {
		t.Fatal(err)
	}
	server := createMockServer()
	server.palloneRepo = r
	server.palloneService = NewPalloneService(r)
	if err := server.recoverPalloneRunReports(now); err != nil {
		t.Fatal(err)
	}
	var guild int64
	if err := db.Get(&guild, `SELECT guild_id FROM pallone_results WHERE character_id=100`); err != nil || guild != 10 {
		t.Fatal("historical membership changed", guild, err)
	}
	if _, err := r.SettlePalloneResult(palloneReceipt(e, 102, 1, 21, now), PalloneSubmission{BaseScore: math.MaxInt32 - 5}, now); err != nil {
		t.Fatal(err)
	}
	bad := palloneReceipt(e, 102, 1, 22, now)
	score := PalloneSubmission{BaseScore: 10}
	if _, err := r.SavePalloneRunReport(bad, &score, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SavePalloneRunReport(bad, nil, true, now); err != nil {
		t.Fatal(err)
	}
	if err := server.recoverPalloneRunReports(now); err != nil {
		t.Fatal(err)
	}
	var rejected int
	if err := db.Get(&rejected, `SELECT COUNT(*) FROM pallone_run_reports WHERE rejected_at IS NOT NULL`); err != nil || rejected != 1 {
		t.Fatal("permanent invalid report not quarantined", err)
	}
	if done, err := r.FinalizePalloneEvent(e.ID, e.End); err != nil || !done {
		t.Fatal("invalid run blocked everyone", err)
	}
}

func TestPallonePostgresNativeStageAndResultHandlersCreditOnlyCompleteRoute(t *testing.T) {
	r, _, _, _ := palloneRepoFixture(t)
	s, base := palloneHandlerSession()
	installPalloneTestProfile(t, s, base)
	now := TimeAdjusted()
	e := PalloneEvent{ID: 51, Start: now.Add(-time.Hour), End: now.Add(time.Hour), ClaimUntil: now.Add(24 * time.Hour)}
	if err := r.RegisterPalloneEvent(e, base.routes); err != nil {
		t.Fatal(err)
	}
	s.server.palloneRepo = r
	s.server.palloneService = NewPalloneService(r)
	s.server.erupeConfig.EarthID = 51
	stageID := "sl1Qs27438p0a0u1"
	stage := NewStage(stageID)
	stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(27438, 0)
	s.server.stages.Store(stageID, stage)
	if !doStageTransfer(s, 1, stageID) {
		t.Fatal("departure rejected")
	}
	_ = readAck(t, s)
	if s.palloneRun == nil {
		t.Fatal("native admission did not pin event")
	}
	for _, qid := range []uint16{27479, 27348, 27365} {
		handleMsgSysSetStageBinary(s, &mhfpacket.MsgSysSetStageBinary{StageID: stageID, BinaryType0: 1, BinaryType1: 3, RawDataPayload: questRunStagePayload(qid, 0)})
	}
	handleMsgMhfCaravanMyScore(s, &mhfpacket.MsgMhfCaravanMyScore{AckHandle: 2, Unk0: 2, Unk1: 3, Unk2: base.routes[0].Key, Unk3: 160000})
	if ack := readAck(t, s); ack.ErrorCode != 0 {
		t.Fatal("score report", ack)
	}
	before, err := r.GetPalloneScores(51, s.charID)
	if err != nil || len(before) != 0 {
		t.Fatal("score without result", err)
	}
	handleMsgSysRecordLog(s, &mhfpacket.MsgSysRecordLog{AckHandle: 3, Data: palloneResultFixture(27365, 4)})
	if ack := readAck(t, s); ack.ErrorCode != 0 {
		t.Fatal("completion", ack)
	}
	after, err := r.GetPalloneScores(51, s.charID)
	if err != nil || len(after) != 1 || after[0].Total != 160000 {
		t.Fatal("native flow did not credit", after, err)
	}
}

func TestPallonePostgresReportOrderingAndRestartRecovery(t *testing.T) {
	for _, first := range []string{"score", "result"} {
		t.Run(first, func(t *testing.T) {
			r, db, e, now := palloneRepoFixture(t)
			receipt := palloneReceipt(e, 100, 1, 10, now)
			v := PalloneSubmission{BaseScore: 160000, PremiumBonus: 1000, GuildID: 10}
			var ready *PalloneReadyRun
			var err error
			if first == "score" {
				ready, err = r.SavePalloneRunReport(receipt, &v, false, now)
			} else {
				ready, err = r.SavePalloneRunReport(receipt, nil, true, now)
			}
			if err != nil || ready != nil {
				t.Fatalf("first report credited: %+v %v", ready, err)
			}
			if first == "score" {
				ready, err = r.SavePalloneRunReport(receipt, nil, true, now)
			} else {
				ready, err = r.SavePalloneRunReport(receipt, &v, false, now)
			}
			if err != nil || ready == nil {
				t.Fatalf("reports not joined: %+v %v", ready, err)
			}
			changed := v
			changed.BaseScore++
			if _, err = r.SavePalloneRunReport(receipt, &changed, false, now); !errors.Is(err, ErrPalloneConflict) {
				t.Fatal("changed score reused run", err)
			}
			if done, err := r.FinalizePalloneEvent(e.ID, e.End.Add(-time.Microsecond)); err != nil || done {
				t.Fatal("finalized before the event deadline", err)
			}
			server := createMockServer()
			server.palloneRepo = r
			server.palloneService = NewPalloneService(r)
			// A restarted process can recover both reports accepted before closing;
			// a newly arriving report after closing remains ineligible.
			if err = server.recoverPalloneRunReports(e.ClaimUntil.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = db.Get(&count, `SELECT COUNT(*) FROM pallone_results WHERE event_id=$1`, e.ID); err != nil || count != 1 {
				t.Fatal("lost pending result", count, err)
			}
			if err = server.recoverPalloneRunReports(e.ClaimUntil.Add(2 * time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err = r.SavePalloneRunReport(palloneReceipt(e, 100, 1, 11, now), &v, false, e.End); !errors.Is(err, ErrPalloneInvalid) && !errors.Is(err, ErrPalloneIneligible) {
				t.Fatal("new report accepted after deadline", err)
			}
			if done, err := r.FinalizePalloneEvent(e.ID, e.ClaimUntil.Add(time.Hour)); err != nil || !done {
				t.Fatal("offline finalization failed", err)
			}
		})
	}
}

func TestPallonePostgresImmediateFinalizationRecoversOnlyPreDeadlineReports(t *testing.T) {
	r, db, e, now := palloneRepoFixture(t)
	v := PalloneSubmission{BaseScore: 160000, GuildID: 10}
	receipt := palloneReceipt(e, 100, 1, 30, now)
	if _, err := r.SavePalloneRunReport(receipt, &v, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SavePalloneRunReport(receipt, nil, true, now); err != nil {
		t.Fatal(err)
	}
	late := palloneReceipt(e, 101, 1, 31, now)
	late.CompletedAt = time.Time{}
	if _, err := r.SavePalloneRunReport(late, &v, false, now); err != nil {
		t.Fatal(err)
	}
	if done, err := r.FinalizePalloneEvent(e.ID, e.End.Add(-time.Microsecond)); err != nil || done {
		t.Fatal("early finalization", err)
	}
	if done, err := r.FinalizePalloneEvent(e.ID, e.End); err != nil || !done {
		t.Fatal("did not finalize immediately at closing", err)
	}
	rows, err := r.OfferPalloneRewards(e.ID, 100, false, []uint32{3201}, e.End)
	if err != nil || len(rows) == 0 {
		t.Fatal("closed-round awards not immediately available", err)
	}
	late.CompletedAt = e.End.Add(time.Minute)
	if _, err = r.SavePalloneRunReport(late, nil, true, late.CompletedAt); !errors.Is(err, ErrPalloneIneligible) {
		t.Fatal("accepted a late clear", err)
	}
	if _, err = r.SettlePalloneResult(late, v, late.CompletedAt); !errors.Is(err, ErrPalloneIneligible) {
		t.Fatal("late result changed frozen ranking", err)
	}
	var count int
	if err = db.Get(&count, `SELECT COUNT(*) FROM pallone_results WHERE event_id=$1`, e.ID); err != nil || count != 1 {
		t.Fatal("late result credited old round", count, err)
	}
	next := PalloneEvent{ID: e.ID + 1, Start: e.End, End: e.End.Add(21 * 24 * time.Hour), ClaimUntil: e.End.Add(42 * 24 * time.Hour)}
	routes, err := r.GetPalloneRoutes(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.RegisterPalloneEvent(next, routes); err != nil {
		t.Fatal(err)
	}
	if current, err := r.GetPalloneScores(next.ID, 101); err != nil || len(current) != 0 {
		t.Fatal("late run migrated into current round", err)
	}
	var finalCount int
	if err = db.Get(&finalCount, `SELECT COUNT(*) FROM pallone_final_ranks WHERE event_id=$1 AND kind=1`, e.ID); err != nil || finalCount != 1 {
		t.Fatal("final ranks changed", err)
	}
}

func TestPallonePostgresScheduleRolloverSettlesBeforeNextTick(t *testing.T) {
	r := palloneRotationPostgres(t)
	s, base := palloneHandlerSession()
	installPalloneTestProfile(t, s, base)
	s.server.palloneRepo = r
	s.server.palloneService = NewPalloneService(r)
	start := rotationMondayNoon(TimeAdjusted()).AddDate(0, 0, -21)
	o := automaticPalloneOptions()
	o.StartAt = start.Format(time.RFC3339)
	s.server.erupeConfig.PalloneFestival = o
	first, err := s.server.palloneSchedule(start)
	if err != nil {
		t.Fatal(err)
	}
	receipt := PalloneRunReceipt{EventID: first.Current.ID, CharID: s.charID, RouteKey: base.routes[0].Key, RunID: "abcdef0123456789abcdef0123456789", StartedAt: start.Add(time.Minute), CompletedAt: start.Add(10 * time.Minute), FinalQuestID: 27365}
	if _, err = r.SettlePalloneResult(receipt, PalloneSubmission{BaseScore: 160000}, receipt.CompletedAt); err != nil {
		t.Fatal(err)
	}
	next, err := s.server.palloneSchedule(first.Current.End)
	if err != nil || next.Current.ID != first.Current.ID+1 || next.Previous == nil {
		t.Fatal("rollover", next, err)
	}
	var frozen bool
	if err = r.db.Get(&frozen, `SELECT EXISTS(SELECT 1 FROM pallone_event_finalizations WHERE event_id=$1)`, first.Current.ID); err != nil || !frozen {
		t.Fatal("rollover waited for a background tick", err)
	}
	if awards, err := r.OfferPalloneRewards(first.Current.ID, s.charID, false, []uint32{3201}, first.Current.End); err != nil || len(awards) == 0 {
		t.Fatal("previous rewards still unavailable", err)
	}
}

func TestPallonePostgresRewardsFreezeOwnershipAndAtomicSaves(t *testing.T) {
	r, db, e, now := palloneRepoFixture(t)
	if _, err := db.Exec(`ALTER TABLE characters ADD COLUMN gcp bigint NOT NULL DEFAULT 100,ADD COLUMN pact_id bigint NOT NULL DEFAULT 0,ADD COLUMN savedata bytea`); err != nil {
		t.Fatal(err)
	}
	for i, cid := range []uint32{100, 101} {
		receipt := palloneReceipt(e, cid, 1, i+1, now)
		if _, err := r.SettlePalloneResult(receipt, PalloneSubmission{BaseScore: 160000 + int32(i)*1000, GuildID: 10}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE pallone_guild_contributions SET points=CASE character_id WHEN 100 THEN 1520 ELSE 20672 END WHERE event_id=$1`, e.ID); err != nil {
		t.Fatal(err)
	}
	current, err := r.OfferPalloneRewards(e.ID, 100, true, []uint32{3301, 3302, 3303}, now)
	if err != nil || len(current) == 0 {
		t.Fatal("no immediate challenge", err)
	}
	var claimed int
	if err = db.Get(&claimed, `SELECT COUNT(*) FROM pallone_reward_offers WHERE claimed_at IS NOT NULL`); err != nil || claimed != 0 {
		t.Fatal("opening list claimed rewards", err)
	}
	if rows, err := r.OfferPalloneRewards(e.ID, 100, false, []uint32{3201}, now); err != nil || len(rows) != 0 {
		t.Fatal("paid provisional ranking", err)
	}
	if done, err := r.FinalizePalloneEvent(e.ID, e.End.Add(-time.Microsecond)); err != nil || done {
		t.Fatal("finalized before the event deadline", err)
	}
	at := e.End
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done, err := r.FinalizePalloneEvent(e.ID, at)
			if err == nil && !done {
				err = errors.New("not finalized")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	old, err := r.OfferPalloneRewards(e.ID, 100, false, []uint32{3201, 3202, 3203, 3204}, at)
	if err != nil || len(old) == 0 {
		t.Fatal("no previous rewards", err)
	}
	var item, gp PalloneRewardOffer
	guildTicket := false
	for _, o := range old {
		if o.ItemType == palloneItem {
			item = o
		}
		if o.ItemType == palloneGP {
			gp = o
		}
		if o.Key == "guild-level-ticket-1" {
			guildTicket = true
		}
		if o.PresentType >= 3301 {
			t.Fatal("old challenge leaked")
		}
	}
	if item.ID == 0 || gp.ID == 0 || !guildTicket {
		t.Fatal("missing item/GP/guild reward")
	}
	awards, err := r.PendingPalloneRP(100)
	if err != nil || len(awards) != 1 || awards[0].Quantity != 100 {
		t.Fatal("automatic RP queue", awards, err)
	}
	rpTx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if err = commitPalloneRP(rpTx, 100, []int32{e.ID}); err != nil {
		t.Fatal(err)
	}
	_ = rpTx.Rollback()
	if awards, err = r.PendingPalloneRP(100); err != nil || len(awards) != 1 {
		t.Fatal("failed RP save consumed automatic award", err)
	}
	rpTx, err = db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if err = commitPalloneRP(rpTx, 100, []int32{e.ID}); err != nil {
		t.Fatal(err)
	}
	if err = rpTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if awards, err = r.PendingPalloneRP(100); err != nil || len(awards) != 0 {
		t.Fatal("RP paid more than once", err)
	}
	// A changed membership or removal of another contributor cannot change
	// the already frozen entitlement of a recipient.
	if _, err = db.Exec(`UPDATE guild_characters SET guild_id=20 WHERE character_id=100;DELETE FROM guild_characters WHERE character_id=101;DELETE FROM characters WHERE id=101`); err != nil {
		t.Fatal(err)
	}
	still, err := r.OfferPalloneRewards(e.ID, 100, false, []uint32{3201, 3202, 3203, 3204}, at.Add(time.Minute))
	if err != nil || len(still) != len(old) {
		t.Fatal("frozen entitlement changed", len(still), len(old), err)
	}
	if _, err = r.PreparePalloneRewardClaims(102, []uint32{item.ID}, at); !errors.Is(err, ErrPalloneIneligible) {
		t.Fatal("claimed another character's reward", err)
	}
	if _, err = r.PreparePalloneRewardClaims(100, []uint32{item.ID, 0xffffffff}, at); !errors.Is(err, ErrPalloneIneligible) {
		t.Fatal("partial batch accepted", err)
	}
	if _, err = r.PreparePalloneRewardClaims(100, []uint32{item.ID, gp.ID}, at); err != nil {
		t.Fatal(err)
	}
	// Failed item save rolls back both the inventory snapshot and its receipt.
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE characters SET savedata=$1 WHERE id=100`, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err = commitPalloneRewardReceipts(tx, 100, []uint32{item.ID}, palloneItem); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`SELECT 1/0`); err == nil {
		t.Fatal("expected injected SQL failure")
	}
	_ = tx.Rollback()
	if err = db.Get(&claimed, `SELECT COUNT(*) FROM pallone_reward_offers WHERE id=$1 AND claimed_at IS NOT NULL`, item.ID); err != nil || claimed != 0 {
		t.Fatal("failed save lost item", err)
	}
	tx, err = db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE characters SET savedata=$1 WHERE id=100`, []byte{4, 5, 6}); err != nil {
		t.Fatal(err)
	}
	if err = commitPalloneRewardReceipts(tx, 100, []uint32{item.ID}, palloneItem); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var data []byte
	if err = db.Get(&data, `SELECT savedata FROM characters WHERE id=100`); err != nil || !bytes.Equal(data, []byte{4, 5, 6}) {
		t.Fatal("item save not committed", err)
	}
	chars := NewCharacterRepository(db)
	if err = chars.UpdateGCPAndPactWithClientRewards(100, 900, 0, nil, []uint32{item.ID}); !errors.Is(err, ErrPalloneIneligible) {
		t.Fatal("item receipt consumed by GP save", err)
	}
	var balance int
	if err = db.Get(&balance, `SELECT gcp FROM characters WHERE id=100`); err != nil || balance != 100 {
		t.Fatal("failed GP save changed balance", balance, err)
	}
	if err = chars.UpdateGCPAndPactWithClientRewards(100, 100+uint32(gp.Quantity), 0, nil, []uint32{gp.ID}); err != nil {
		t.Fatal(err)
	}
	retry, err := r.PreparePalloneRewardClaims(100, []uint32{gp.ID}, e.ClaimUntil.Add(time.Second))
	if err != nil || len(retry) != 1 || !retry[0].Claimed {
		t.Fatal("committed retry not idempotent", retry, err)
	}
	if rows, err := r.OfferPalloneRewards(e.ID, 100, false, []uint32{3201}, e.ClaimUntil); err != nil || len(rows) != 0 {
		t.Fatal("previous award did not expire", err)
	}
	if _, err = r.SettlePalloneResult(palloneReceipt(e, 102, 1, 90, now), PalloneSubmission{BaseScore: 2000000}, at); !errors.Is(err, ErrPalloneIneligible) {
		t.Fatal("frozen ranking accepted a new score", err)
	}
}
