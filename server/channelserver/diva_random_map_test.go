package channelserver

import (
	"math"
	"reflect"
	"slices"
	"testing"
)

func mustRandomDivaMap(t *testing.T, seed uint64) DivaInterceptionMap {
	t.Helper()
	m, err := divaRandomMapInitial(seed)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func assertRandomDivaGeometry(t *testing.T, nodes []DivaInterceptionMapNodeState) {
	t.Helper()
	if len(nodes) != 23 {
		t.Fatalf("wrong node count: %d", len(nodes))
	}
	seen := make(map[uint16]bool)
	for i, node := range nodes {
		if !divaInterceptionMapCoordinateValid(node.Coordinate) || seen[node.Coordinate] || int(node.Ordinal) != i+1 {
			t.Fatalf("bad coordinate/ordinal at %d: %+v", i, node)
		}
		seen[node.Coordinate] = true
		if i < 20 && (node.NextCoordinate != nodes[i+1].Coordinate || !divaMapHexAdjacent(node.Coordinate, node.NextCoordinate)) {
			t.Fatalf("nonadjacent or unordered main path: %+v", node)
		}
		if i >= 20 && node.NextCoordinate != 0 {
			t.Fatal("goal/branch has a next link")
		}
	}
	for i, junction := range []int{6, 16} {
		branch := nodes[21+i]
		if nodes[junction].BranchStartCoordinate != branch.Coordinate || !divaMapHexAdjacent(nodes[junction].Coordinate, branch.Coordinate) {
			t.Fatalf("invalid branch %d", i)
		}
	}
}

func TestDivaRandomMapHexNeighbors(t *testing.T) {
	if got := divaMapHexNeighbors(303); !reflect.DeepEqual(got, []uint16{403, 203, 302, 304, 202, 204}) {
		t.Fatalf("odd-column geometry: %v", got)
	}
	if got := divaMapHexNeighbors(304); !reflect.DeepEqual(got, []uint16{404, 204, 303, 305, 403, 405}) {
		t.Fatalf("even-column geometry: %v", got)
	}
	if len(divaMapHexNeighbors(0)) != 0 || divaMapHexAdjacent(301, 512) {
		t.Fatal("invalid/nonadjacent coordinates accepted")
	}
	for row := uint16(1); row <= 5; row++ {
		for col := uint16(1); col <= 12; col++ {
			a := row*100 + col
			for _, b := range divaMapHexNeighbors(a) {
				if !divaMapHexAdjacent(b, a) {
					t.Fatalf("asymmetric neighbors %d/%d", a, b)
				}
			}
		}
	}
}

func TestDivaRandomMapPortableGolden(t *testing.T) {
	m := mustRandomDivaMap(t, 1)
	want := []uint16{502, 402, 403, 404, 503, 504, 505, 406, 506, 507, 508, 509, 510, 511, 410, 409, 310, 210, 311, 312, 412, 405, 309}
	for i, n := range m.States[0].Nodes {
		if n.Coordinate != want[i] {
			t.Fatalf("v2 deterministic geometry changed at %d: %d != %d", i, n.Coordinate, want[i])
		}
	}
	again := mustRandomDivaMap(t, 1)
	if !reflect.DeepEqual(m, again) {
		t.Fatal("same seed changed catalog")
	}
}

// At least 3,000 seed/map combinations cover real native paging as well as
// generation. These are server-custom fixtures, not recovered retail maps.
func TestDivaRandomMapThousandSeedProperties(t *testing.T) {
	paths := make(map[[21]uint16]bool)
	branchPlaces := make(map[[2]uint16]bool)
	var internalStarts int
	for seed := uint64(1); seed <= 1000; seed++ {
		m := mustRandomDivaMap(t, seed)
		for number := uint16(1); number <= 3; number++ {
			if err := validateDivaCustomMap(m); err != nil {
				t.Fatalf("seed=%d map=%d: %v", seed, number, err)
			}
			if _, err := divaInterceptionMapPayload(m); err != nil {
				t.Fatalf("seed=%d map=%d wire: %v", seed, number, err)
			}
			if m.RulesVersion != divaRandomMapRules || m.GenerationSeed != seed {
				t.Fatal("metadata lost")
			}
			current := m.States[0]
			assertRandomDivaGeometry(t, current.Nodes)
			if current.Nodes[20].Coordinate%100 != 12 || current.TemplateID != uint32(number) || current.MapNumber != number || m.Definitions[0].NextTemplateID != 0 {
				t.Fatal("wrong goal/template/number")
			}
			if number == 1 {
				var key [21]uint16
				for i := range key {
					key[i] = current.Nodes[i].Coordinate
				}
				paths[key] = true
				branchPlaces[[2]uint16{current.Nodes[21].Coordinate, current.Nodes[22].Coordinate}] = true
				if current.Nodes[0].Coordinate%100 > 1 {
					internalStarts++
				}
				if len(m.States) != 1 || len(m.Definitions) != 1 {
					t.Fatal("first map contains invented past")
				}
			} else {
				if len(m.States) != 2 || len(m.Definitions) != 2 || current.Nodes[0].Coordinate%100 != 1 ||
					current.Nodes[0].Coordinate/100 != m.States[1].Nodes[20].Coordinate/100 ||
					m.Definitions[1].NextTemplateID != current.TemplateID {
					t.Fatal("previous/current seam or paging mismatch")
				}
			}
			var main, branch uint64
			for _, n := range current.Nodes {
				if n.BranchQuests[0] == 0 {
					main += uint64(n.RequiredPoints)
				} else {
					branch += uint64(n.RequiredPoints)
				}
			}
			wantCost := uint64(80000) + 20000*uint64(min(number-1, 4))
			if main != wantCost || branch != wantCost/8 {
				t.Fatalf("map %d changed costs: %d/%d", number, main, branch)
			}
			done, err := advanceDivaCustomMap(m, map[uint16]uint64{0: main + 321})
			if err != nil || !done.GoalCompleted || done.Discarded != 321 || len(done.Awarded) != 20 || done.Map.AcquiredAreas != uint32(number)*20 {
				t.Fatalf("seed=%d map=%d advance: %+v %v", seed, number, done, err)
			}
			if number < 3 {
				next, err := divaCustomMapNext(done.Map)
				if err != nil || !reflect.DeepEqual(next.States[1], done.Map.States[0]) {
					t.Fatalf("previous progress not preserved: %v", err)
				}
				m = next
			}
		}
	}
	if len(paths) < 700 || len(branchPlaces) < 100 || internalStarts < 700 {
		t.Fatalf("insufficient variety: paths=%d branches=%d internal=%d", len(paths), len(branchPlaces), internalStarts)
	}
	t.Logf("1000 seeds/3000 maps: first paths=%d branch placements=%d internal starts=%d fallback starts=%d", len(paths), len(branchPlaces), internalStarts, 1000-internalStarts)
}

func BenchmarkDivaRandomMapGeometry(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := divaRandomMapGeometry(uint64(i%1000+1), uint16(i%100+1), divaRandomMapSearchBudget); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDivaRandomMapInitial(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := divaRandomMapInitial(uint64(i%1000 + 1)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDivaRandomMapValidateTwoStates(b *testing.B) {
	m, err := divaRandomMapInitial(1)
	if err != nil {
		b.Fatal(err)
	}
	done, err := advanceDivaCustomMap(m, map[uint16]uint64{0: 80000})
	if err != nil {
		b.Fatal(err)
	}
	m, err = divaRandomMapNext(done.Map)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validateDivaRandomMap(m); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDivaRandomMapValidateSearchBound(b *testing.B) {
	var m DivaInterceptionMap
	for seed := uint64(1); seed <= 1000; seed++ {
		candidate, err := divaRandomMapInitial(seed)
		if err != nil {
			b.Fatal(err)
		}
		if candidate.States[0].Nodes[0].Coordinate%100 == 1 {
			m = candidate // First-map fallback means the full 4,096-node budget ran.
			break
		}
	}
	if len(m.States) == 0 {
		b.Fatal("no bound-exhausting fixture found")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validateDivaRandomMap(m); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDivaRandomMapFallbackAllEndpointRows(t *testing.T) {
	for start := uint16(1); start <= 5; start++ {
		for end := uint16(1); end <= 5; end++ {
			path, branches, err := divaRandomMapFallback(start, end)
			if err != nil || len(path) != 21 || path[0] != start*100+1 || path[20] != end*100+12 {
				t.Fatalf("fallback %d/%d: %v %v", start, end, path, err)
			}
			seen := make(map[uint16]bool)
			for i, c := range path {
				if seen[c] || !divaInterceptionMapCoordinateValid(c) || (i > 0 && !divaMapHexAdjacent(path[i-1], c)) {
					t.Fatalf("invalid fallback path %d/%d", start, end)
				}
				seen[c] = true
			}
			for i, c := range branches {
				if seen[c] || !divaMapHexAdjacent(path[6+i*10], c) {
					t.Fatalf("invalid fallback branch %d/%d", start, end)
				}
				seen[c] = true
			}
		}
	}
	for seed := uint64(1); seed <= 100; seed++ {
		path, branches, err := divaRandomMapGeometry(seed, 2, 0)
		want, wantBranches, wantErr := divaRandomMapFallback(divaRandomMapEndRow(seed, 1), divaRandomMapEndRow(seed, 2))
		if err != nil || wantErr != nil || !slices.Equal(path, want) || branches != wantBranches {
			t.Fatal("zero budget did not use deterministic safe fallback")
		}
	}
	for _, rows := range [][2]uint16{{0, 1}, {1, 0}, {6, 1}, {1, 6}} {
		if _, _, err := divaRandomMapFallback(rows[0], rows[1]); err == nil {
			t.Fatal("out-of-grid fallback accepted")
		}
	}
}

func TestDivaRandomMapRoutesProgressAndAliasing(t *testing.T) {
	initial := mustRandomDivaMap(t, 2)
	branch1, branch2 := initial.States[0].Nodes[21].Coordinate, initial.States[0].Nodes[22].Coordinate
	if _, ok := divaCustomMapQuestRoute(initial, 58079); ok {
		t.Fatal("locked branch quest accepted")
	}
	if _, err := advanceDivaCustomMap(initial, map[uint16]uint64{branch1: 5000}); err == nil {
		t.Fatal("locked branch points accepted")
	}
	p, err := advanceDivaCustomMap(initial, map[uint16]uint64{0: 12500})
	if err != nil || !divaCustomBranchUnlocked(p.Map, branch1) || divaCustomBranchUnlocked(p.Map, branch2) {
		t.Fatal("first junction unlock mismatch", err)
	}
	for _, id := range []uint16{58079, 58080} {
		if route, ok := divaCustomMapQuestRoute(p.Map, id); !ok || route != branch1 {
			t.Fatal("dynamic branch coordinate not used")
		}
	}
	p, err = advanceDivaCustomMap(p.Map, map[uint16]uint64{branch1: 5001, 0: 40000})
	if err != nil || p.Discarded != 1 || !divaCustomBranchUnlocked(p.Map, branch2) || p.Map.AcquiredAreas != 16 {
		t.Fatal("branch changed main progress", err)
	}
	for _, id := range []uint16{58081, 58082, 58083} {
		if route, ok := divaCustomMapQuestRoute(p.Map, id); !ok || route != branch2 {
			t.Fatal("second dynamic branch coordinate not used")
		}
	}
	for _, id := range []uint16{0, 40217, 58044, 58073, 58084, 65535} {
		if _, ok := divaCustomMapQuestRoute(p.Map, id); ok {
			t.Fatal("unknown quest accepted", id)
		}
	}
	before := p.Map
	done, err := advanceDivaCustomMap(before, map[uint16]uint64{0: 27500, branch2: 5000})
	if err != nil || !done.GoalCompleted || done.Map.AcquiredAreas != 22 {
		t.Fatal("branches and main not combined", err)
	}
	if before.States[0].Nodes[20].EarnedPoints != 0 || before.States[0].Nodes[22].EarnedPoints != 0 || initial.AcquiredAreas != 0 {
		t.Fatal("progress aliased an input")
	}
	next, err := divaRandomMapNext(done.Map)
	if err != nil || !reflect.DeepEqual(next.States[1], done.Map.States[0]) {
		t.Fatal("next map did not preserve full predecessor", err)
	}
	next.States[1].Nodes[1].EarnedPoints = 1
	next.Definitions[1].Nodes[1].BaseRequiredPoints = 1
	next.Treasures[0].Quantity = 1
	if done.Map.States[0].Nodes[1].EarnedPoints == 1 || done.Map.Definitions[0].Nodes[1].BaseRequiredPoints == 1 || done.Map.Treasures[0].Quantity == 1 {
		t.Fatal("next map aliased predecessor slices")
	}
	if _, err := advanceDivaCustomMap(done.Map, map[uint16]uint64{branch1: math.MaxUint64, branch2: math.MaxUint64}); err == nil {
		t.Fatal("discard overflow accepted")
	}
}

func TestDivaRandomMapRejectsTamperAndOverflow(t *testing.T) {
	for _, seed := range []uint64{0, math.MaxInt64 + 1, math.MaxUint64} {
		if _, err := divaRandomMapInitial(seed); err == nil {
			t.Fatal("invalid seed accepted")
		}
	}
	mustRandomDivaMap(t, math.MaxInt64)
	for _, change := range []func(*DivaInterceptionMap){
		func(m *DivaInterceptionMap) { m.RulesVersion = "" },
		func(m *DivaInterceptionMap) { m.RulesVersion = "future-version" },
		func(m *DivaInterceptionMap) { m.GenerationSeed++ },
		func(m *DivaInterceptionMap) { m.GenerationSeed = 0 },
		func(m *DivaInterceptionMap) { m.AcquiredAreas = 1 },
		func(m *DivaInterceptionMap) { m.AcquiredAreas = math.MaxInt32 + 1 },
		func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[1].BaseRequiredPoints-- },
		func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[1].Unknown02 = 1 },
		func(m *DivaInterceptionMap) { m.States[0].Nodes[1].RequiredPoints++ },
		func(m *DivaInterceptionMap) { m.States[0].Nodes[1].Unknown21 = 1 },
		func(m *DivaInterceptionMap) { m.States[0].Nodes[21].BranchQuests[0] = 58084 },
		func(m *DivaInterceptionMap) { m.States[0].Nodes[6].BranchStartCoordinate = 0 },
		func(m *DivaInterceptionMap) { m.States[0].MapNumber++ },
		func(m *DivaInterceptionMap) { m.Definitions[0].NextTemplateID = 1 },
		func(m *DivaInterceptionMap) { m.Treasures[0].Quantity++ },
	} {
		m := mustRandomDivaMap(t, 1)
		change(&m)
		if validateDivaCustomMap(m) == nil {
			t.Fatal("modified v2 accepted")
		}
		if _, err := divaCustomMapNext(m); err == nil {
			t.Fatal("modified next map accepted")
		}
		if _, ok := divaCustomMapQuestRoute(m, 58043); ok {
			t.Fatal("modified route accepted")
		}
	}
	if _, err := divaRandomMapNext(mustRandomDivaMap(t, 1)); err == nil {
		t.Fatal("incomplete goal advanced")
	}
	legacy := mustCustomDivaMap(t)
	legacy.GenerationSeed = 1
	if validateDivaCustomMap(legacy) == nil {
		t.Fatal("v1 accepted v2 metadata")
	}
	// Independently reconstruct the last two catalogs; no 65,535-map replay.
	last, err := divaRandomMapAt(1, math.MaxUint16)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := divaRandomMapAt(1, math.MaxUint16-1)
	if err != nil {
		t.Fatal(err)
	}
	prior.Definitions[0].NextTemplateID = math.MaxUint16
	last.Definitions = append(last.Definitions, prior.Definitions[0])
	last.States = append(last.States, prior.States[0])
	for i := range last.States {
		for j := 1; j <= 20; j++ {
			last.States[i].Nodes[j].EarnedPoints = last.States[i].Nodes[j].RequiredPoints
		}
	}
	last.AcquiredAreas = math.MaxUint16 * 20
	if err := validateDivaRandomMap(last); err != nil {
		t.Fatal("last valid map rejected", err)
	}
	if _, err := divaCustomMapNext(last); err == nil {
		t.Fatal("map number wrapped")
	}
}
