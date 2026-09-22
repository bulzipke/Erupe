package channelserver

import (
	"encoding/binary"
	"math"
	"reflect"
	"testing"
)

func mustCustomDivaMap(t *testing.T) DivaInterceptionMap {
	t.Helper()
	m, err := divaCustomMapInitial()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDivaCustomMapCatalogAndScaling(t *testing.T) {
	m := mustCustomDivaMap(t)
	for number, cost := range []uint64{80000, 100000, 120000, 140000, 160000, 160000, 160000} {
		if err := validateDivaCustomMap(m); err != nil {
			t.Fatal(err)
		}
		var main, branch uint64
		for _, n := range m.States[0].Nodes {
			if n.BranchQuests[0] == 0 {
				main += uint64(n.RequiredPoints)
			} else {
				branch += uint64(n.RequiredPoints)
			}
		}
		if main != cost || branch != cost/8 || m.States[0].MapNumber != uint16(number+1) {
			t.Fatalf("map %d costs main=%d branch=%d", number+1, main, branch)
		}
		progress, err := advanceDivaCustomMap(m, map[uint16]uint64{0: cost + 123})
		if err != nil {
			t.Fatal(err)
		}
		if !progress.GoalCompleted || progress.Discarded != 123 || len(progress.Awarded) != 20 || progress.Map.AcquiredAreas != uint32((number+1)*20) {
			t.Fatalf("unexpected main-only settlement: %+v", progress)
		}
		m, err = divaCustomMapNext(progress.Map)
		if err != nil {
			t.Fatal(err)
		}
		if len(m.States) != 2 || m.States[0].Nodes[1].EarnedPoints != 0 || m.States[1].Nodes[20].EarnedPoints != progress.Map.States[0].Nodes[20].RequiredPoints {
			t.Fatal("next map lost previous state or carried surplus")
		}
	}
}

func TestDivaCustomMapBranchesAreSeparateAndOptional(t *testing.T) {
	initial := mustCustomDivaMap(t)
	if _, err := advanceDivaCustomMap(initial, map[uint16]uint64{407: 5000}); err == nil {
		t.Fatal("locked branch accepted")
	}
	if _, ok := divaCustomMapQuestRoute(initial, 58079); ok {
		t.Fatal("locked branch quest eligible")
	}
	p, err := advanceDivaCustomMap(initial, map[uint16]uint64{0: 12500})
	if err != nil {
		t.Fatal(err)
	}
	if !divaCustomBranchUnlocked(p.Map, 407) || divaCustomBranchUnlocked(p.Map, 106) {
		t.Fatal("wrong first junction unlock")
	}
	for _, id := range []uint16{58079, 58080} {
		if route, ok := divaCustomMapQuestRoute(p.Map, id); !ok || route != 407 {
			t.Fatalf("branch quest %d unavailable", id)
		}
	}
	original := p.Map
	b, err := advanceDivaCustomMap(original, map[uint16]uint64{407: 5001})
	if err != nil {
		t.Fatal(err)
	}
	if b.Map.AcquiredAreas != 6 || b.Discarded != 1 || !reflect.DeepEqual(b.CompletedBranches, []uint16{407}) || b.Map.States[0].Nodes[6].EarnedPoints != 0 {
		t.Fatal("branch changed main progress")
	}
	if !reflect.DeepEqual(original, p.Map) || original.States[0].Nodes[21].EarnedPoints != 0 {
		t.Fatal("input aliased")
	}
	again, err := advanceDivaCustomMap(b.Map, map[uint16]uint64{407: 5000})
	if err != nil || len(again.Awarded) != 0 || again.Map.AcquiredAreas != 6 || again.Discarded != 5000 {
		t.Fatal("branch counted twice", err)
	}
	p, err = advanceDivaCustomMap(b.Map, map[uint16]uint64{0: 40000})
	if err != nil || !divaCustomBranchUnlocked(p.Map, 106) {
		t.Fatal("second junction unavailable", err)
	}
	final, err := advanceDivaCustomMap(p.Map, map[uint16]uint64{0: 27500, 106: 5000})
	if err != nil || !final.GoalCompleted || final.Map.AcquiredAreas != 22 || !reflect.DeepEqual(final.CompletedBranches, []uint16{106}) {
		t.Fatal("same-hour branch and goal not preserved", err)
	}
	if _, err := divaCustomMapNext(final.Map); err != nil {
		t.Fatal(err)
	}
}

func TestDivaCustomMapQuestAllowlist(t *testing.T) {
	m := mustCustomDivaMap(t)
	for _, id := range []uint16{58043, 58050, 58078, 58088, 58109, 58128} {
		if route, ok := divaCustomMapQuestRoute(m, id); !ok || route != 0 {
			t.Fatalf("main %d rejected", id)
		}
	}
	for _, id := range []uint16{0, 40217, 58044, 58073, 58079, 58084, 58095, 58110, 58129, math.MaxUint16} {
		if _, ok := divaCustomMapQuestRoute(m, id); ok {
			t.Fatalf("non-main or locked %d accepted", id)
		}
	}
}

func TestDivaCustomMapTreasureWireAndGeometry(t *testing.T) {
	m := mustCustomDivaMap(t)
	payload, err := divaInterceptionMapPayload(m)
	if err != nil {
		t.Fatal(err)
	}
	const off = 2 + 8 + 64*23
	if binary.BigEndian.Uint16(payload[off:]) != 4 {
		t.Fatal("wrong treasure row count")
	}
	for i, row := range m.Treasures {
		data := payload[off+2+i*14 : off+2+(i+1)*14]
		if binary.BigEndian.Uint32(data) != row.GroupID || data[4] != row.ItemType || binary.BigEndian.Uint16(data[5:]) != row.ItemID || binary.BigEndian.Uint16(data[7:]) != row.Quantity || binary.BigEndian.Uint16(data[9:]) != 1 || binary.BigEndian.Uint16(data[11:]) != math.MaxUint16 || data[13] != 1 {
			t.Fatalf("treasure row %d malformed", i)
		}
	}
	// These paths use only native adjacent horizontal or vertical coordinates.
	for _, node := range m.States[0].Nodes {
		for _, next := range []uint16{node.NextCoordinate, node.BranchStartCoordinate} {
			if next == 0 {
				continue
			}
			dr, dc := int(node.Coordinate/100)-int(next/100), int(node.Coordinate%100)-int(next%100)
			if !((dr == 0 && (dc == 1 || dc == -1)) || (dc == 0 && (dr == 1 || dr == -1))) {
				t.Fatalf("nonadjacent path %d -> %d", node.Coordinate, next)
			}
		}
	}
}

func TestDivaCustomMapRejectsChangedRulesAndMalformedBranches(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*DivaInterceptionMap)
	}{
		{"empty", func(m *DivaInterceptionMap) { *m = DivaInterceptionMap{} }},
		{"generic-only", func(m *DivaInterceptionMap) { *m = syntheticDivaInterceptionMap() }},
		{"changed-cost", func(m *DivaInterceptionMap) { m.States[0].Nodes[1].RequiredPoints++ }},
		{"changed-reward", func(m *DivaInterceptionMap) { m.Treasures[0].Quantity++ }},
		{"orphan-branch", func(m *DivaInterceptionMap) { m.States[0].Nodes[6].BranchStartCoordinate = 0 }},
		{"shared-branch", func(m *DivaInterceptionMap) { m.States[0].Nodes[16].BranchStartCoordinate = 407 }},
		{"quest-gap", func(m *DivaInterceptionMap) { m.States[0].Nodes[21].BranchQuests = [3]uint16{58079, 0, 58080} }},
		{"locked-progress", func(m *DivaInterceptionMap) { m.States[0].Nodes[21].EarnedPoints = 1 }},
		{"unknown-group", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[21].TreasureGroupID = 999 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := mustCustomDivaMap(t)
			tt.change(&m)
			if _, err := divaCustomMapNext(m); err == nil {
				t.Fatal("invalid next map accepted")
			}
			if _, err := advanceDivaCustomMap(m, map[uint16]uint64{0: 100}); err == nil {
				t.Fatal("invalid progress accepted")
			}
			if _, ok := divaCustomMapQuestRoute(m, 58043); ok {
				t.Fatal("invalid map accepted departure")
			}
		})
	}
	m := mustCustomDivaMap(t)
	if _, err := divaCustomMapNext(m); err == nil {
		t.Fatal("incomplete goal accepted")
	}
	if _, err := advanceDivaCustomMap(m, map[uint16]uint64{999: 1}); err == nil {
		t.Fatal("unknown route accepted")
	}
}
