package channelserver

import (
	"fmt"
	"math"
	"slices"
)

const divaCustomMapRules = "custom-v1"

// Server-custom v1: twenty main areas, two optional single-node branches.
// These are approved operating values, not a recovered round-40 map.
var divaCustomMainCoordinates = [...]uint16{301, 302, 303, 304, 305, 306, 307, 308, 309, 310, 311, 211, 210, 209, 208, 207, 206, 205, 204, 203, 202}
var divaCustomBranches = [...]struct {
	Junction   int
	Coordinate uint16
	Quests     [3]uint16
	Group      uint32
}{
	{6, 407, [3]uint16{58079, 58080, 0}, 1},
	{16, 106, [3]uint16{58081, 58082, 58083}, 2},
}

// Explicit quest_type=46/48 rows in the supplied EventQuests.sql. Branch
// quest_type=47 is 58079..58083, corroborated by the original quest titles.
// Do not turn arbitrary IDs in the broad interception packet range into main
// contributions. Quest loading/departure validation is still required.
func divaCustomMainQuest(id uint16) bool {
	return id == 58043 || (id >= 58050 && id <= 58072) ||
		(id >= 58074 && id <= 58078) || (id >= 58088 && id <= 58091) ||
		(id >= 58096 && id <= 58099) || (id >= 58101 && id <= 58109) ||
		(id >= 58112 && id <= 58115) || (id >= 58118 && id <= 58123) ||
		(id >= 58125 && id <= 58128)
}

func divaCustomMapInitial() (DivaInterceptionMap, error) { return divaCustomMapAt(1) }

func divaCustomMapAt(number uint16) (DivaInterceptionMap, error) {
	if number == 0 {
		return DivaInterceptionMap{}, fmt.Errorf("diva map: zero map number")
	}
	factor := uint32(4 + min(number-1, 4))
	definition := DivaInterceptionMapDefinition{ID: 1, NextTemplateID: 1}
	state := DivaInterceptionMapState{TemplateID: 1, MapNumber: number}
	for index, coordinate := range divaCustomMainCoordinates {
		base, kind := uint32(0), uint8(0)
		if index == 0 {
			kind = 1
		} else {
			base = 2500 + uint32((index-1)/5)*1000
		}
		if index == len(divaCustomMainCoordinates)-1 {
			kind = 2
		}
		node := DivaInterceptionMapNodeState{Coordinate: coordinate, Ordinal: uint8(index + 1), RequiredPoints: base * factor / 4}
		if index+1 < len(divaCustomMainCoordinates) {
			node.NextCoordinate = divaCustomMainCoordinates[index+1]
		}
		definition.Nodes = append(definition.Nodes, DivaInterceptionMapNodeDefinition{Coordinate: coordinate, Kind: kind, BaseRequiredPoints: base})
		state.Nodes = append(state.Nodes, node)
	}
	m := DivaInterceptionMap{}
	for _, branch := range divaCustomBranches {
		definition.Nodes[branch.Junction].Kind = 3
		state.Nodes[branch.Junction].BranchStartCoordinate = branch.Coordinate
		definition.Nodes = append(definition.Nodes, DivaInterceptionMapNodeDefinition{
			Coordinate: branch.Coordinate, BaseRequiredPoints: 5000, TreasureGroupID: branch.Group,
		})
		state.Nodes = append(state.Nodes, DivaInterceptionMapNodeState{
			Coordinate: branch.Coordinate, RequiredPoints: 5000 * factor / 4, Ordinal: uint8(len(state.Nodes) + 1), BranchQuests: branch.Quests,
		})
		m.Treasures = append(m.Treasures,
			DivaInterceptionMapTreasure{GroupID: branch.Group, ItemType: 7, ItemID: 1026, Quantity: 5, MinMap: 1, MaxMap: math.MaxUint16, DisplayMode: 1},
			DivaInterceptionMapTreasure{GroupID: branch.Group, ItemType: 26, Quantity: 500, MinMap: 1, MaxMap: math.MaxUint16, DisplayMode: 1})
	}
	m.Definitions = []DivaInterceptionMapDefinition{definition}
	m.States = []DivaInterceptionMapState{state}
	// A repeated map needs the real completed predecessor. Only number 1 can
	// stand alone; callers of this constructor attach the predecessor below.
	if number == 1 {
		if err := validateDivaInterceptionMap(m); err != nil {
			return DivaInterceptionMap{}, err
		}
	}
	return m, nil
}

func divaCustomMapNext(previous DivaInterceptionMap) (DivaInterceptionMap, error) {
	if previous.RulesVersion == divaProgressiveMapRules {
		return divaProgressiveMapNext(previous)
	}
	if previous.RulesVersion == divaRandomMapRules {
		return divaRandomMapNext(previous)
	}
	if err := validateDivaCustomMap(previous); err != nil {
		return DivaInterceptionMap{}, err
	}
	state := previous.States[0]
	if state.MapNumber == math.MaxUint16 {
		return DivaInterceptionMap{}, fmt.Errorf("diva map number exhausted")
	}
	goal := state.Nodes[len(divaCustomMainCoordinates)-1]
	if goal.Coordinate != divaCustomMainCoordinates[len(divaCustomMainCoordinates)-1] || goal.EarnedPoints != goal.RequiredPoints {
		return DivaInterceptionMap{}, fmt.Errorf("diva map: current goal is incomplete")
	}
	next, err := divaCustomMapAt(state.MapNumber + 1)
	if err != nil {
		return DivaInterceptionMap{}, err
	}
	state.Nodes = append([]DivaInterceptionMapNodeState(nil), state.Nodes...)
	next.States = append(next.States, state)
	next.AcquiredAreas = previous.AcquiredAreas
	return next, validateDivaInterceptionMap(next)
}

func divaCustomBranchUnlocked(m DivaInterceptionMap, coordinate uint16) bool {
	if len(m.States) == 0 {
		return false
	}
	nodes := m.States[0].Nodes
	if coordinate == 0 {
		return false
	}
	for _, junction := range nodes {
		if junction.BranchStartCoordinate != coordinate {
			continue
		}
		for _, predecessor := range nodes {
			if predecessor.NextCoordinate == junction.Coordinate {
				return predecessor.RequiredPoints > 0 && predecessor.EarnedPoints == predecessor.RequiredPoints
			}
		}
	}
	return false
}

func divaCustomMapQuestRoute(m DivaInterceptionMap, questID uint16) (uint16, bool) {
	if err := validateDivaCustomMap(m); err != nil {
		return 0, false
	}
	if divaCustomMainQuest(questID) {
		return 0, true
	}
	for _, branch := range m.States[0].Nodes {
		for _, quest := range branch.BranchQuests {
			if quest != 0 && quest == questID {
				return branch.Coordinate, divaCustomBranchUnlocked(m, branch.Coordinate)
			}
		}
	}
	return 0, false
}

type divaCustomMapProgress struct {
	Map               DivaInterceptionMap
	Awarded           []uint16
	CompletedBranches []uint16
	Discarded         uint64
	GoalCompleted     bool
}

// Settle one map bucket. Branches unlocked before this update receive their
// own points first; main points never pay a branch. The repository preserves
// departure attribution and deduplicates contributions before invoking this.
func advanceDivaCustomMap(m DivaInterceptionMap, points map[uint16]uint64) (divaCustomMapProgress, error) {
	if err := validateDivaCustomMap(m); err != nil {
		return divaCustomMapProgress{}, err
	}
	cloned, err := advanceDivaMainMap(m, 0)
	if err != nil {
		return divaCustomMapProgress{}, err
	}
	result := divaCustomMapProgress{Map: cloned.Map}
	nodes := result.Map.States[0].Nodes
	for route, amount := range points {
		if route == 0 {
			continue
		}
		index := -1
		for i, node := range nodes {
			if node.Coordinate == route && node.BranchQuests[0] != 0 {
				index = i
				break
			}
		}
		if index < 0 {
			return divaCustomMapProgress{}, fmt.Errorf("diva map: unknown branch route %d", route)
		}
		if !divaCustomBranchUnlocked(m, route) {
			return divaCustomMapProgress{}, fmt.Errorf("diva map: locked branch route %d", route)
		}
		node := &nodes[index]
		needed := uint64(node.RequiredPoints - node.EarnedPoints)
		used := min(amount, needed)
		node.EarnedPoints += uint32(used)
		if amount-used > math.MaxUint64-result.Discarded {
			return divaCustomMapProgress{}, fmt.Errorf("diva map discarded point overflow")
		}
		result.Discarded += amount - used
		if needed > 0 && used == needed {
			result.Awarded = append(result.Awarded, route)
			result.CompletedBranches = append(result.CompletedBranches, route)
		}
	}
	if uint64(result.Map.AcquiredAreas)+uint64(len(result.CompletedBranches)) > math.MaxInt32 {
		return divaCustomMapProgress{}, fmt.Errorf("diva map area total overflow")
	}
	result.Map.AcquiredAreas += uint32(len(result.CompletedBranches))
	main, err := advanceDivaMainMap(result.Map, points[0])
	if err != nil {
		return divaCustomMapProgress{}, err
	}
	for i, old := range m.States[0].Nodes {
		if old.BranchQuests[0] != 0 || old.RequiredPoints == 0 {
			continue
		}
		if old.EarnedPoints < old.RequiredPoints && main.Map.States[0].Nodes[i].EarnedPoints == old.RequiredPoints {
			result.Awarded = append(result.Awarded, old.Coordinate)
		}
	}
	if main.Discarded > math.MaxUint64-result.Discarded {
		return divaCustomMapProgress{}, fmt.Errorf("diva map discarded point overflow")
	}
	result.Discarded += main.Discarded
	result.Map, result.GoalCompleted = main.Map, main.GoalCompleted
	slices.Sort(result.Awarded)
	slices.Sort(result.CompletedBranches)
	return result, nil
}

// The generic wire validator permits other valid native maps. The persisted
// custom-v1 rules must additionally retain their exact geometry and costs;
// otherwise fixed junction indices or a changed treasure table could silently
// reinterpret an already bound departure.
func validateDivaCustomMap(m DivaInterceptionMap) error {
	if m.RulesVersion == divaProgressiveMapRules {
		return validateDivaProgressiveMap(m)
	}
	if m.RulesVersion == divaRandomMapRules {
		return validateDivaRandomMap(m)
	}
	if m.RulesVersion != "" || m.GenerationSeed != 0 {
		return fmt.Errorf("diva custom map: unsupported version or legacy seed")
	}
	if err := validateDivaInterceptionMap(m); err != nil {
		return err
	}
	if len(m.Definitions) != 1 {
		return fmt.Errorf("diva custom map: unexpected template count")
	}
	for _, state := range m.States {
		if state.InvasionTick != 0 {
			return fmt.Errorf("diva legacy map contains invasion metadata")
		}
		expected, err := divaCustomMapAt(state.MapNumber)
		if err != nil {
			return err
		}
		definition, want := m.Definitions[0], expected.Definitions[0]
		if definition.ID != want.ID || definition.NextTemplateID != want.NextTemplateID ||
			!slices.Equal(definition.Nodes, want.Nodes) || !slices.Equal(m.Treasures, expected.Treasures) ||
			state.TemplateID != expected.States[0].TemplateID || len(state.Nodes) != len(expected.States[0].Nodes) {
			return fmt.Errorf("diva custom map: catalog does not match %s", divaCustomMapRules)
		}
		for i, actual := range state.Nodes {
			wantNode := expected.States[0].Nodes[i]
			wantNode.EarnedPoints = actual.EarnedPoints
			if actual != wantNode {
				return fmt.Errorf("diva custom map: map %d node %d does not match %s", state.MapNumber, i, divaCustomMapRules)
			}
		}
	}
	return nil
}
