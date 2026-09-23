package channelserver

import (
	"fmt"
	"math"
	"slices"
)

const divaRandomMapRules = "custom-random-v2"
const divaRandomMapSearchBudget = 4096

// This is an approved server-custom generator inferred from retail screenshots,
// not recovered retail map data or the original server's generation algorithm.
// SplitMix64, neighbor order, budget and fallback are persisted v2 rules.
type divaMapRandom uint64

func (r *divaMapRandom) next() uint64 {
	*r += 0x9e3779b97f4a7c15
	z := uint64(*r)
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func divaRandomMapRNG(seed uint64, number uint16, domain uint64) divaMapRandom {
	return divaMapRandom(seed ^ (uint64(number) * 0xd6e8feb86659fd93) ^ domain)
}

func divaRandomMapEndRow(seed uint64, number uint16) uint16 {
	rng := divaRandomMapRNG(seed, number, 0xe7037ed1a0b428db)
	return 1 + uint16(rng.next()%5)
}

// HD 103a4d00 uses x=(column-1)*50 and
// y=(5-row)*58 + (odd one-based column ? 29 : 0), before common offsets.
func divaMapHexNeighbors(coordinate uint16) []uint16 {
	row, column := int(coordinate/100), int(coordinate%100)
	if !divaInterceptionMapCoordinateValid(coordinate) {
		return nil
	}
	diagonal := 1
	if column%2 == 1 {
		diagonal = -1
	}
	steps := [6][2]int{{1, 0}, {-1, 0}, {0, -1}, {0, 1}, {diagonal, -1}, {diagonal, 1}}
	result := make([]uint16, 0, 6)
	for _, step := range steps {
		r, c := row+step[0], column+step[1]
		if r >= 1 && r <= 5 && c >= 1 && c <= 12 {
			result = append(result, uint16(r*100+c))
		}
	}
	return result
}

func divaMapHexAdjacent(a, b uint16) bool {
	return slices.Contains(divaMapHexNeighbors(a), b)
}

func divaMapCellBit(coordinate uint16) uint64 {
	return uint64(1) << ((coordinate/100-1)*12 + coordinate%100 - 1)
}

func divaMapShuffle(values []uint16, rng *divaMapRandom) {
	for i := len(values) - 1; i > 0; i-- {
		j := int(rng.next() % uint64(i+1))
		values[i], values[j] = values[j], values[i]
	}
}

func divaRandomMapBranches(path []uint16, occupied uint64, rng *divaMapRandom) ([2]uint16, bool) {
	a := divaMapHexNeighbors(path[6])
	b := divaMapHexNeighbors(path[16])
	if rng != nil {
		divaMapShuffle(a, rng)
		divaMapShuffle(b, rng)
	}
	for _, first := range a {
		if occupied&divaMapCellBit(first) != 0 {
			continue
		}
		for _, second := range b {
			if first != second && occupied&divaMapCellBit(second) == 0 {
				return [2]uint16{first, second}, true
			}
		}
	}
	return [2]uint16{}, false
}

// The fallback combines one of five fixed left-edge prefixes with one of five
// right-edge suffixes. All 25 combinations are independently geometry-tested,
// including an unused adjacent branch at each fixed junction (6 and 16).
func divaRandomMapFallback(startRow, endRow uint16) ([]uint16, [2]uint16, error) {
	prefixes := [5][11]uint16{
		{101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111},
		{201, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111},
		{301, 202, 203, 104, 105, 106, 107, 108, 109, 110, 111},
		{401, 302, 303, 204, 205, 106, 107, 108, 109, 110, 111},
		{501, 402, 403, 304, 305, 206, 207, 108, 109, 110, 111},
	}
	suffixes := [5][10]uint16{
		{211, 311, 411, 310, 410, 511, 412, 312, 212, 112},
		{112, 211, 311, 411, 310, 410, 511, 412, 312, 212},
		{112, 212, 211, 311, 411, 310, 410, 511, 412, 312},
		{112, 212, 312, 311, 211, 210, 310, 411, 511, 412},
		{112, 212, 312, 412, 411, 311, 310, 410, 511, 512},
	}
	if startRow < 1 || startRow > 5 || endRow < 1 || endRow > 5 {
		return nil, [2]uint16{}, fmt.Errorf("diva random map: invalid fallback endpoints")
	}
	path := append([]uint16(nil), prefixes[startRow-1][:]...)
	path = append(path, suffixes[endRow-1][:]...)
	var occupied uint64
	for _, c := range path {
		occupied |= divaMapCellBit(c)
	}
	branches, ok := divaRandomMapBranches(path, occupied, nil)
	if !ok {
		return nil, branches, fmt.Errorf("diva random map: invalid fallback branches")
	}
	return path, branches, nil
}

// Goal rows are independent of search outcomes, allowing O(1) reconstruction of
// the preceding goal. The first map prefers an internal left/upper start;
// later maps start at column 1 in the preceding goal's row. These alignments
// are an approved visual approximation, not a proven retail constraint.
func divaRandomMapGeometry(seed uint64, number uint16, budget int) ([]uint16, [2]uint16, error) {
	rng := divaRandomMapRNG(seed, number, 0x8ebc6af09c88c6e3)
	startRow, startColumn := uint16(0), uint16(1)
	if number == 1 {
		startRow, startColumn = 3+uint16(rng.next()%3), 2+uint16(rng.next()%3)
	} else {
		startRow = divaRandomMapEndRow(seed, number-1)
	}
	endRow := divaRandomMapEndRow(seed, number)
	goal := endRow*100 + 12
	// Exact shortest hex distances prune impossible paths without recursion.
	distance := [60]int{}
	for i := range distance {
		distance[i] = -1
	}
	index := func(c uint16) int { return int((c/100-1)*12 + c%100 - 1) }
	distance[index(goal)] = 0
	queue := []uint16{goal}
	for i := 0; i < len(queue); i++ {
		for _, n := range divaMapHexNeighbors(queue[i]) {
			if distance[index(n)] < 0 {
				distance[index(n)] = distance[index(queue[i])] + 1
				queue = append(queue, n)
			}
		}
	}
	path := []uint16{startRow*100 + startColumn}
	occupied := divaMapCellBit(path[0])
	var branches [2]uint16
	var search func() bool
	search = func() bool {
		if budget <= 0 {
			return false
		}
		budget--
		last := path[len(path)-1]
		remaining := 21 - len(path)
		if distance[index(last)] > remaining {
			return false
		}
		if remaining == 0 {
			if last != goal {
				return false
			}
			var ok bool
			branches, ok = divaRandomMapBranches(path, occupied, &rng)
			return ok
		}
		options := divaMapHexNeighbors(last)
		divaMapShuffle(options, &rng)
		for _, next := range options {
			bit := divaMapCellBit(next)
			if occupied&bit != 0 || (next == goal && remaining != 1) {
				continue
			}
			path = append(path, next)
			occupied |= bit
			if search() {
				return true
			}
			occupied &^= bit
			path = path[:len(path)-1]
			if budget == 0 {
				break
			}
		}
		return false
	}
	if search() {
		return path, branches, nil
	}
	return divaRandomMapFallback(startRow, endRow)
}

// Builds one catalog/state without attaching a predecessor. It is an internal
// construction step, not a complete map response for number > 1.
func divaRandomMapAt(seed uint64, number uint16) (DivaInterceptionMap, error) {
	if seed == 0 || seed > math.MaxInt64 || number == 0 {
		return DivaInterceptionMap{}, fmt.Errorf("diva random map: invalid seed or map number")
	}
	path, branches, err := divaRandomMapGeometry(seed, number, divaRandomMapSearchBudget)
	if err != nil {
		return DivaInterceptionMap{}, err
	}
	factor := uint32(4 + min(number-1, 4))
	definition := DivaInterceptionMapDefinition{ID: uint32(number)}
	state := DivaInterceptionMapState{TemplateID: uint32(number), MapNumber: number}
	for i, coordinate := range path {
		base, kind := uint32(0), uint8(0)
		if i == 0 {
			kind = 1
		} else {
			base = 2500 + uint32((i-1)/5)*1000
		}
		if i == 20 {
			kind = 2
		}
		node := DivaInterceptionMapNodeState{Coordinate: coordinate, Ordinal: uint8(i + 1), RequiredPoints: base * factor / 4}
		if i < 20 {
			node.NextCoordinate = path[i+1]
		}
		definition.Nodes = append(definition.Nodes, DivaInterceptionMapNodeDefinition{Coordinate: coordinate, Kind: kind, BaseRequiredPoints: base})
		state.Nodes = append(state.Nodes, node)
	}
	m := DivaInterceptionMap{RulesVersion: divaRandomMapRules, GenerationSeed: seed}
	for i, branch := range divaCustomBranches {
		coordinate := branches[i]
		definition.Nodes[branch.Junction].Kind = 3
		state.Nodes[branch.Junction].BranchStartCoordinate = coordinate
		definition.Nodes = append(definition.Nodes, DivaInterceptionMapNodeDefinition{
			Coordinate: coordinate, BaseRequiredPoints: 5000, TreasureGroupID: branch.Group,
		})
		state.Nodes = append(state.Nodes, DivaInterceptionMapNodeState{
			Coordinate: coordinate, RequiredPoints: 5000 * factor / 4, Ordinal: uint8(len(state.Nodes) + 1), BranchQuests: branch.Quests,
		})
		m.Treasures = append(m.Treasures,
			DivaInterceptionMapTreasure{GroupID: branch.Group, ItemType: 7, ItemID: 1026, Quantity: 5, MinMap: 1, MaxMap: math.MaxUint16, DisplayMode: 1},
			DivaInterceptionMapTreasure{GroupID: branch.Group, ItemType: 26, Quantity: 500, MinMap: 1, MaxMap: math.MaxUint16, DisplayMode: 1})
	}
	m.Definitions = []DivaInterceptionMapDefinition{definition}
	m.States = []DivaInterceptionMapState{state}
	return m, nil
}

func divaRandomMapInitial(seed uint64) (DivaInterceptionMap, error) {
	m, err := divaRandomMapAt(seed, 1)
	if err != nil {
		return m, err
	}
	return m, validateDivaRandomMap(m)
}

func divaRandomMapNext(previous DivaInterceptionMap) (DivaInterceptionMap, error) {
	if err := validateDivaRandomMap(previous); err != nil {
		return DivaInterceptionMap{}, err
	}
	state := previous.States[0]
	if state.MapNumber == math.MaxUint16 {
		return DivaInterceptionMap{}, fmt.Errorf("diva map number exhausted")
	}
	if state.Nodes[20].EarnedPoints != state.Nodes[20].RequiredPoints {
		return DivaInterceptionMap{}, fmt.Errorf("diva random map: current goal is incomplete")
	}
	next, err := divaRandomMapAt(previous.GenerationSeed, state.MapNumber+1)
	if err != nil {
		return DivaInterceptionMap{}, err
	}
	definition := previous.Definitions[0]
	definition.Nodes = slices.Clone(definition.Nodes)
	definition.NextTemplateID = next.States[0].TemplateID
	state.Nodes = slices.Clone(state.Nodes)
	next.Definitions = append(next.Definitions, definition)
	next.States = append(next.States, state)
	next.AcquiredAreas = previous.AcquiredAreas
	return next, validateDivaRandomMap(next)
}

func validateDivaRandomMap(m DivaInterceptionMap) error {
	if m.RulesVersion != divaRandomMapRules || m.GenerationSeed == 0 || m.GenerationSeed > math.MaxInt64 {
		return fmt.Errorf("diva random map: invalid metadata")
	}
	if err := validateDivaInterceptionMap(m); err != nil {
		return err
	}
	number := m.States[0].MapNumber
	count := 1
	if number > 1 {
		count = 2
	}
	if len(m.States) != count || len(m.Definitions) != count {
		return fmt.Errorf("diva random map: current/previous count mismatch")
	}
	var currentAreas, previousBranches uint32
	for i, state := range m.States {
		if state.InvasionTick != 0 {
			return fmt.Errorf("diva v2 map contains invasion metadata")
		}
		wantNumber := number - uint16(i)
		if state.MapNumber != wantNumber {
			return fmt.Errorf("diva random map: nonconsecutive map number")
		}
		expected, err := divaRandomMapAt(m.GenerationSeed, wantNumber)
		if err != nil {
			return err
		}
		wantDef, wantState := expected.Definitions[0], expected.States[0]
		if i == 1 {
			wantDef.NextTemplateID = uint32(number)
		}
		actualDef := m.Definitions[i]
		if actualDef.ID != wantDef.ID || actualDef.NextTemplateID != wantDef.NextTemplateID ||
			!slices.Equal(actualDef.Nodes, wantDef.Nodes) || !slices.Equal(m.Treasures, expected.Treasures) ||
			state.TemplateID != wantState.TemplateID || len(state.Nodes) != len(wantState.Nodes) {
			return fmt.Errorf("diva random map: catalog mismatch")
		}
		for j, actual := range state.Nodes {
			want := wantState.Nodes[j]
			want.EarnedPoints = actual.EarnedPoints
			if actual != want {
				return fmt.Errorf("diva random map: map %d node %d changed", number, j)
			}
			if actual.RequiredPoints > 0 && actual.EarnedPoints == actual.RequiredPoints {
				if i == 0 {
					currentAreas++
				} else if actual.BranchQuests[0] != 0 {
					previousBranches++
				}
			}
		}
	}
	// Keep the cumulative total separate from personal points. Older branch
	// completions are no longer on the wire, so validate their possible range.
	minimum, maximum := currentAreas, currentAreas
	if number > 1 {
		minimum += uint32(number-1)*20 + previousBranches
		maximum += uint32(number-2)*22 + 20 + previousBranches
	}
	if m.AcquiredAreas < minimum || m.AcquiredAreas > maximum {
		return fmt.Errorf("diva random map: acquired-area total disagrees with progress")
	}
	return nil
}
