package channelserver

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

// Build every intervening completed page; a standalone page >1 is not a
// valid native history and must not be used as a high-difficulty fixture.
func mustProgressiveDivaPage(t *testing.T, seed uint64, number uint16) DivaInterceptionMap {
	t.Helper()
	m, err := divaProgressiveMapInitial(seed)
	if err != nil {
		t.Fatal(err)
	}
	for page := uint16(1); page < number; page++ {
		progress, e := advanceDivaCustomMap(m, map[uint16]uint64{0: math.MaxUint64})
		if e != nil || !progress.GoalCompleted {
			t.Fatal(page, e)
		}
		m, e = divaCustomMapNext(progress.Map)
		if e != nil {
			t.Fatal(page, e)
		}
	}
	if err = validateDivaCustomMap(m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDivaProgressivePoliciesAndProbabilityDistribution(t *testing.T) {
	expected := []divaProgressivePolicy{{6, 5, 5, 10}, {5, 10, 8, 20}, {4, 20, 12, 35}, {3, 30, 16, 50}, {2, 40, 20, 65}}
	lastInitial, lastHourly, lastRed := 0, 0, 0
	const samples = 8000
	for tier, want := range expected {
		number := uint16(1 + tier*2)
		if got := divaProgressiveMapPolicy(number); got != want || divaProgressiveMapPolicy(number+1) != want {
			t.Fatal(number, got, want)
		}
		initial, hourly, red := 0, 0, 0
		for seed := uint64(1); seed <= samples; seed++ {
			if divaProgressiveInitialPercent(seed, number, 303) > 0 {
				initial++
			}
			if divaProgressiveRoll(seed, number, 303, uint16(want.Interval), 0xd6e8feb86659fd17) < want.Chance {
				hourly++
			}
			if divaProgressiveRoll(seed, number, 1, 0, 0xa0761d6478bd642f) < want.RedChance {
				red++
			}
		}
		// Deterministic sample, not a flaky random experiment. Broad 2%-point
		// tolerance guards reversed thresholds and incorrect percentage scales.
		for _, sample := range [][2]int{{initial, int(want.Chance)}, {hourly, int(want.Chance)}, {red, int(want.RedChance)}} {
			if math.Abs(float64(sample[0]*100)/samples-float64(sample[1])) > 2 {
				t.Fatal(number, sample)
			}
		}
		if tier > 0 && (initial <= lastInitial || hourly <= lastHourly || red <= lastRed) {
			t.Fatal("difficulty is not monotonic", number, initial, hourly, red)
		}
		lastInitial, lastHourly, lastRed = initial, hourly, red
	}
	if divaProgressiveMapPolicy(math.MaxUint16) != expected[4] {
		t.Fatal("uncapped page tier")
	}
	for _, tc := range []struct {
		base    uint32
		percent uint8
		want    uint32
	}{{1, 5, 2}, {3125, 5, 3282}, {11000, 100, 22000}, {0, 0, 0}} {
		if got := divaProgressiveRequired(tc.base, tc.percent); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestDivaProgressiveInitialBaselineIdentityAndWire(t *testing.T) {
	for _, seed := range []uint64{1, 17, math.MaxInt64} {
		for _, number := range []uint16{1, 3, 9} {
			m := mustProgressiveDivaPage(t, seed, number)
			normal, err := divaRandomMapAt(seed, number)
			if err != nil {
				t.Fatal(err)
			}
			if m.RulesVersion != divaProgressiveMapRules || m.GenerationSeed != seed || m.States[0].InvasionTick != 0 {
				t.Fatal("lost generation identity")
			}
			for i, node := range m.States[0].Nodes {
				base := normal.States[0].Nodes[i].RequiredPoints
				if m.Definitions[0].Nodes[i].BaseRequiredPoints != base || node.RequiredPoints != divaProgressiveRequired(base, node.Fortification) {
					t.Fatal("page difficulty mislabeled invasion", number, i)
				}
				if i == 0 || i > 20 {
					if node.Fortification != 0 {
						t.Fatal("start/branch fortified")
					}
				}
			}
			wire, err := divaInterceptionMapPayload(m)
			if err != nil {
				t.Fatal(err)
			}
			// Persistence-only metadata must not add bytes or reuse native unknowns.
			presentation := cloneDivaMapPresentation(m)
			for i := range presentation.States {
				presentation.States[i].InvasionTick = 0
				for j := range presentation.States[i].Nodes {
					presentation.States[i].Nodes[j].Fortification = 0
				}
			}
			plain, err := divaInterceptionMapPayload(presentation)
			if err != nil || !bytes.Equal(wire, plain) {
				t.Fatal("metadata leaked onto wire", err)
			}
			data, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			var restored DivaInterceptionMap
			if err = json.Unmarshal(data, &restored); err != nil || validateDivaProgressiveMap(restored) != nil || !reflect.DeepEqual(m, restored) {
				t.Fatal("JSON round-trip changed map", err)
			}
		}
	}
}

func TestDivaProgressiveTicksFreezeAndBoundedDifficulty(t *testing.T) {
	m := mustProgressiveDivaPage(t, 1, 9)
	first, second := m.States[0].Nodes[1], m.States[0].Nodes[2]
	progress, err := advanceDivaCustomMap(m, map[uint16]uint64{0: uint64(first.RequiredPoints) + uint64(second.RequiredPoints/2)})
	if err != nil {
		t.Fatal(err)
	}
	m = progress.Map
	frozen := m.States[0].Nodes[1]
	partial := m.States[0].Nodes[2].EarnedPoints
	areas := m.AcquiredAreas
	previous := m.States[1]
	changes := 0
	for tick := uint16(1); tick <= divaProgressiveMaxTicks; tick++ {
		before := cloneDivaMapPresentation(m)
		next, changed, e := applyDivaProgressiveInvasion(m, tick)
		if e != nil {
			t.Fatal(tick, e)
		}
		if !reflect.DeepEqual(before, m) {
			t.Fatal("input snapshot mutated")
		}
		if next.States[0].Nodes[1] != frozen || next.States[0].Nodes[2].EarnedPoints != partial || next.AcquiredAreas != areas || !reflect.DeepEqual(next.States[1], previous) {
			t.Fatal("invasion changed earned history", tick)
		}
		if tick%2 != 0 && len(changed) != 0 {
			t.Fatal("attack between scheduled ticks", tick)
		}
		if len(changed) > 0 {
			changes++
		}
		for i, node := range next.States[0].Nodes {
			if node.Fortification > 100 || node.RequiredPoints > next.Definitions[0].Nodes[i].BaseRequiredPoints*2 {
				t.Fatal("uncapped invasion", tick, node)
			}
			if node.Fortification < before.States[0].Nodes[i].Fortification || node.EarnedPoints != before.States[0].Nodes[i].EarnedPoints {
				t.Fatal("regression or invented contribution")
			}
		}
		again, repeat, e := applyDivaProgressiveInvasion(next, tick)
		if e != nil || len(repeat) != 0 || !reflect.DeepEqual(again, next) {
			t.Fatal("same tick rerolled", tick, e)
		}
		if _, _, e = applyDivaProgressiveInvasion(next, tick+2); e == nil {
			t.Fatal("skipped tick accepted")
		}
		if _, _, e = applyDivaProgressiveInvasion(next, tick-1); e == nil {
			t.Fatal("backward tick accepted")
		}
		m = next
	}
	if changes == 0 {
		t.Fatal("fixture never attacked")
	}
	if _, _, err = applyDivaProgressiveInvasion(m, divaProgressiveMaxTicks+1); err == nil {
		t.Fatal("age limit exceeded")
	}
	capReached := false
	for _, node := range m.States[0].Nodes[2:21] {
		capReached = capReached || node.Fortification == 100
	}
	if !capReached {
		t.Fatal("high-tier fixture never reached the 100% cap")
	}
	// Earned may legitimately exceed the ordinary baseline while still being
	// below the reinforced requirement. Normalization must not count that tile
	// as captured, discard its partial points, or reject the stored snapshot.
	base := m.Definitions[0].Nodes[2].BaseRequiredPoints
	if m.States[0].Nodes[2].RequiredPoints <= base+1 {
		t.Fatal("fixture frontier was not reinforced")
	}
	progress, err = advanceDivaCustomMap(m, map[uint16]uint64{0: uint64(base + 1 - m.States[0].Nodes[2].EarnedPoints)})
	if err != nil || progress.Map.States[0].Nodes[2].EarnedPoints != base+1 || progress.Map.AcquiredAreas != areas {
		t.Fatal("reinforced partial point normalization", err)
	}
	if err = validateDivaProgressiveMap(progress.Map); err != nil {
		t.Fatal(err)
	}
}

func TestDivaProgressiveNextPageResetsOnlyCurrentClock(t *testing.T) {
	m := mustProgressiveDivaPage(t, 17, 1)
	var err error
	for tick := uint16(1); tick <= 18; tick++ {
		m, _, err = applyDivaProgressiveInvasion(m, tick)
		if err != nil {
			t.Fatal(err)
		}
	}
	progress, err := advanceDivaCustomMap(m, map[uint16]uint64{0: math.MaxUint64})
	if err != nil || !progress.GoalCompleted {
		t.Fatal(err)
	}
	before := cloneDivaMapPresentation(progress.Map)
	next, err := divaCustomMapNext(progress.Map)
	if err != nil {
		t.Fatal(err)
	}
	if next.States[0].InvasionTick != 0 || next.States[0].MapNumber != 2 || !reflect.DeepEqual(next.States[1], before.States[0]) || next.AcquiredAreas != before.AcquiredAreas {
		t.Fatal("page transition changed old progress")
	}
	if !reflect.DeepEqual(progress.Map, before) {
		t.Fatal("page transition mutated input")
	}
	newPage, _, err := applyDivaProgressiveInvasion(next, 1)
	if err != nil || !reflect.DeepEqual(newPage.States[1], before.States[0]) {
		t.Fatal("next page tick touched previous page", err)
	}
	newPage.States[1].Nodes[1].Fortification = 99
	newPage.Definitions[1].Nodes[1].BaseRequiredPoints = 1
	if !reflect.DeepEqual(progress.Map, before) {
		t.Fatal("previous page aliases old snapshot")
	}
}

func TestDivaProgressiveTamperingAndLegacyIsolation(t *testing.T) {
	m := mustProgressiveDivaPage(t, 1, 3)
	for name, mutate := range map[string]func(*DivaInterceptionMap){
		"wrong version":    func(m *DivaInterceptionMap) { m.RulesVersion = divaRandomMapRules },
		"zero seed":        func(m *DivaInterceptionMap) { m.GenerationSeed = 0 },
		"seed overflow":    func(m *DivaInterceptionMap) { m.GenerationSeed = math.MaxInt64 + 1 },
		"age overflow":     func(m *DivaInterceptionMap) { m.States[0].InvasionTick = 169 },
		"changed required": func(m *DivaInterceptionMap) { m.States[0].Nodes[1].RequiredPoints++ },
		"changed baseline": func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[1].BaseRequiredPoints++ },
		"forged reinforcement": func(m *DivaInterceptionMap) {
			m.States[0].Nodes[1].Fortification = 100
			m.States[0].Nodes[1].RequiredPoints = divaProgressiveRequired(m.Definitions[0].Nodes[1].BaseRequiredPoints, 100)
		},
		"branch reinforcement": func(m *DivaInterceptionMap) {
			m.States[0].Nodes[21].Fortification = 8
			m.States[0].Nodes[21].RequiredPoints = divaProgressiveRequired(m.Definitions[0].Nodes[21].BaseRequiredPoints, 8)
		},
		"changed quest":       func(m *DivaInterceptionMap) { m.States[0].Nodes[21].BranchQuests[0]++ },
		"changed reward":      func(m *DivaInterceptionMap) { m.Treasures[0].Quantity++ },
		"forged areas":        func(m *DivaInterceptionMap) { m.AcquiredAreas = 0 },
		"missing predecessor": func(m *DivaInterceptionMap) { m.States = m.States[:1]; m.Definitions = m.Definitions[:1] },
		"previous incomplete": func(m *DivaInterceptionMap) { m.States[1].Nodes[20].EarnedPoints-- },
	} {
		t.Run(name, func(t *testing.T) {
			v := cloneDivaMapPresentation(m)
			mutate(&v)
			if validateDivaProgressiveMap(v) == nil {
				t.Fatal("tampered v3 accepted")
			}
		})
	}
	v1, err := divaCustomMapInitial()
	if err != nil {
		t.Fatal(err)
	}
	v2, err := divaRandomMapInitial(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []DivaInterceptionMap{v1, v2} {
		legacy.States[0].InvasionTick = 1
		if validateDivaCustomMap(legacy) == nil {
			t.Fatal("legacy clock accepted")
		}
		legacy.States[0].InvasionTick = 0
		legacy.States[0].Nodes[1].Fortification = 1
		if validateDivaCustomMap(legacy) == nil {
			t.Fatal("legacy reinforcement accepted")
		}
	}
}

func TestDivaProgressiveTreasurePreviewPreservesPayouts(t *testing.T) {
	m := mustProgressiveDivaPage(t, 17, 9)
	chosen, err := selectDivaProgressiveTreasures(m)
	if err != nil {
		t.Fatal(err)
	}
	again, err := selectDivaProgressiveTreasures(m)
	if err != nil || !reflect.DeepEqual(chosen, again) {
		t.Fatal("treasure rerolled")
	}
	for _, selection := range chosen {
		if selection.MapNumber != 9 && selection.MapNumber != 8 {
			t.Fatal(selection)
		}
	}
	// Force an explicit selection to exercise both presentation states, without
	// depending on whether this particular deterministic seed picks branch 1.
	selection := []DivaMapSpecialTreasureSelection{{MapNumber: 9, GroupID: 1}}
	var unlock uint64
	for i := 1; i <= 5; i++ {
		unlock += uint64(m.States[0].Nodes[i].RequiredPoints)
	}
	progress, err := advanceDivaCustomMap(m, map[uint16]uint64{0: unlock})
	if err != nil {
		t.Fatal(err)
	}
	m = progress.Map
	branch := m.States[0].Nodes[21]
	for _, amount := range []uint64{uint64(branch.RequiredPoints - 1), 1} {
		progress, err = advanceDivaCustomMap(m, map[uint16]uint64{branch.Coordinate: amount})
		if err != nil {
			t.Fatal(err)
		}
		m = progress.Map
		before := cloneDivaMapPresentation(m)
		projection, e := projectDivaMapSpecialTreasures(m, selection)
		if e != nil {
			t.Fatal(e)
		}
		want := uint8(1)
		if m.States[0].Nodes[21].EarnedPoints == branch.RequiredPoints {
			want = 0
		}
		if projection.Definitions[0].Nodes[21].TreasureMode != want || projection.Definitions[1].Nodes[21].TreasureMode != 0 {
			t.Fatal("wrong page hidden/revealed")
		}
		if !reflect.DeepEqual(projection.States, m.States) || !reflect.DeepEqual(before, m) || projection.AcquiredAreas != m.AcquiredAreas {
			t.Fatal("projection mutated progress")
		}
		for i, row := range projection.Treasures {
			original := m.Treasures[i%len(m.Treasures)]
			if row.ItemType != original.ItemType || row.ItemID != original.ItemID || row.Quantity != original.Quantity || row.GroupID != original.GroupID {
				t.Fatal("presentation changed rewards")
			}
			wantMode := uint8(1)
			if row.MinMap == 9 && row.GroupID == 1 {
				wantMode = 2
			}
			if row.DisplayMode != wantMode || row.MinMap != row.MaxMap {
				t.Fatal("page selection changed", row)
			}
		}
		if _, e = divaMapSpecialTreasurePayload(m, selection); e != nil {
			t.Fatal(e)
		}
		if validateDivaProgressiveMap(projection) == nil {
			t.Fatal("wire projection accepted as stored canonical map")
		}
	}
}
