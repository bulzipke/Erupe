package channelserver

import (
	"encoding/binary"
	"reflect"
	"sync"
	"testing"
	"time"

	"erupe-ce/network/mhfpacket"
)

func TestDivaCustomRewardCatalog(t *testing.T) {
	if len(divaCustomSongRewards) != 18 {
		t.Fatal("custom catalog row count")
	}
	for _, kind := range []uint8{0, 3} {
		var rows []DivaRewardCatalogEntry
		for _, r := range divaCustomSongRewards {
			if r.RewardType == kind {
				rows = append(rows, r)
			}
			if r.Basis != "operator-approved-2026-09-22" {
				t.Fatal("custom data advertised as original")
			}
		}
		if err := validateDivaRewardCatalog(kind, rows); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		hr, gr     uint16
		want       int
		quantities []uint16
	}{
		{0, 0, 0, nil}, {1, 0, 0, nil}, {2, 0, 7, []uint16{1, 100, 3, 1, 200, 5, 3}},
		{99, 0, 7, []uint16{1, 100, 3, 1, 200, 5, 3}}, {100, 0, 7, []uint16{2, 200, 5, 2, 400, 10, 5}},
		{999, 0, 7, []uint16{2, 200, 5, 2, 400, 10, 5}}, {1000, 0, 0, nil}, {999, 1, 12, nil},
	} {
		rows := eligibleDivaSongRewards(0, DivaRewardProgress{HR: tt.hr, GR: tt.gr, Points: 1, ParticipationDays: 7})
		if len(rows) != tt.want {
			t.Fatalf("HR=%d GR=%d: %d", tt.hr, tt.gr, len(rows))
		}
		for i, q := range tt.quantities {
			if rows[i].Quantity != q || rows[i].Threshold != uint32(i+1) {
				t.Fatalf("wrong daily row %+v", rows[i])
			}
		}
	}
	for day := uint32(0); day <= 7; day++ {
		if rows := eligibleDivaSongRewards(0, DivaRewardProgress{HR: 100, Points: 1, ParticipationDays: day}); len(rows) != int(day) {
			t.Fatal("participation threshold")
		}
	}
	if rows := eligibleDivaSongRewards(0, DivaRewardProgress{HR: 100, ParticipationDays: 7}); len(rows) != 0 {
		t.Fatal("zero points qualified")
	}
	for _, tt := range []struct {
		rank     uint32
		count    int
		quantity uint16
	}{{0, 0, 0}, {1, 2, 50}, {100, 2, 50}, {101, 2, 25}, {500, 2, 25}, {501, 0, 0}} {
		rows := eligibleDivaSongRewards(3, DivaRewardProgress{Points: 1, GuildRank: tt.rank})
		if len(rows) != tt.count || (tt.count > 0 && rows[0].Quantity != tt.quantity) {
			t.Fatalf("wrong guild tier %d: %+v", tt.rank, rows)
		}
	}
	daily := divaDailyRewardPayload(divaCustomSongRewards)
	for i := 0; i < 14; i++ {
		row := daily[2+i*15 : 2+(i+1)*15]
		lo, hi := uint32(2), uint32(99)
		if i >= 7 {
			lo, hi = 100, 999
		}
		if row[5] != 0 || binary.BigEndian.Uint32(row[6:10]) != hi || binary.BigEndian.Uint32(row[10:14]) != lo {
			t.Fatalf("native HR bounds: %x", row)
		}
	}
	ranks := divaRankingRewardPayload(divaSongRewardCatalog())
	for i := 0; i < 16; i++ {
		want := byte(0)
		if i >= 12 {
			want = 1
		}
		if ranks[2+i*14+5] != want {
			t.Fatal("personal/guild selector")
		}
	}
}

func TestDivaCustomGuildRewardPhaseGuard(t *testing.T) {
	for _, tt := range []struct {
		age  int64
		mode int
		want int
	}{{3600, -1, 0}, {divaPhaseDuration + 1, -1, 0}, {divaPhaseDuration + divaInterlude + 60, -1, 2}, {divaPhaseDuration + divaInterlude + 60, 1, 0}} {
		s, repo := newDivaRewardHandlerTestSession(DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Unix() - tt.age)})
		s.server.erupeConfig.DebugOptions.DivaOverride = tt.mode
		repo.progress.GuildRank = 1
		handleMsgMhfAcquireUdItem(s, &mhfpacket.MsgMhfAcquireUdItem{AckHandle: 1, Unk0: 1, RewardType: 3})
		ack := readAck(t, s)
		if ack.ErrorCode != 0 || len(ack.Payload) != 2+tt.want*9 {
			t.Fatalf("phase guard: %+v", ack)
		}
		if tt.want == 0 && repo.progressCalls != 0 {
			t.Fatal("queried unfinished ranking")
		}
	}
}

func TestRepoDivaDailyPromotionBundles(t *testing.T) {
	r, db, c, event := setupDivaRewardRepoTest(t)
	first := eligibleDivaSongRewards(0, DivaRewardProgress{HR: 30, Points: 1, ParticipationDays: 2})
	offered, err := r.OfferDivaRewards(c, event, 0, first)
	if err != nil || len(offered) != 2 {
		t.Fatalf("first HR offer %+v %v", offered, err)
	}
	gr := eligibleDivaSongRewards(0, DivaRewardProgress{HR: 999, GR: 1, Points: 1, ParticipationDays: 3})
	next, err := r.OfferDivaRewards(c, event, 0, gr)
	if err != nil || len(next) != 3 || !reflect.DeepEqual(next[:2], offered) || next[2].CatalogKey != "daily-3-2499" {
		t.Fatalf("promotion changed old bundles %+v %v", next, err)
	}
	if _, err := db.Exec(`UPDATE diva_reward_receipts SET claimed_at=NOW() WHERE char_id=$1`, c); err != nil {
		t.Fatal(err)
	}
	if got, err := r.OfferDivaRewards(c, event, 0, gr); err != nil || len(got) != 0 {
		t.Fatalf("promotion duplicate %+v %v", got, err)
	}
	// Multi-item original GR days remain complete, even if the character is later HR.
	c2 := CreateTestCharacter(t, db, CreateTestUser(t, db, "diva_multi"), "Multi")
	five := eligibleDivaSongRewards(0, DivaRewardProgress{GR: 1, Points: 1, ParticipationDays: 5})
	all, err := r.OfferDivaRewards(c2, event, 0, five)
	if err != nil || len(all) != 9 {
		t.Fatalf("GR bundle lost rows %+v %v", all, err)
	}
	hr := eligibleDivaSongRewards(0, DivaRewardProgress{HR: 100, Points: 1, ParticipationDays: 5})
	after, err := r.OfferDivaRewards(c2, event, 0, hr)
	if err != nil || !reflect.DeepEqual(after, all) {
		t.Fatalf("GR bundle replaced %+v %v", after, err)
	}
}

func TestRepoDivaDailyConcurrentPromotion(t *testing.T) {
	r, db, c, event := setupDivaRewardRepoTest(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, progress := range []DivaRewardProgress{{HR: 30, Points: 1, ParticipationDays: 7}, {GR: 1, Points: 1, ParticipationDays: 7}} {
		wg.Add(1)
		go func(p DivaRewardProgress) {
			defer wg.Done()
			_, err := r.OfferDivaRewards(c, event, 0, eligibleDivaSongRewards(0, p))
			errs <- err
		}(progress)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var variants, groups, receipts int
	if err := db.QueryRow(`SELECT COUNT(DISTINCT variant),COUNT(*) FROM diva_reward_groups WHERE char_id=$1`, c).Scan(&variants, &groups); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&receipts, `SELECT COUNT(*) FROM diva_reward_receipts WHERE char_id=$1`, c); err != nil {
		t.Fatal(err)
	}
	if variants != 1 || groups != 7 || (receipts != 7 && receipts != 12) {
		t.Fatalf("mixed concurrent variants %d/%d/%d", variants, groups, receipts)
	}
}

func TestRepoDivaGuildRewardFinalMembership(t *testing.T) {
	r, db := setupDivaRepo(t)
	user := CreateTestUser(t, db, "diva_final_guild")
	a := CreateTestCharacter(t, db, user, "A")
	b := CreateTestCharacter(t, db, user, "B")
	ga := CreateTestGuild(t, db, a, "Alpha")
	gb := CreateTestGuild(t, db, b, "Beta")
	start := TimeAdjusted().Add(-10 * 24 * time.Hour).Truncate(time.Second)
	end := start.Add(time.Duration(divaPhaseDuration) * time.Second)
	var event uint32
	if err := db.QueryRow(`INSERT INTO events(event_type,start_time) VALUES('diva',$1) RETURNING id`, start).Scan(&event); err != nil {
		t.Fatal(err)
	}
	// Simulate memberships observed before this historical test round.
	if _, err := db.Exec(`UPDATE diva_guild_membership_history SET valid_from=$1`, start.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDivaPoints(a, event, 20, 0, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDivaPoints(b, event, 10, 0, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A transfer after the event must preserve the old guild's eligibility.
	if _, err := db.Exec(`UPDATE guild_characters SET guild_id=$1 WHERE character_id=$2`, gb, a); err != nil {
		t.Fatal(err)
	}
	p, err := r.GetDivaRewardProgress(a, event, start, end)
	if err != nil || p.GuildRank != 1 {
		t.Fatalf("end membership lost %+v %v", p, err)
	}
	offers, err := r.OfferDivaRewards(a, event, 3, eligibleDivaSongRewards(3, p))
	if err != nil || len(offers) != 2 {
		t.Fatalf("guild reward missing %+v %v", offers, err)
	}
	// Even an attempted different tier cannot create another reward bundle.
	again, err := r.OfferDivaRewards(a, event, 3, eligibleDivaSongRewards(3, DivaRewardProgress{Points: 1, GuildRank: 101}))
	if err != nil || !reflect.DeepEqual(offers, again) {
		t.Fatalf("guild tier duplicate %+v %v", again, err)
	}
	// Deleting a guild or adding a late/backdated record cannot reorder final ranks.
	if _, err := db.Exec(`DELETE FROM guilds WHERE id=$1`, ga); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDivaPoints(b, event, 1000, 0, start.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	p, err = r.GetDivaRewardProgress(b, event, start, end)
	if err != nil || p.GuildRank != 2 {
		t.Fatalf("final ranking moved %+v %v", p, err)
	}
	ranked, err := r.GetDivaGuildRanking(event, b, end)
	if err != nil || len(ranked) != 2 || ranked[0].Name != "Alpha" || ranked[0].Points != 20 {
		t.Fatalf("display/final snapshot differs %+v %v", ranked, err)
	}
	// A member who left BEFORE the end cannot claim their former guild's prize.
	if _, err := db.Exec(`UPDATE diva_guild_membership_history SET valid_until=$1 WHERE char_id=$2 AND guild_id=$3`, end.Add(-time.Second), a, ga); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE diva_guild_membership_history SET valid_from=$1 WHERE char_id=$2 AND guild_id=$3`, end.Add(-time.Second), a, gb); err != nil {
		t.Fatal(err)
	}
	p, err = r.GetDivaRewardProgress(a, event, start, end)
	if err != nil || p.GuildRank != 0 {
		t.Fatalf("pre-end transfer without a contribution to the new guild retained eligibility %+v %v", p, err)
	}
	// Unknown pre-migration membership is not invented retroactively.
	if _, err := db.Exec(`UPDATE diva_guild_membership_history SET valid_from=$1 WHERE char_id=$2`, end.Add(time.Second), b); err != nil {
		t.Fatal(err)
	}
	p, err = r.GetDivaRewardProgress(b, event, start, end)
	if err != nil || p.GuildRank != 0 {
		t.Fatalf("unknown old membership awarded %+v %v", p, err)
	}
}
