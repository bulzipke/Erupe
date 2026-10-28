package channelserver

import (
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestTowerContinuousRotationKeepsPreviousRewards(t *testing.T) {
	_, db := setupDivaRepo(t)
	r := NewTowerRepository(db)
	u := CreateTestUser(t, db, "tower_continuous")
	cid := CreateTestCharacter(t, db, u, "Tower")
	if _, err := r.GetTowerData(cid); err != nil {
		t.Fatal(err)
	}
	start := rotationMondayNoon(TimeAdjusted()).AddDate(0, 0, -21)
	o := cfg.TowerRotationOptions{Enabled: true, StartAt: start.Format(time.RFC3339), ActiveDays: 21, CycleDays: 21}
	first, err := r.EnsureTowerEvent(start, o, 36)
	if err != nil || first.End.Sub(first.Start) != 21*24*time.Hour || first.ClaimUntil.Sub(first.End) != 21*24*time.Hour {
		t.Fatalf("continuous dates: %+v %v", first, err)
	}
	if err := r.RecordTowerRun(first.ID, cid, 1, start, TowerMissionStats{Floors: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tower SET block1=1,tr=60,trp=35400,tsp=17 WHERE char_id=$1`, cid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tower_daily_rewards(earth_id,character_id,day_start,mission_slot,item_slot,item_id,quantity)
		VALUES($2,$1,$3,0,0,100,1)`, cid, first.ID, start); err != nil {
		t.Fatal(err)
	}
	current, err := r.EnsureTowerEvent(first.End, o, 36)
	if err != nil || current.ID != first.ID+1 || !current.Start.Equal(first.End) || !current.Active(first.End) {
		t.Fatalf("gap or delayed rollover: %+v %v", current, err)
	}
	for _, at := range []time.Time{current.Start, current.End.Add(-time.Microsecond)} {
		events, err := r.ClaimableTowerEvents(at)
		if err != nil || len(events) != 2 || events[0].ID != first.ID || events[1].ID != current.ID {
			t.Fatalf("previous rewards not available throughout the new round at %v: %+v %v", at, events, err)
		}
	}
	td, err := r.GetTowerData(cid)
	if err != nil || td.Block1 != 0 || td.TR != 60 || td.TRP != 35400 || td.TSP != 17 {
		t.Fatalf("rollover progress/growth: %+v %v", td, err)
	}
	if err := r.RecordTowerRun(current.ID, cid, 1, current.Start, TowerMissionStats{Floors: 1}); err != nil {
		t.Fatal(err)
	}
	srv := createMockServer()
	srv.towerRepo, srv.erupeConfig.TowerRotation, srv.erupeConfig.EarthID = r, o, 36
	s := createMockSession(cid, srv)
	handleTowerPresentBox(s, &mhfpacket.MsgMhfPresentBox{Unk1: 1, Unk7: []uint32{towerPresentFloor}})
	ids := towerPresentListIndexes(t, (<-s.sendPackets).data)
	if len(ids) != 2 || ids[0]>>16 != uint32(first.ID) || ids[1]>>16 != uint32(current.ID) {
		t.Fatalf("old/current reward list lost round identity: %v", ids)
	}
	handleTowerPresentBox(s, &mhfpacket.MsgMhfPresentBox{Unk1: 2, Unk7: []uint32{ids[0]}})
	if code, _ := parseAckSimpleValue(t, (<-s.sendPackets).data); code != 0 {
		t.Fatalf("previous receipt failed: %d", code)
	}
	oldState, err := r.GetTowerRewardState(first.ID, cid, start)
	if err != nil || !oldState.Claimed[towerRewardKey(towerRewardFloor, 1)] {
		t.Fatalf("old receipt missing: %+v %v", oldState, err)
	}
	newState, err := r.GetTowerRewardState(current.ID, cid, current.Start)
	if err != nil || newState.Claimed[towerRewardKey(towerRewardFloor, 1)] {
		t.Fatalf("old receipt consumed new reward: %+v %v", newState, err)
	}
	daily, err := s.listTowerDailyRewards()
	if err != nil || len(daily) != 4 || daily[0] != 100 {
		t.Fatalf("previous daily queue missing: %x %v", daily, err)
	}
	third, err := r.EnsureTowerEvent(current.End, o, 36)
	if err != nil {
		t.Fatal(err)
	}
	events, err := r.ClaimableTowerEvents(third.Start)
	if err != nil || len(events) != 2 || events[0].ID != current.ID || events[1].ID != third.ID {
		t.Fatalf("old rewards did not expire at next-next round: %+v %v", events, err)
	}
}
