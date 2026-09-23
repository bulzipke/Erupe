package channelserver

import (
	"fmt"
	"math"
	"slices"
)

// Operator-approved substitute rules, NOT recovered retail invasion data.
// Keep v1/v2 constructors and validators immutable for historical receipts.
const divaProgressiveMapRules = "custom-progressive-v3"
const divaProgressiveMaxTicks = 168

type divaProgressivePolicy struct {
	Interval, Chance, Increase, RedChance uint8
}

func divaProgressiveMapPolicy(number uint16) divaProgressivePolicy {
	tier := 0
	if number > 0 {
		tier = min(int(number-1)/2, 4)
	}
	return [...]divaProgressivePolicy{
		{6, 5, 5, 10}, {5, 10, 8, 20}, {4, 20, 12, 35}, {3, 30, 16, 50}, {2, 40, 20, 65},
	}[tier]
}

func divaProgressiveRoll(seed uint64, number, coordinate, tick uint16, domain uint64) uint8 {
	rng := divaRandomMapRNG(seed, number, domain^uint64(coordinate)*0x9e3779b97f4a7c15^uint64(tick)*0xbf58476d1ce4e5b9)
	return uint8(rng.next() % 100)
}

func divaProgressiveRequired(base uint32, percent uint8) uint32 {
	// Round up once from normal cost; never compound or repeatedly round costs.
	return uint32((uint64(base)*uint64(100+uint16(percent)) + 99) / 100)
}

func divaProgressiveInitialPercent(seed uint64, number, coordinate uint16) uint8 {
	p := divaProgressiveMapPolicy(number)
	if divaProgressiveRoll(seed, number, coordinate, 0, 0x68e31da4b5294b73) < p.Chance {
		return p.Increase
	}
	return 0
}

// Upper bound for a completed tile, exact value for a still-unclaimed main
// tile. Completed tiles freeze their percentage at capture and never regress.
func divaProgressivePercentAt(seed uint64, number, coordinate, tick uint16) uint8 {
	p := divaProgressiveMapPolicy(number)
	percent := divaProgressiveInitialPercent(seed, number, coordinate)
	for n := uint16(p.Interval); n <= tick; n += uint16(p.Interval) {
		if divaProgressiveRoll(seed, number, coordinate, n, 0xd6e8feb86659fd17) < p.Chance {
			percent = uint8(min(uint16(percent)+uint16(p.Increase), 100))
		}
	}
	return percent
}

func divaProgressiveMapAt(seed uint64, number uint16) (DivaInterceptionMap, error) {
	m, err := divaRandomMapAt(seed, number)
	if err != nil {
		return m, err
	}
	m.RulesVersion = divaProgressiveMapRules
	for i := range m.States[0].Nodes {
		node := &m.States[0].Nodes[i]
		// Page difficulty is the normal baseline, not an invasion modifier.
		m.Definitions[0].Nodes[i].BaseRequiredPoints = node.RequiredPoints
		if i > 0 && node.BranchQuests[0] == 0 {
			node.Fortification = divaProgressiveInitialPercent(seed, number, node.Coordinate)
			node.RequiredPoints = divaProgressiveRequired(node.RequiredPoints, node.Fortification)
		}
	}
	return m, nil
}

func divaProgressiveMapInitial(seed uint64) (DivaInterceptionMap, error) {
	m, err := divaProgressiveMapAt(seed, 1)
	if err == nil {
		err = validateDivaProgressiveMap(m)
	}
	return m, err
}

func divaProgressiveMapNext(previous DivaInterceptionMap) (DivaInterceptionMap, error) {
	if err := validateDivaProgressiveMap(previous); err != nil {
		return DivaInterceptionMap{}, err
	}
	state := previous.States[0]
	if state.MapNumber == math.MaxUint16 || state.Nodes[20].EarnedPoints != state.Nodes[20].RequiredPoints {
		return DivaInterceptionMap{}, fmt.Errorf("diva progressive map: incomplete/exhausted page")
	}
	next, err := divaProgressiveMapAt(previous.GenerationSeed, state.MapNumber+1)
	if err != nil {
		return next, err
	}
	definition := previous.Definitions[0]
	definition.Nodes = slices.Clone(definition.Nodes)
	definition.NextTemplateID = next.States[0].TemplateID
	state.Nodes = slices.Clone(state.Nodes)
	next.Definitions = append(next.Definitions, definition)
	next.States = append(next.States, state)
	next.AcquiredAreas = previous.AcquiredAreas
	return next, validateDivaProgressiveMap(next)
}

// Caller advances exactly one hourly boundary at a time, after settling points
// timestamped before it. Same-tick retries are harmless; skipped ticks rejected.
// Tick progress is persistent even when the attack roll changes no tile.
func applyDivaProgressiveInvasion(m DivaInterceptionMap, tick uint16) (DivaInterceptionMap, []uint16, error) {
	if err := validateDivaProgressiveMap(m); err != nil {
		return DivaInterceptionMap{}, nil, err
	}
	last := m.States[0].InvasionTick
	if tick == last {
		return cloneDivaMapPresentation(m), nil, nil
	}
	if tick != last+1 || tick > divaProgressiveMaxTicks {
		return DivaInterceptionMap{}, nil, fmt.Errorf("diva progressive map: nonsequential invasion tick")
	}
	result := cloneDivaMapPresentation(m)
	state := &result.States[0]
	state.InvasionTick = tick
	p := divaProgressiveMapPolicy(state.MapNumber)
	var changed []uint16
	if tick%uint16(p.Interval) == 0 {
		for i := 1; i <= 20; i++ {
			node := &state.Nodes[i]
			if node.EarnedPoints == node.RequiredPoints || node.Fortification == 100 {
				continue
			}
			if divaProgressiveRoll(result.GenerationSeed, state.MapNumber, node.Coordinate, tick, 0xd6e8feb86659fd17) >= p.Chance {
				continue
			}
			node.Fortification = uint8(min(uint16(node.Fortification)+uint16(p.Increase), 100))
			node.RequiredPoints = divaProgressiveRequired(result.Definitions[0].Nodes[i].BaseRequiredPoints, node.Fortification)
			changed = append(changed, node.Coordinate)
		}
	}
	return result, changed, validateDivaProgressiveMap(result)
}

func validateDivaProgressiveMap(m DivaInterceptionMap) error {
	if m.RulesVersion != divaProgressiveMapRules || m.GenerationSeed == 0 || m.GenerationSeed > math.MaxInt64 {
		return fmt.Errorf("diva progressive map: invalid identity")
	}
	if err := validateDivaInterceptionMap(m); err != nil {
		return err
	}
	// Normalize only a private copy to v2. Its unchanged strict validator checks
	// topology, quests, treasure payouts, page chain and cumulative area counts.
	normal := cloneDivaMapPresentation(m)
	normal.RulesVersion = divaRandomMapRules
	if len(m.Definitions) != len(m.States) {
		return fmt.Errorf("diva progressive map: inconsistent definitions")
	}
	for i, state := range m.States {
		if state.InvasionTick > divaProgressiveMaxTicks {
			return fmt.Errorf("diva progressive map: excessive age")
		}
		expected, err := divaRandomMapAt(m.GenerationSeed, state.MapNumber)
		if err != nil || len(state.Nodes) != 23 || len(m.Definitions[i].Nodes) != 23 {
			return fmt.Errorf("diva progressive map: invalid page")
		}
		p := divaProgressiveMapPolicy(state.MapNumber)
		for j, actual := range state.Nodes {
			base := expected.States[0].Nodes[j].RequiredPoints
			wantDefinition := expected.Definitions[0].Nodes[j]
			wantDefinition.BaseRequiredPoints = base
			if m.Definitions[i].Nodes[j] != wantDefinition {
				return fmt.Errorf("diva progressive map: changed definition")
			}
			initial, upper := uint8(0), uint8(0)
			if j > 0 && j <= 20 {
				initial = divaProgressiveInitialPercent(m.GenerationSeed, state.MapNumber, actual.Coordinate)
				upper = divaProgressivePercentAt(m.GenerationSeed, state.MapNumber, actual.Coordinate, state.InvasionTick)
			}
			if actual.Fortification < initial || actual.Fortification > upper ||
				(actual.Fortification != 100 && actual.Fortification%p.Increase != 0) ||
				(actual.EarnedPoints < actual.RequiredPoints && actual.Fortification != upper) ||
				actual.RequiredPoints != divaProgressiveRequired(base, actual.Fortification) {
				return fmt.Errorf("diva progressive map: invalid reinforcement")
			}
			node := &normal.States[i].Nodes[j]
			node.Fortification = 0
			node.RequiredPoints = base
			if actual.EarnedPoints == actual.RequiredPoints {
				node.EarnedPoints = base
			} else if base > 0 {
				node.EarnedPoints = min(actual.EarnedPoints, base-1)
			}
			normal.Definitions[i].Nodes[j].BaseRequiredPoints = expected.Definitions[0].Nodes[j].BaseRequiredPoints
		}
		normal.States[i].InvasionTick = 0
	}
	return validateDivaRandomMap(normal)
}

func selectDivaProgressiveTreasures(m DivaInterceptionMap) ([]DivaMapSpecialTreasureSelection, error) {
	if err := validateDivaProgressiveMap(m); err != nil {
		return nil, err
	}
	var selected []DivaMapSpecialTreasureSelection
	for _, state := range m.States {
		p := divaProgressiveMapPolicy(state.MapNumber)
		for _, branch := range divaCustomBranches {
			if divaProgressiveRoll(m.GenerationSeed, state.MapNumber, uint16(branch.Group), 0, 0xa0761d6478bd642f) < p.RedChance {
				selected = append(selected, DivaMapSpecialTreasureSelection{MapNumber: state.MapNumber, GroupID: branch.Group})
			}
		}
	}
	return selected, nil
}
