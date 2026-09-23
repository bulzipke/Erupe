package channelserver

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	cfg "erupe-ce/config"
)

// These are synthetic policy selections over the approved custom catalog, not
// evidence for any retail placement frequency or red-treasure reward table.
func TestDivaMapSpecialTreasureNoSelectionPreservesNativePayload(t *testing.T) {
	v1, err := divaCustomMapInitial()
	if err != nil {
		t.Fatal(err)
	}
	v2, err := divaRandomMapInitial(7)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []DivaInterceptionMap{v1, v2} {
		before, err := divaInterceptionMapPayload(m)
		if err != nil {
			t.Fatal(err)
		}
		after, err := divaMapSpecialTreasurePayload(m, nil)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("no-selection payload changed: %v", err)
		}
		projection, err := projectDivaMapSpecialTreasures(m, nil)
		if err != nil {
			t.Fatal(err)
		}
		projection.Definitions[0].Nodes[21].TreasureMode = 1
		projection.States[0].Nodes[1].EarnedPoints = 99
		projection.Treasures[0].Quantity = 99
		unchanged, err := divaInterceptionMapPayload(m)
		if err != nil || !bytes.Equal(before, unchanged) {
			t.Fatal("no-selection projection aliases stored map")
		}
	}
}

func TestDivaMapSpecialTreasureDeterministicPolicy(t *testing.T) {
	groups := map[uint32]int{}
	for seed := uint64(1); seed <= 1000; seed++ {
		m, err := divaRandomMapInitial(seed)
		if err != nil {
			t.Fatal(err)
		}
		one, err := selectDivaMapSpecialTreasures(m, cfg.DivaMapRedTreasureRandomOne)
		if err != nil || len(one) != 1 || one[0].MapNumber != 1 {
			t.Fatalf("selection=%v err=%v", one, err)
		}
		groups[one[0].GroupID]++
		again, err := selectDivaMapSpecialTreasures(m, cfg.DivaMapRedTreasureRandomOne)
		if err != nil || !reflect.DeepEqual(one, again) {
			t.Fatal("selection rerolled")
		}
		all, err := selectDivaMapSpecialTreasures(m, cfg.DivaMapRedTreasureAll)
		if err != nil || len(all) != 2 || all[0].GroupID == all[1].GroupID {
			t.Fatal("all policy did not select each branch")
		}
		for i := 1; i <= 20; i++ {
			m.States[0].Nodes[i].EarnedPoints = m.States[0].Nodes[i].RequiredPoints
		}
		m.AcquiredAreas = 20
		next, err := divaRandomMapNext(m)
		if err != nil {
			t.Fatal(err)
		}
		paged, err := selectDivaMapSpecialTreasures(next, cfg.DivaMapRedTreasureRandomOne)
		if err != nil || len(paged) != 2 || paged[1] != one[0] {
			t.Fatal("previous-page policy rerolled")
		}
	}
	if len(groups) != 2 {
		t.Fatalf("deterministic choice never selected both groups: %v", groups)
	}
	if got, err := selectDivaMapSpecialTreasures(DivaInterceptionMap{}, cfg.DivaMapRedTreasureOff); err != nil || got != nil {
		t.Fatal("off requires no new generation state")
	}
	if _, err := selectDivaMapSpecialTreasures(DivaInterceptionMap{}, "unknown"); err == nil {
		t.Fatal("accepted unknown policy")
	}
}

func TestDivaMapSpecialTreasureHandlerFallsBackOnPolicyFailure(t *testing.T) {
	s, r, _ := newDivaMapHandlerSession(t)
	r.view.SpecialTreasureError = errors.New("optional presentation policy unavailable")
	want, err := divaInterceptionMapPayload(r.view.Map)
	if err != nil {
		t.Fatal(err)
	}
	handleDivaMapQuery(s, 11, false)
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || !ack.IsBufferResponse || !bytes.Equal(ack.Payload, want) {
		t.Fatal("policy failure blocked ordinary map")
	}
}

// Reproduce only the verified HD 103b96e0 state predicate. This does not assert
// an unobserved texture color or replace an in-game rendering test.
func syntheticDivaTreasureIcon(display uint8, acquired bool) int {
	icon := 1
	if display > 1 {
		icon++
	}
	if acquired {
		icon += 2
	}
	return icon
}

func TestDivaMapSpecialTreasurePreviewLifecycleAndWireOffsets(t *testing.T) {
	for _, progress := range []uint32{0, 4999, 5000} {
		m, err := divaRandomMapInitial(1)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 5; i++ {
			m.States[0].Nodes[i].EarnedPoints = m.States[0].Nodes[i].RequiredPoints
		}
		m.AcquiredAreas = 5
		m.States[0].Nodes[21].EarnedPoints = progress
		if progress == 5000 {
			m.AcquiredAreas++
		}
		before := cloneDivaMapPresentation(m)
		selection := []DivaMapSpecialTreasureSelection{{MapNumber: 1, GroupID: 1}}
		projection, err := projectDivaMapSpecialTreasures(m, selection)
		if err != nil {
			t.Fatal(err)
		}
		wantHidden := uint8(1)
		wantIcon := 2
		if progress == 5000 {
			wantHidden, wantIcon = 0, 4
		}
		if projection.Definitions[0].Nodes[21].TreasureMode != wantHidden ||
			projection.Definitions[0].Nodes[22].TreasureMode != 0 {
			t.Fatalf("incorrect hidden state for progress %d", progress)
		}
		for i, row := range projection.Treasures {
			wantMode := uint8(1)
			if row.GroupID == 1 {
				wantMode = 2
				if syntheticDivaTreasureIcon(row.DisplayMode, progress == 5000) != wantIcon {
					t.Fatal("native acquired icon transition changed")
				}
			}
			if row.DisplayMode != wantMode || row.MinMap != 1 || row.MaxMap != 1 ||
				row.ItemType != m.Treasures[i].ItemType || row.ItemID != m.Treasures[i].ItemID || row.Quantity != m.Treasures[i].Quantity {
				t.Fatalf("treasure presentation altered reward: %+v", row)
			}
		}
		if !reflect.DeepEqual(projection.States, m.States) || projection.AcquiredAreas != m.AcquiredAreas ||
			!reflect.DeepEqual(m, before) {
			t.Fatal("projection changed progress or persisted input")
		}
		payload, err := divaMapSpecialTreasurePayload(m, selection)
		if err != nil {
			t.Fatal(err)
		}
		const hiddenOffset = 2 + 8 + 21*23 + 18
		const firstTreasureModeOffset = 2 + 8 + 64*23 + 2 + 13
		if payload[hiddenOffset] != wantHidden || payload[firstTreasureModeOffset] != 2 {
			t.Fatal("native wire flag offsets changed")
		}
		if err := validateDivaRandomMap(projection); err == nil {
			t.Fatal("wire-only projection was accepted as canonical persisted v2")
		}
		projection.Definitions[0].Nodes[21].Coordinate = 999
		projection.States[0].Nodes[21].EarnedPoints = 999
		projection.Treasures[0].Quantity = 999
		if !reflect.DeepEqual(m, before) {
			t.Fatal("selected projection aliases stored map")
		}
	}
}

func TestDivaMapSpecialTreasureIndependentPreviousPage(t *testing.T) {
	m, err := divaRandomMapInitial(3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 21; i++ {
		m.States[0].Nodes[i].EarnedPoints = m.States[0].Nodes[i].RequiredPoints
	}
	m.AcquiredAreas = 21
	m.States[0].Nodes[22].EarnedPoints = 4999
	m, err = divaRandomMapNext(m)
	if err != nil {
		t.Fatal(err)
	}
	selection := []DivaMapSpecialTreasureSelection{{MapNumber: 1, GroupID: 1}, {MapNumber: 2, GroupID: 2}}
	p, err := projectDivaMapSpecialTreasures(m, selection)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Treasures) != 8 || p.Definitions[0].Nodes[22].TreasureMode != 1 ||
		p.Definitions[0].Nodes[21].TreasureMode != 0 || p.Definitions[1].Nodes[21].TreasureMode != 0 ||
		p.Definitions[1].Nodes[22].TreasureMode != 0 {
		t.Fatal("previous/current treasure presentation interfered")
	}
	for _, state := range m.States {
		for _, group := range []uint32{1, 2} {
			count := 0
			for _, row := range p.Treasures {
				if row.GroupID != group || state.MapNumber < row.MinMap || state.MapNumber > row.MaxMap {
					continue
				}
				count++
				want := uint8(1)
				if (state.MapNumber == 1 && group == 1) || (state.MapNumber == 2 && group == 2) {
					want = 2
				}
				if row.DisplayMode != want {
					t.Fatal("treasure group leaked across page ranges")
				}
			}
			if count != 2 {
				t.Fatalf("map %d group %d matched %d reward rows", state.MapNumber, group, count)
			}
		}
	}
	if !reflect.DeepEqual(p.States, m.States) {
		t.Fatal("previous progress changed")
	}
}

func TestDivaMapSpecialTreasureRejectsInvalidOrUnapprovedInput(t *testing.T) {
	tests := []struct {
		name      string
		selection []DivaMapSpecialTreasureSelection
		mutate    func(*DivaInterceptionMap)
	}{
		{"zero group", []DivaMapSpecialTreasureSelection{{1, 0}}, nil},
		{"missing group", []DivaMapSpecialTreasureSelection{{1, 9}}, nil},
		{"zero map", []DivaMapSpecialTreasureSelection{{0, 1}}, nil},
		{"not visible map", []DivaMapSpecialTreasureSelection{{2, 1}}, nil},
		{"duplicate", []DivaMapSpecialTreasureSelection{{1, 1}, {1, 1}}, nil},
		{"excessive", []DivaMapSpecialTreasureSelection{{1, 1}, {1, 2}, {1, 3}}, nil},
		{"reward tampering", []DivaMapSpecialTreasureSelection{{1, 1}}, func(m *DivaInterceptionMap) { m.Treasures[0].Quantity++ }},
		{"orphaned mode", nil, func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[1].TreasureMode = 1 }},
		{"unexamined mode", nil, func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[21].TreasureMode = 2 }},
		{"unexamined icon", nil, func(m *DivaInterceptionMap) { m.Treasures[0].DisplayMode = 3 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := divaRandomMapInitial(9)
			if err != nil {
				t.Fatal(err)
			}
			if tt.mutate != nil {
				tt.mutate(&m)
			}
			payload, err := divaMapSpecialTreasurePayload(m, tt.selection)
			if err == nil || payload != nil {
				t.Fatal("invalid input produced a native success payload")
			}
		})
	}
	v1, err := divaCustomMapInitial()
	if err != nil {
		t.Fatal(err)
	}
	if payload, err := divaMapSpecialTreasurePayload(v1, []DivaMapSpecialTreasureSelection{{1, 1}}); err == nil || payload != nil {
		t.Fatal("existing shared-template v1 opted into a new policy")
	}
}
