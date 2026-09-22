package channelserver

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestDivaMapGenerationMetadataIsNotOnWire(t *testing.T) {
	m := syntheticDivaInterceptionMap()
	want, err := divaInterceptionMapPayload(m)
	if err != nil {
		t.Fatal(err)
	}
	m.RulesVersion, m.GenerationSeed = "persistence-only", 987654321
	got, err := divaInterceptionMapPayload(m)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("generation identity changed the native packet", err)
	}
	stored, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var restored DivaInterceptionMap
	if err = json.Unmarshal(stored, &restored); err != nil || !reflect.DeepEqual(restored, m) {
		t.Fatal("generation identity was lost in persistence", err)
	}
}

func TestDivaMapBranchTreasureLookupUsesActualLayout(t *testing.T) {
	old, err := divaCustomMapInitial()
	if err != nil {
		t.Fatal(err)
	}
	for _, coordinate := range []uint16{407, 106} {
		if !reflect.DeepEqual(divaMapBranchTreasureRewards(old, coordinate), divaCustomBranchTreasureRewards(1, coordinate)) {
			t.Fatal("legacy receipt keys/amounts changed")
		}
	}
	m, err := divaRandomMapInitial(17)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range m.States[0].Nodes {
		got := divaMapBranchTreasureRewards(m, node.Coordinate)
		if node.BranchQuests[0] == 0 {
			if len(got) != 0 {
				t.Fatal("main coordinate received a branch treasure")
			}
			continue
		}
		want := divaApprovedBranchTreasureRewards(1, node.Coordinate)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("random branch lost its approved rewards")
		}
	}
	if len(divaMapBranchTreasureRewards(m, 999)) != 0 || len(divaMapBranchTreasureRewards(DivaInterceptionMap{}, 407)) != 0 {
		t.Fatal("missing layout or coordinate granted a treasure")
	}
}

func TestRepoDivaRandomTreasureSurvivesMoreThanTwoPages(t *testing.T) {
	r, db, char, guild, event, start, mainQuest := setupDivaMapRepoTest(t)
	if _, err := db.Exec(`UPDATE diva_random_map_cutover SET installed_at=$1`, start.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Pin a deterministic fixture seed before creating any guild map. Production
	// never rewrites an event seed; it is allocated once inside the event lock.
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockDivaMapEvent(tx, event.ID); err != nil {
		t.Fatal(err)
	}
	if _, enabled, err := ensureDivaMapEventTx(tx, event.ID, start); err != nil || !enabled {
		t.Fatal("random test event unavailable", err)
	}
	if _, err = tx.Exec(`UPDATE diva_map_events SET generation_seed=17 WHERE event_id=$1 AND rules_version=$2`, event.ID, divaRandomMapRules); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	view, err := r.GetDivaMap(char, guild, event.ID, start)
	if err != nil || !view.Enabled || view.Map.RulesVersion != divaRandomMapRules {
		t.Fatalf("random map unavailable: %+v %v", view, err)
	}
	branch := view.Map.States[0].Nodes[21]
	divaMapTestReport(t, r, char, guild, event, mainQuest, divaRunKeyOne, 15000, start.Add(time.Minute), start.Add(2*time.Minute))
	if _, err = r.GetDivaMap(char, guild, event.ID, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	divaMapTestReport(t, r, char, guild, event, branch.BranchQuests[0], divaRunKeyTwo, 5000, start.Add(61*time.Minute), start.Add(62*time.Minute))
	divaMapTestReport(t, r, char, guild, event, mainQuest, "00000000-0000-4000-8000-000000000003", 65000, start.Add(63*time.Minute), start.Add(64*time.Minute))
	if _, err = r.GetDivaMap(char, guild, event.ID, start.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	divaMapTestReport(t, r, char, guild, event, mainQuest, "00000000-0000-4000-8000-000000000004", 100000, start.Add(121*time.Minute), start.Add(122*time.Minute))
	now := start.Add(3 * time.Hour)
	view, err = r.GetDivaMap(char, guild, event.ID, now)
	if err != nil || view.Map.States[0].MapNumber != 3 || view.Map.States[1].MapNumber != 2 || view.Map.AcquiredAreas != 41 {
		t.Fatalf("old page not evicted from UI: %+v %v", view, err)
	}
	offers, err := r.offerDivaMapRewardsAt(char, 5, now)
	if err != nil || len(offers) != 2 {
		t.Fatalf("page-one treasure lost after third page: %+v %v", offers, err)
	}
	want := divaApprovedBranchTreasureRewards(1, branch.Coordinate)
	for i, offer := range offers {
		if offer.CatalogKey != want[i].Key || offer.ItemType != want[i].ItemType || offer.ItemID != want[i].ItemID || offer.Quantity != want[i].Quantity {
			t.Fatal("historical treasure identity changed", offers)
		}
	}
	if _, err = r.prepareDivaMapRewardClaimsAt(char, 5, divaGuildRewardTestIDs(offers, 0), now); err != nil {
		t.Fatal(err)
	}
	again, err := r.offerDivaMapRewardsAt(char, 5, now.Add(time.Minute))
	if err != nil || !reflect.DeepEqual(again, offers) {
		t.Fatal("historical treasure replay changed IDs", err)
	}
	// A persisted map from another seed must never be accepted just because its
	// coordinate happens to match a branch award. Restore only this test fixture.
	var snapshot []byte
	if err = db.Get(&snapshot, `SELECT map_data FROM diva_map_snapshots WHERE event_id=$1 AND guild_id=$2 AND map_number=1 ORDER BY settled_at DESC LIMIT 1`, event.ID, guild); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE diva_map_snapshots SET map_data=jsonb_set(map_data,'{GenerationSeed}','18') WHERE event_id=$1 AND guild_id=$2 AND map_number=1`, event.ID, guild); err != nil {
		t.Fatal(err)
	}
	if _, err = r.prepareDivaMapRewardClaimsAt(char, 5, divaGuildRewardTestIDs(offers, 0), now); err == nil {
		t.Fatal("changed historical seed accepted for a pending treasure")
	}
	if _, err = db.Exec(`UPDATE diva_map_snapshots SET map_data=$3 WHERE event_id=$1 AND guild_id=$2 AND map_number=1`, event.ID, guild, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE diva_reward_receipts SET claimed_at=$2 WHERE id=$1`, offers[0].ID, now); err != nil {
		t.Fatal(err)
	}
	remaining, err := r.offerDivaMapRewardsAt(char, 5, now)
	if err != nil || len(remaining) != 1 || remaining[0].ID != offers[1].ID || remaining[0].ItemType != 26 {
		t.Fatalf("partial claim regranted material or lost GP: %+v %v", remaining, err)
	}
}
