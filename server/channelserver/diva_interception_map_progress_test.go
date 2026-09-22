package channelserver

import (
	"math"
	"reflect"
	"testing"
)

func TestDivaInterceptionMapProgress(t *testing.T) {
	for _, tt := range []struct {
		name                string
		points              uint64
		middle, goal, areas uint32
		discarded           uint64
		complete            bool
	}{
		{"zero", 0, 40, 0, 0, 0, false},
		{"before threshold", 59, 99, 0, 0, 0, false},
		{"exact threshold", 60, 100, 0, 1, 0, false},
		{"next area frontier", 61, 100, 1, 1, 0, false},
		{"exact goal", 260, 100, 200, 2, 0, true},
		{"surplus discarded", 300, 100, 200, 2, 40, true},
		{"huge input bounded by node count", math.MaxUint64, 100, 200, 2, math.MaxUint64 - 260, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original := syntheticDivaInterceptionMap()
			got, err := advanceDivaMainMap(original, tt.points)
			if err != nil {
				t.Fatal(err)
			}
			nodes := got.Map.States[0].Nodes
			if nodes[0].EarnedPoints != 0 || nodes[1].EarnedPoints != tt.middle || nodes[2].EarnedPoints != tt.goal ||
				got.NewAreas != tt.areas || got.Map.AcquiredAreas != tt.areas || got.Discarded != tt.discarded || got.GoalCompleted != tt.complete {
				t.Fatalf("unexpected result: %+v, nodes=%+v", got, nodes)
			}
			if len(got.Map.States) != 1 || got.Map.States[0].MapNumber != 1 {
				t.Fatal("progress must not generate or advance the next map")
			}
			if err := validateDivaInterceptionMap(got.Map); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, syntheticDivaInterceptionMap()) {
				t.Fatal("input mutated")
			}
		})
	}
}

func TestDivaInterceptionMapProgressNoRecount(t *testing.T) {
	m := syntheticDivaInterceptionMap()
	m.AcquiredAreas = 20
	first, err := advanceDivaMainMap(m, 260)
	if err != nil {
		t.Fatal(err)
	}
	second, err := advanceDivaMainMap(first.Map, 500)
	if err != nil {
		t.Fatal(err)
	}
	if second.NewAreas != 0 || second.Map.AcquiredAreas != 22 || second.Discarded != 500 || !second.GoalCompleted {
		t.Fatalf("completed map recounted or surplus carried over: %+v", second)
	}
}

func TestDivaInterceptionMapProgressNoAliasing(t *testing.T) {
	m := syntheticDivaInterceptionMap()
	m.States[0].MapNumber = 2
	previous := m.States[0]
	previous.MapNumber = 1
	previous.Nodes = append([]DivaInterceptionMapNodeState(nil), previous.Nodes...)
	for i := range previous.Nodes {
		previous.Nodes[i].EarnedPoints = previous.Nodes[i].RequiredPoints
	}
	m.States = append(m.States, previous)
	got, err := advanceDivaMainMap(m, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Map.States[1], previous) {
		t.Fatal("previous map changed")
	}
	got.Map.Definitions[0].ID = 99
	got.Map.Definitions[0].Nodes[1].Coordinate = 101
	got.Map.States[0].Nodes[1].EarnedPoints = 99
	got.Map.States[1].Nodes[1].EarnedPoints = 0
	if m.Definitions[0].ID != 1 || m.Definitions[0].Nodes[1].Coordinate != 302 ||
		m.States[0].Nodes[1].EarnedPoints != 40 || m.States[1].Nodes[1].EarnedPoints != 100 {
		t.Fatal("returned slices alias input")
	}
}

func TestDivaInterceptionMapProgressRejectsInvalid(t *testing.T) {
	for _, tt := range []struct {
		name  string
		alter func(*DivaInterceptionMap)
	}{
		{"no current map", func(m *DivaInterceptionMap) { m.States = nil }},
		{"points exceed threshold", func(m *DivaInterceptionMap) { m.States[0].Nodes[1].EarnedPoints = 101 }},
		{"area total overflow", func(m *DivaInterceptionMap) { m.AcquiredAreas = math.MaxInt32 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := syntheticDivaInterceptionMap()
			tt.alter(&m)
			got, err := advanceDivaMainMap(m, 260)
			if err == nil || !reflect.DeepEqual(got, divaMainMapProgress{}) {
				t.Fatalf("invalid data produced partial progress: %+v, %v", got, err)
			}
		})
	}
}
