package channelserver

import (
	"fmt"
	"math"

	"erupe-ce/common/byteframe"
)

const (
	divaInterceptionMapMaxDefinitions  = 8
	divaInterceptionMapDefinitionSlots = 64
	divaInterceptionMapMaxStates       = 2
	divaInterceptionMapMaxNodes        = 60
)

// DivaInterceptionMap contains a main route and optional single-node branches.
// The server-custom catalog is separate from this native wire representation.
type DivaInterceptionMap struct {
	// Persistence-only generation identity; never serialized to the native wire.
	// Empty metadata denotes the original immutable custom-v1 catalog.
	RulesVersion   string `json:",omitempty"`
	GenerationSeed uint64 `json:",omitempty"`
	Definitions    []DivaInterceptionMapDefinition
	Treasures      []DivaInterceptionMapTreasure
	States         []DivaInterceptionMapState // Current first, previous second when available.
	AcquiredAreas  uint32
}

type DivaInterceptionMapTreasure struct {
	GroupID     uint32
	ItemType    uint8
	ItemID      uint16
	Quantity    uint16
	MinMap      uint16
	MaxMap      uint16
	DisplayMode uint8
}

type DivaInterceptionMapDefinition struct {
	ID             uint32 // The native lookup masks IDs to 16 bits.
	NextTemplateID uint32
	Nodes          []DivaInterceptionMapNodeDefinition
}

// Unknown fields retain their exact native wire offsets. The examined HD map
// renderer does not consume them; preserving them avoids inventing semantics.
type DivaInterceptionMapNodeDefinition struct {
	Coordinate         uint16
	Unknown02          uint16
	Unknown04          uint16
	Unknown06          uint16
	Unknown08          uint16
	Unknown10          uint16
	Unknown12          uint8
	Kind               uint8 // 0 normal, 1 start, 2 goal, 3 main-route branch junction.
	BaseRequiredPoints uint32
	TreasureMode       uint8
	TreasureGroupID    uint32
}

type DivaInterceptionMapState struct {
	TemplateID uint32
	MapNumber  uint16
	Nodes      []DivaInterceptionMapNodeState
}

type DivaInterceptionMapNodeState struct {
	EarnedPoints          uint32
	RequiredPoints        uint32
	Coordinate            uint16
	NextCoordinate        uint16
	BranchStartCoordinate uint16
	BranchQuests          [3]uint16
	Ordinal               uint8
	Unknown21             uint8
	Unknown22             uint8
}

func divaInterceptionMapCoordinateValid(coordinate uint16) bool {
	row, column := coordinate/100, coordinate%100
	return row >= 1 && row <= 5 && column >= 1 && column <= 12
}

// validateDivaInterceptionMap rejects data that could overrun native arrays,
// leave dangling branch pointers, truncate the ordinal-indexed UI, or create an
// initially completed zero-cost goal. It deliberately does not supply missing
// retail thresholds, treasure, or a points-to-area conversion.
func validateDivaInterceptionMap(m DivaInterceptionMap) error {
	if len(m.Definitions) == 0 || len(m.Definitions) > divaInterceptionMapMaxDefinitions {
		return fmt.Errorf("diva map: definition count must be 1..%d", divaInterceptionMapMaxDefinitions)
	}
	if len(m.States) == 0 || len(m.States) > divaInterceptionMapMaxStates {
		return fmt.Errorf("diva map: state count must be 1..%d", divaInterceptionMapMaxStates)
	}
	if m.AcquiredAreas > math.MaxInt32 {
		return fmt.Errorf("diva map: acquired area total exceeds signed native display range")
	}
	if len(m.Treasures) > 512 {
		return fmt.Errorf("diva map: treasure table exceeds native capacity")
	}
	groups := make(map[uint32]bool)
	for _, row := range m.Treasures {
		if row.GroupID == 0 || row.Quantity == 0 || row.MinMap == 0 || row.MaxMap < row.MinMap ||
			(row.ItemType != 7 && row.ItemType != 26) || (row.ItemType == 7 && row.ItemID == 0) ||
			(row.ItemType == 26 && row.ItemID != 0) || row.DisplayMode > 1 {
			return fmt.Errorf("diva map: invalid fixed treasure reward")
		}
		groups[row.GroupID] = true
	}
	definitions := make(map[uint32]DivaInterceptionMapDefinition, len(m.Definitions))
	for _, definition := range m.Definitions {
		if definition.ID == 0 || definition.ID > math.MaxUint16 {
			return fmt.Errorf("diva map: template ID %d exceeds native lookup range", definition.ID)
		}
		if _, exists := definitions[definition.ID]; exists {
			return fmt.Errorf("diva map: duplicate template %d", definition.ID)
		}
		if err := validateDivaInterceptionMapDefinition(definition); err != nil {
			return err
		}
		for _, node := range definition.Nodes {
			if node.TreasureGroupID != 0 && !groups[node.TreasureGroupID] {
				return fmt.Errorf("diva map: node references missing treasure group")
			}
		}
		definitions[definition.ID] = definition
	}
	for _, definition := range m.Definitions {
		if definition.NextTemplateID != 0 {
			if _, exists := definitions[definition.NextTemplateID]; !exists {
				return fmt.Errorf("diva map: template %d links to missing template %d", definition.ID, definition.NextTemplateID)
			}
		}
	}
	// The native predecessor scan is bounded but cyclic multi-template data can
	// miscount previous pages. Only acyclic chains and a terminal self-link have
	// been accepted for this main-route-only protocol layer.
	for _, definition := range m.Definitions {
		visited := make(map[uint32]bool, len(m.Definitions))
		for id := definition.ID; id != 0; {
			if visited[id] {
				return fmt.Errorf("diva map: multi-template cycle involving template %d", id)
			}
			visited[id] = true
			next := definitions[id].NextTemplateID
			if next == id {
				break
			}
			id = next
		}
	}
	for index, state := range m.States {
		definition, exists := definitions[state.TemplateID]
		if !exists {
			return fmt.Errorf("diva map: state %d references missing template %d", index, state.TemplateID)
		}
		if err := validateDivaInterceptionMapState(state, definition); err != nil {
			return fmt.Errorf("diva map state %d: %w", index, err)
		}
	}
	current := m.States[0]
	hasPrevious := false
	for _, definition := range m.Definitions {
		if definition.NextTemplateID == current.TemplateID &&
			(definition.ID != current.TemplateID || current.MapNumber > 1) {
			hasPrevious = true
		}
	}
	if hasPrevious != (len(m.States) == 2) {
		return fmt.Errorf("diva map: previous-page availability and state count disagree")
	}
	if len(m.States) == 2 {
		previous := m.States[1]
		if definitions[previous.TemplateID].NextTemplateID != current.TemplateID {
			return fmt.Errorf("diva map: previous template does not link to current template")
		}
		if previous.TemplateID == current.TemplateID &&
			uint32(previous.MapNumber)+1 != uint32(current.MapNumber) {
			return fmt.Errorf("diva map: repeated template requires consecutive map numbers")
		}
		goalIndex := divaInterceptionMapGoalIndex(previous, definitions[previous.TemplateID])
		goal := previous.Nodes[goalIndex]
		if goal.EarnedPoints != goal.RequiredPoints {
			return fmt.Errorf("diva map: previous map is not complete")
		}
	}
	return nil
}

func validateDivaInterceptionMapDefinition(definition DivaInterceptionMapDefinition) error {
	if len(definition.Nodes) < 2 || len(definition.Nodes) > divaInterceptionMapMaxNodes {
		return fmt.Errorf("diva map: template %d requires 2..%d nodes", definition.ID, divaInterceptionMapMaxNodes)
	}
	coordinates := make(map[uint16]bool, len(definition.Nodes))
	starts, goals := 0, 0
	for _, node := range definition.Nodes {
		if !divaInterceptionMapCoordinateValid(node.Coordinate) || coordinates[node.Coordinate] {
			return fmt.Errorf("diva map: template %d has invalid/duplicate coordinate %d", definition.ID, node.Coordinate)
		}
		coordinates[node.Coordinate] = true
		switch node.Kind {
		case 0, 3:
		case 1:
			starts++
		case 2:
			goals++
		default:
			return fmt.Errorf("diva map: template %d has unsupported node kind %d", definition.ID, node.Kind)
		}
		if node.Kind == 1 {
			if node.BaseRequiredPoints != 0 {
				return fmt.Errorf("diva map: start must not cost points")
			}
		} else if node.BaseRequiredPoints == 0 || node.BaseRequiredPoints > math.MaxInt32 {
			return fmt.Errorf("diva map: node %d requires positive signed-safe base points", node.Coordinate)
		}
		if node.TreasureMode != 0 {
			return fmt.Errorf("diva map: unsupported special treasure mode")
		}
	}
	if starts != 1 || goals != 1 {
		return fmt.Errorf("diva map: template %d requires exactly one start and one goal", definition.ID)
	}
	return nil
}

func divaInterceptionMapGoalIndex(state DivaInterceptionMapState, definition DivaInterceptionMapDefinition) int {
	for i, node := range state.Nodes {
		for _, def := range definition.Nodes {
			if def.Coordinate == node.Coordinate && def.Kind == 2 {
				return i
			}
		}
	}
	return -1
}

func validateDivaInterceptionMapState(state DivaInterceptionMapState, definition DivaInterceptionMapDefinition) error {
	if state.MapNumber == 0 {
		return fmt.Errorf("map number must be positive")
	}
	if len(state.Nodes) != len(definition.Nodes) {
		return fmt.Errorf("state and definition node counts disagree")
	}
	definitions := make(map[uint16]DivaInterceptionMapNodeDefinition, len(definition.Nodes))
	for _, node := range definition.Nodes {
		definitions[node.Coordinate] = node
	}
	seen := make(map[uint16]bool, len(state.Nodes))
	goalIndex := divaInterceptionMapGoalIndex(state, definition)
	if goalIndex < 1 {
		return fmt.Errorf("main route must end in a goal after its start")
	}
	branches := make(map[uint16]int)
	for i := goalIndex + 1; i < len(state.Nodes); i++ {
		branches[state.Nodes[i].Coordinate] = i
	}
	branchParents := make(map[uint16]int)
	questRoutes := make(map[uint16]uint16)
	incomplete := false
	for index, node := range state.Nodes {
		definitionNode, exists := definitions[node.Coordinate]
		if !exists || seen[node.Coordinate] {
			return fmt.Errorf("node %d references missing/duplicate coordinate %d", index, node.Coordinate)
		}
		seen[node.Coordinate] = true
		if int(node.Ordinal) != index+1 {
			return fmt.Errorf("node ordinals must be contiguous from 1")
		}
		main := index <= goalIndex
		if (index == 0 && definitionNode.Kind != 1) || (index == goalIndex && definitionNode.Kind != 2) ||
			(index > 0 && index < goalIndex && definitionNode.Kind != 0 && definitionNode.Kind != 3) ||
			(!main && definitionNode.Kind != 0) {
			return fmt.Errorf("wrong main/branch node kind")
		}
		next := uint16(0)
		if index < goalIndex {
			next = state.Nodes[index+1].Coordinate
		}
		if node.NextCoordinate != next {
			return fmt.Errorf("node %d has dangling, cyclic, or out-of-order next link", node.Coordinate)
		}
		if main {
			if node.BranchQuests != [3]uint16{} || definitionNode.TreasureGroupID != 0 {
				return fmt.Errorf("main node carries branch quest/treasure")
			}
			if definitionNode.Kind == 3 {
				if _, exists := branches[node.BranchStartCoordinate]; !exists || node.BranchStartCoordinate == 0 {
					return fmt.Errorf("missing branch target")
				}
				if _, duplicate := branchParents[node.BranchStartCoordinate]; duplicate {
					return fmt.Errorf("shared branch target")
				}
				branchParents[node.BranchStartCoordinate] = index - 1
			} else if node.BranchStartCoordinate != 0 {
				return fmt.Errorf("non-junction links a branch")
			}
		} else {
			if node.BranchStartCoordinate != 0 || node.BranchQuests[0] == 0 {
				return fmt.Errorf("invalid single-node branch")
			}
			ended := false
			for _, quest := range node.BranchQuests {
				if quest == 0 {
					ended = true
					continue
				}
				if ended || questRoutes[quest] != 0 {
					return fmt.Errorf("duplicate or noncontiguous branch quests")
				}
				questRoutes[quest] = node.Coordinate
			}
		}
		if index == 0 {
			if node.RequiredPoints != 0 || node.EarnedPoints != 0 {
				return fmt.Errorf("start must not cost or earn points")
			}
		} else if node.RequiredPoints < definitionNode.BaseRequiredPoints || node.RequiredPoints > math.MaxInt32 {
			return fmt.Errorf("node %d has unsafe required points", node.Coordinate)
		}
		if node.EarnedPoints > node.RequiredPoints {
			return fmt.Errorf("node %d earned points exceed required points", node.Coordinate)
		}
		if main && incomplete && node.EarnedPoints != 0 {
			return fmt.Errorf("node %d has points beyond the incomplete frontier", node.Coordinate)
		}
		if main && node.EarnedPoints < node.RequiredPoints {
			incomplete = true
		}
	}
	if len(branchParents) != len(branches) || len(branches) > 10 {
		return fmt.Errorf("orphaned or excessive branch nodes")
	}
	for coordinate, index := range branches {
		parent := state.Nodes[branchParents[coordinate]]
		if parent.EarnedPoints < parent.RequiredPoints && state.Nodes[index].EarnedPoints != 0 {
			return fmt.Errorf("locked branch already contains points")
		}
	}
	return nil
}

// divaInterceptionMapPayload follows HD FUN_11536390. Validation happens before
// writing, so invalid data returns nil instead of a partial success payload.
// Empty treasure data is explicitly serialized, clearing the native cache.
func divaInterceptionMapPayload(m DivaInterceptionMap) ([]byte, error) {
	if err := validateDivaInterceptionMap(m); err != nil {
		return nil, err
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0)
	bf.WriteUint8(uint8(len(m.Definitions)))
	for _, definition := range m.Definitions {
		bf.WriteUint32(definition.ID)
		bf.WriteUint32(definition.NextTemplateID)
		for index := 0; index < divaInterceptionMapDefinitionSlots; index++ {
			var node DivaInterceptionMapNodeDefinition
			if index < len(definition.Nodes) {
				node = definition.Nodes[index]
			}
			bf.WriteUint16(node.Coordinate)
			bf.WriteUint16(node.Unknown02)
			bf.WriteUint16(node.Unknown04)
			bf.WriteUint16(node.Unknown06)
			bf.WriteUint16(node.Unknown08)
			bf.WriteUint16(node.Unknown10)
			bf.WriteUint8(node.Unknown12)
			bf.WriteUint8(node.Kind)
			bf.WriteUint32(node.BaseRequiredPoints)
			bf.WriteUint8(node.TreasureMode)
			bf.WriteUint32(node.TreasureGroupID)
		}
	}
	bf.WriteUint16(uint16(len(m.Treasures)))
	for _, treasure := range m.Treasures {
		bf.WriteUint32(treasure.GroupID)
		bf.WriteUint8(treasure.ItemType)
		bf.WriteUint16(treasure.ItemID)
		bf.WriteUint16(treasure.Quantity)
		bf.WriteUint16(treasure.MinMap)
		bf.WriteUint16(treasure.MaxMap)
		bf.WriteUint8(treasure.DisplayMode)
	}
	bf.WriteUint8(uint8(len(m.States)))
	for _, state := range m.States {
		bf.WriteUint32(state.TemplateID)
		bf.WriteUint16(state.MapNumber)
		bf.WriteUint8(uint8(len(state.Nodes)))
		for _, node := range state.Nodes {
			bf.WriteUint32(node.EarnedPoints)
			bf.WriteUint32(node.RequiredPoints)
			bf.WriteUint16(node.Coordinate)
			bf.WriteUint16(node.NextCoordinate)
			bf.WriteUint16(node.BranchStartCoordinate)
			for _, quest := range node.BranchQuests {
				bf.WriteUint16(quest)
			}
			bf.WriteUint8(node.Ordinal)
			bf.WriteUint8(node.Unknown21)
			bf.WriteUint8(node.Unknown22)
		}
	}
	bf.WriteUint32(m.AcquiredAreas)
	return bf.Data(), nil
}
