package channelserver

import (
	"testing"
	"time"
)

func TestRepoDivaMelodyHRRoundCutover(t *testing.T) {
	r, db := setupDivaRepo(t)
	userID := CreateTestUser(t, db, "melody_cutover")
	charID := CreateTestCharacter(t, db, userID, "MelodyCutover")
	start := divaTestTime(21, 12, 0)
	old, err := r.EnsureDivaEvent(start, -1)
	if err != nil {
		t.Fatal(err)
	}
	// Migrate after the old prayer phase has begun, before those points or
	// receipts exist. Its eligibility must still follow the old rules.
	if _, err := db.Exec(`INSERT INTO diva_hr_melody_cutover(installed_at) VALUES($1)`, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	checkPreview := func(now time.Time, wantHRRows int) {
		t.Helper()
		rows, err := r.GetDivaMelodyPrizes(now, -1)
		if err != nil || len(rows) != 10+wantHRRows {
			t.Fatalf("preview at %v: %+v, %v", now, rows, err)
		}
		var eligibleHR, eligibleGR int
		for _, row := range rows {
			if row.PointsReq > 59702 {
				continue
			}
			if row.GR {
				eligibleGR = max(eligibleGR, row.Quantity)
			} else {
				eligibleHR = max(eligibleHR, row.Quantity)
			}
		}
		wantHR := 3
		if wantHRRows == 10 {
			wantHR = 8
		}
		if eligibleHR != wantHR || eligibleGR != 3 {
			t.Fatalf("59702-point preview: HR=%d, GR=%d, want HR=%d, GR=3", eligibleHR, eligibleGR, wantHR)
		}
	}
	checkPreview(start.Add(2*time.Hour), 10)

	award := func(event DivaEvent, now time.Time, points, wantRows, wantCap int) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO diva_interception_runs(char_id,event_id,run_key,quest_id,guild_id,points,started_at,submitted_at)
			VALUES($1,$2,$3,1,1,$4,$5,$5) ON CONFLICT(char_id,event_id,run_key) DO UPDATE SET points=EXCLUDED.points`,
			charID, event.ID, divaRunKeyOne, points, event.phaseTime(2)); err != nil {
			t.Fatal(err)
		}
		offers, err := r.offerDivaRewardsAt(charID, event.ID, 6, nil, now, -1)
		if err != nil || len(offers) != wantRows || offers[len(offers)-1].Quantity != uint16(wantCap) {
			t.Fatalf("round %d, %d points: offers %+v, want %d rows with cap %d, err %v", event.ID, points, offers, wantRows, wantCap, err)
		}
		if _, err := r.prepareDivaRewardClaimsAt(charID, 6, []uint32{offers[len(offers)-1].ID}, now, -1); err != nil {
			t.Fatal(err)
		}
		if balance, err := r.getDivaMelodyAt(userID, charID, -1, now); err != nil || balance != uint8(wantCap) {
			t.Fatalf("round %d balance %d, want %d, err %v", event.ID, balance, wantCap, err)
		}
	}
	checkPreview(old.phaseTime(2), 10)
	award(old, old.phaseTime(4), 59702, 8, 8)
	checkPreview(old.phaseTime(5), 10)
	next, err := r.EnsureDivaEvent(old.phaseTime(5), -1)
	if err != nil || next.ID == old.ID {
		t.Fatalf("next round %+v, err %v", next, err)
	}
	// The preview itself must register the next interception at the boundary.
	checkPreview(next.phaseTime(2), 3)
	award(next, next.phaseTime(2), 59702, 3, 3)
	if _, err := db.Exec(`UPDATE characters SET gr=1 WHERE id=$1`, charID); err != nil {
		t.Fatal(err)
	}
	award(next, next.phaseTime(2), 75000, 6, 4) // Two unclaimed HR rows and four GR rows.
}
