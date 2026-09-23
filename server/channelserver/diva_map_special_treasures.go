package channelserver

import (
	"fmt"
	"slices"

	cfg "erupe-ce/config"
)

// DivaMapSpecialTreasureSelection is an explicit, persisted-policy input to a
// presentation-only projection. It does not choose a location, add a treasure,
// change rewards, or grant eligibility. MapNumber prevents a choice for a new
// map from silently changing the previous map's treasure presentation.
type DivaMapSpecialTreasureSelection struct {
	MapNumber uint16
	GroupID   uint32
}

// This deterministic choice is an explicit server operating policy, NOT a
// recovered retail distribution. Its independent domain never consumes or
// changes the v2 geometry generator's PRNG stream.
func selectDivaMapSpecialTreasures(m DivaInterceptionMap, mode string) ([]DivaMapSpecialTreasureSelection, error) {
	if err := cfg.ValidateDivaMapRedTreasureMode(mode); err != nil {
		return nil, err
	}
	if mode == cfg.DivaMapRedTreasureOff {
		return nil, nil
	}
	if err := validateDivaRandomMap(m); err != nil {
		return nil, err
	}
	var selections []DivaMapSpecialTreasureSelection
	for _, state := range m.States {
		if mode == cfg.DivaMapRedTreasureRandomOne {
			rng := divaRandomMapRNG(m.GenerationSeed, state.MapNumber, 0x94d049bb7ed1da7b)
			branch := divaCustomBranches[rng.next()%uint64(len(divaCustomBranches))]
			selections = append(selections, DivaMapSpecialTreasureSelection{state.MapNumber, branch.Group})
		} else {
			for _, branch := range divaCustomBranches {
				selections = append(selections, DivaMapSpecialTreasureSelection{state.MapNumber, branch.Group})
			}
		}
	}
	return selections, nil
}

func cloneDivaMapPresentation(m DivaInterceptionMap) DivaInterceptionMap {
	m.Treasures = slices.Clone(m.Treasures)
	m.Definitions = slices.Clone(m.Definitions)
	for i := range m.Definitions {
		m.Definitions[i].Nodes = slices.Clone(m.Definitions[i].Nodes)
	}
	m.States = slices.Clone(m.States)
	for i := range m.States {
		m.States[i].Nodes = slices.Clone(m.States[i].Nodes)
	}
	return m
}

// projectDivaMapSpecialTreasures preserves the canonical stored map. Its result
// is for native serialization only, not persistence or reward validation.
//
// HD 103b96e0 uses DisplayMode > 1 for the second treasure icon pair and changes
// the icon when remaining points become zero. 103a90b0 independently suppresses
// item names whenever TreasureMode != 0; it does NOT automatically reveal them
// after capture. Clear that flag on an acquired area, not on personal claiming.
// The official manual describes red treasure as the hidden-content variant.
// Associating it with the second icon pair is an inference; exact sprite colors
// and final in-game display still require client verification.
// This is UI concealment: reward rows remain on the wire, not encrypted secrets.
func projectDivaMapSpecialTreasures(m DivaInterceptionMap, selections []DivaMapSpecialTreasureSelection) (DivaInterceptionMap, error) {
	if err := validateDivaInterceptionMap(m); err != nil {
		return DivaInterceptionMap{}, err
	}
	if len(selections) == 0 {
		return cloneDivaMapPresentation(m), nil
	}
	// v1 shares one definition between both pages, so its per-page hidden flag
	// cannot be represented independently. Existing v1/v2 maps remain untouched;
	// v2 requires an explicit policy; v3 selects its own approved tiered policy.
	if m.RulesVersion != divaRandomMapRules && m.RulesVersion != divaProgressiveMapRules {
		return DivaInterceptionMap{}, fmt.Errorf("diva special treasure: versioned random map required")
	}
	if err := validateDivaCustomMap(m); err != nil {
		return DivaInterceptionMap{}, fmt.Errorf("diva special treasure: invalid versioned map: %w", err)
	}
	if len(selections) > len(m.States)*len(divaCustomBranches) {
		return DivaInterceptionMap{}, fmt.Errorf("diva special treasure: excessive selections")
	}
	selected := make(map[DivaMapSpecialTreasureSelection]bool, len(selections))
	for _, selection := range selections {
		if selection.MapNumber == 0 || selection.GroupID == 0 || selected[selection] {
			return DivaInterceptionMap{}, fmt.Errorf("diva special treasure: invalid/duplicate selection")
		}
		found := 0
		for i, state := range m.States {
			if state.MapNumber != selection.MapNumber {
				continue
			}
			for j, node := range m.Definitions[i].Nodes {
				if node.TreasureGroupID == selection.GroupID && state.Nodes[j].BranchQuests[0] != 0 {
					found++
				}
			}
		}
		if found != 1 {
			return DivaInterceptionMap{}, fmt.Errorf("diva special treasure: selection is not a unique visible-page branch")
		}
		selected[selection] = true
	}
	result := cloneDivaMapPresentation(m)
	result.Treasures = nil
	for i, state := range m.States {
		// The same group IDs recur on every map. Exact page ranges keep one
		// map's red choice from recoloring the other page. Canonical v2/v3 rows
		// cover every possible map; no reward-range fallback is invented here.
		for _, row := range m.Treasures {
			row.MinMap, row.MaxMap = state.MapNumber, state.MapNumber
			if selected[DivaMapSpecialTreasureSelection{state.MapNumber, row.GroupID}] {
				row.DisplayMode = 2
			}
			result.Treasures = append(result.Treasures, row)
		}
		for j := range result.Definitions[i].Nodes {
			node := &result.Definitions[i].Nodes[j]
			if selected[DivaMapSpecialTreasureSelection{state.MapNumber, node.TreasureGroupID}] &&
				state.Nodes[j].EarnedPoints < state.Nodes[j].RequiredPoints {
				node.TreasureMode = 1
			}
		}
	}
	if err := validateDivaInterceptionMap(result); err != nil {
		return DivaInterceptionMap{}, err
	}
	return result, nil
}

func divaMapSpecialTreasurePayload(m DivaInterceptionMap, selections []DivaMapSpecialTreasureSelection) ([]byte, error) {
	projection, err := projectDivaMapSpecialTreasures(m, selections)
	if err != nil {
		return nil, err
	}
	return divaInterceptionMapPayload(projection)
}
