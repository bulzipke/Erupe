package channelserver

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"erupe-ce/common/byteframe"
)

// syntheticDivaInterceptionMap is a protocol fixture, NOT a recovered retail
// map or an approved operating balance. Its thresholds must not seed a server.
func syntheticDivaInterceptionMap() DivaInterceptionMap {
	return DivaInterceptionMap{
		Definitions: []DivaInterceptionMapDefinition{{
			ID: 1, NextTemplateID: 1,
			Nodes: []DivaInterceptionMapNodeDefinition{
				{Coordinate: 301, Kind: 1},
				{Coordinate: 302, BaseRequiredPoints: 100},
				{Coordinate: 303, Kind: 2, BaseRequiredPoints: 200},
			},
		}},
		States: []DivaInterceptionMapState{{
			TemplateID: 1, MapNumber: 1,
			Nodes: []DivaInterceptionMapNodeState{
				{Coordinate: 301, NextCoordinate: 302, Ordinal: 1},
				{Coordinate: 302, NextCoordinate: 303, Ordinal: 2, EarnedPoints: 40, RequiredPoints: 100},
				{Coordinate: 303, Ordinal: 3, RequiredPoints: 200},
			},
		}},
	}
}

func TestDivaInterceptionMapPayloadNativeLayout(t *testing.T) {
	m := syntheticDivaInterceptionMap()
	definitionNode := &m.Definitions[0].Nodes[1]
	definitionNode.Unknown02 = 0x1234
	definitionNode.Unknown04 = 0x2345
	definitionNode.Unknown06 = 0x3456
	definitionNode.Unknown08 = 0x4567
	definitionNode.Unknown10 = 0x5678
	definitionNode.Unknown12 = 0x67
	m.States[0].Nodes[1].Unknown21 = 0x78
	m.States[0].Nodes[1].Unknown22 = 0x89
	m.AcquiredAreas = 0x01020304
	payload, err := divaInterceptionMapPayload(m)
	if err != nil {
		t.Fatal(err)
	}
	const wantSize = 2 + 8 + 64*23 + 2 + 1 + 7 + 3*23 + 4
	if len(payload) != wantSize {
		t.Fatalf("payload size = %d, want %d", len(payload), wantSize)
	}
	bf := byteframe.NewByteFrameFromBytes(payload)
	if bf.ReadUint8() != 0 || bf.ReadUint8() != 1 || bf.ReadUint32() != 1 || bf.ReadUint32() != 1 {
		t.Fatal("incorrect success/definition header")
	}
	for index := 0; index < 64; index++ {
		var expected DivaInterceptionMapNodeDefinition
		if index < 3 {
			expected = m.Definitions[0].Nodes[index]
		}
		actual := DivaInterceptionMapNodeDefinition{
			Coordinate: bf.ReadUint16(),
			Unknown02:  bf.ReadUint16(), Unknown04: bf.ReadUint16(),
			Unknown06: bf.ReadUint16(), Unknown08: bf.ReadUint16(), Unknown10: bf.ReadUint16(),
			Unknown12: bf.ReadUint8(), Kind: bf.ReadUint8(), BaseRequiredPoints: bf.ReadUint32(),
			TreasureMode: bf.ReadUint8(), TreasureGroupID: bf.ReadUint32(),
		}
		if actual != expected {
			t.Fatalf("definition node %d = %+v, want %+v", index, actual, expected)
		}
	}
	if bf.ReadUint16() != 0 || bf.ReadUint8() != 1 {
		t.Fatal("missing explicit empty treasure cache or state count")
	}
	if bf.ReadUint32() != 1 || bf.ReadUint16() != 1 || bf.ReadUint8() != 3 {
		t.Fatal("incorrect current state header")
	}
	for index, expected := range m.States[0].Nodes {
		actual := DivaInterceptionMapNodeState{
			EarnedPoints: bf.ReadUint32(), RequiredPoints: bf.ReadUint32(),
			Coordinate: bf.ReadUint16(), NextCoordinate: bf.ReadUint16(), BranchStartCoordinate: bf.ReadUint16(),
			BranchQuests: [3]uint16{bf.ReadUint16(), bf.ReadUint16(), bf.ReadUint16()},
			Ordinal:      bf.ReadUint8(), Unknown21: bf.ReadUint8(), Unknown22: bf.ReadUint8(),
		}
		if actual != expected {
			t.Fatalf("state node %d = %+v, want %+v", index, actual, expected)
		}
	}
	if bf.ReadUint32() != m.AcquiredAreas || bf.Err() != nil {
		t.Fatalf("incorrect area total or short body: %v", bf.Err())
	}
	// Independent random-access checks catch accidental native/wire offset swaps.
	const secondDefinition = 2 + 8 + 23
	if binary.BigEndian.Uint16(payload[secondDefinition:]) != 302 ||
		binary.BigEndian.Uint16(payload[secondDefinition+2:]) != 0x1234 ||
		binary.BigEndian.Uint32(payload[secondDefinition+14:]) != 100 {
		t.Fatal("definition wire offsets changed")
	}
	const secondState = 2 + 8 + 64*23 + 2 + 1 + 7 + 23
	if binary.BigEndian.Uint32(payload[secondState:]) != 40 ||
		binary.BigEndian.Uint16(payload[secondState+10:]) != 303 ||
		payload[secondState+20] != 2 || payload[secondState+21] != 0x78 || payload[secondState+22] != 0x89 {
		t.Fatal("state wire offsets changed")
	}
	if !bytes.Equal(payload[2+8+3*23:2+8+64*23], make([]byte, 61*23)) {
		t.Fatal("unused definition slots were not cleared")
	}
}

func TestDivaInterceptionMapRejectsUnsafePayload(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DivaInterceptionMap)
	}{
		{"no definitions", func(m *DivaInterceptionMap) { m.Definitions = nil }},
		{"too many definitions", func(m *DivaInterceptionMap) { m.Definitions = make([]DivaInterceptionMapDefinition, 9) }},
		{"zero template", func(m *DivaInterceptionMap) { m.Definitions[0].ID = 0 }},
		{"wide template", func(m *DivaInterceptionMap) { m.Definitions[0].ID = math.MaxUint16 + 1 }},
		{"duplicate template", func(m *DivaInterceptionMap) { m.Definitions = append(m.Definitions, m.Definitions[0]) }},
		{"dangling template", func(m *DivaInterceptionMap) { m.Definitions[0].NextTemplateID = 2 }},
		{"wide area total", func(m *DivaInterceptionMap) { m.AcquiredAreas = math.MaxInt32 + 1 }},
		{"no state", func(m *DivaInterceptionMap) { m.States = nil }},
		{"too many states", func(m *DivaInterceptionMap) { m.States = make([]DivaInterceptionMapState, 3) }},
		{"missing state template", func(m *DivaInterceptionMap) { m.States[0].TemplateID = 2 }},
		{"zero map number", func(m *DivaInterceptionMap) { m.States[0].MapNumber = 0 }},
		{"no nodes", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes = nil }},
		{"too many nodes", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes = make([]DivaInterceptionMapNodeDefinition, 61) }},
		{"invalid row", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[1].Coordinate = 602 }},
		{"invalid column", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[1].Coordinate = 313 }},
		{"duplicate coordinate", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[1].Coordinate = 301 }},
		{"no start", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[0].Kind = 0 }},
		{"two starts", func(m *DivaInterceptionMap) {
			m.Definitions[0].Nodes[1].Kind = 1
			m.Definitions[0].Nodes[1].BaseRequiredPoints = 0
		}},
		{"no goal", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[2].Kind = 0 }},
		{"two goals", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[1].Kind = 2 }},
		{"branch definition", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[1].Kind = 3 }},
		{"start costs points", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[0].BaseRequiredPoints = 1 }},
		{"zero goal base", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[2].BaseRequiredPoints = 0 }},
		{"wide base points", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[1].BaseRequiredPoints = math.MaxUint32 }},
		{"treasure mode", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[1].TreasureMode = 1 }},
		{"treasure group", func(m *DivaInterceptionMap) { m.Definitions[0].Nodes[1].TreasureGroupID = 1 }},
		{"state count mismatch", func(m *DivaInterceptionMap) { m.States[0].Nodes = m.States[0].Nodes[:2] }},
		{"missing node", func(m *DivaInterceptionMap) { m.States[0].Nodes[1].Coordinate = 304 }},
		{"duplicate state node", func(m *DivaInterceptionMap) { m.States[0].Nodes[1].Coordinate = 301 }},
		{"ordinal gap", func(m *DivaInterceptionMap) { m.States[0].Nodes[1].Ordinal = 4 }},
		{"zero ordinal", func(m *DivaInterceptionMap) { m.States[0].Nodes[0].Ordinal = 0 }},
		{"backward link", func(m *DivaInterceptionMap) { m.States[0].Nodes[1].NextCoordinate = 301 }},
		{"self cycle", func(m *DivaInterceptionMap) { m.States[0].Nodes[1].NextCoordinate = 302 }},
		{"dangling link", func(m *DivaInterceptionMap) { m.States[0].Nodes[1].NextCoordinate = 312 }},
		{"missing link", func(m *DivaInterceptionMap) { m.States[0].Nodes[0].NextCoordinate = 0 }},
		{"goal cycle", func(m *DivaInterceptionMap) { m.States[0].Nodes[2].NextCoordinate = 301 }},
		{"branch start", func(m *DivaInterceptionMap) { m.States[0].Nodes[1].BranchStartCoordinate = 303 }},
		{"branch quest", func(m *DivaInterceptionMap) { m.States[0].Nodes[1].BranchQuests[0] = 58043 }},
		{"start state points", func(m *DivaInterceptionMap) { m.States[0].Nodes[0].RequiredPoints = 1 }},
		{"zero goal state", func(m *DivaInterceptionMap) { m.States[0].Nodes[2].RequiredPoints = 0 }},
		{"wide state points", func(m *DivaInterceptionMap) { m.States[0].Nodes[1].RequiredPoints = math.MaxUint32 }},
		{"below base", func(m *DivaInterceptionMap) { m.States[0].Nodes[1].RequiredPoints = 99 }},
		{"excess earned", func(m *DivaInterceptionMap) { m.States[0].Nodes[1].EarnedPoints = 101 }},
		{"past frontier", func(m *DivaInterceptionMap) { m.States[0].Nodes[2].EarnedPoints = 1 }},
		{"missing previous page", func(m *DivaInterceptionMap) { m.States[0].MapNumber = 2 }},
		{"unexpected previous page", func(m *DivaInterceptionMap) { m.States = append(m.States, m.States[0]) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := syntheticDivaInterceptionMap()
			tt.mutate(&m)
			payload, err := divaInterceptionMapPayload(m)
			if err == nil || payload != nil {
				t.Fatalf("unsafe map produced payload length %d, error %v", len(payload), err)
			}
		})
	}
}

func TestDivaInterceptionMapPreviousState(t *testing.T) {
	m := syntheticDivaInterceptionMap()
	previous := syntheticDivaInterceptionMap().States[0]
	for index := range previous.Nodes {
		previous.Nodes[index].EarnedPoints = previous.Nodes[index].RequiredPoints
	}
	m.States[0].MapNumber = 2
	m.States = append(m.States, previous)
	m.AcquiredAreas = 2
	payload, err := divaInterceptionMapPayload(m)
	if err != nil {
		t.Fatal(err)
	}
	const stateCountOffset = 2 + 8 + 64*23 + 2
	if payload[stateCountOffset] != 2 {
		t.Fatal("previous map state missing")
	}
	const stateSize = 7 + 3*23
	if binary.BigEndian.Uint16(payload[stateCountOffset+1+4:]) != 2 ||
		binary.BigEndian.Uint16(payload[stateCountOffset+1+stateSize+4:]) != 1 {
		t.Fatal("current and previous map ordering changed")
	}
	m.States[1].MapNumber = 2
	if err := validateDivaInterceptionMap(m); err == nil {
		t.Fatal("accepted nonconsecutive previous map")
	}
	m.States[1].MapNumber = 1
	m.States[1].Nodes[2].EarnedPoints = 0
	if err := validateDivaInterceptionMap(m); err == nil {
		t.Fatal("accepted incomplete previous map")
	}
}

func TestDivaInterceptionMapTemplateChain(t *testing.T) {
	// These definitions exercise protocol linking only, not a retail map order.
	m := syntheticDivaInterceptionMap()
	second := syntheticDivaInterceptionMap().Definitions[0]
	second.ID, second.NextTemplateID = 2, 0
	m.Definitions[0].NextTemplateID = 2
	m.Definitions = append(m.Definitions, second)
	previous := syntheticDivaInterceptionMap().States[0]
	for index := range previous.Nodes {
		previous.Nodes[index].EarnedPoints = previous.Nodes[index].RequiredPoints
	}
	m.States[0].TemplateID = 2
	m.States = append(m.States, previous)
	if _, err := divaInterceptionMapPayload(m); err != nil {
		t.Fatalf("acyclic template chain rejected: %v", err)
	}
	m.Definitions[1].NextTemplateID = 1
	if payload, err := divaInterceptionMapPayload(m); err == nil || payload != nil {
		t.Fatal("accepted multi-template cycle")
	}
}

func TestDivaInterceptionMapSignedBoundaries(t *testing.T) {
	m := syntheticDivaInterceptionMap()
	m.AcquiredAreas = math.MaxInt32
	m.Definitions[0].Nodes[1].BaseRequiredPoints = math.MaxInt32
	m.States[0].Nodes[1].RequiredPoints = math.MaxInt32
	m.States[0].Nodes[1].EarnedPoints = math.MaxInt32
	if _, err := divaInterceptionMapPayload(m); err != nil {
		t.Fatalf("signed native boundary rejected: %v", err)
	}
}

func TestDivaInterceptionMapNativeCapacity(t *testing.T) {
	// Maximum-capacity synthetic grid; point values are test-only.
	m := DivaInterceptionMap{}
	for id := uint32(1); id <= 8; id++ {
		definition := DivaInterceptionMapDefinition{ID: id, NextTemplateID: id}
		for index := 0; index < 60; index++ {
			node := DivaInterceptionMapNodeDefinition{Coordinate: uint16((index/12+1)*100 + index%12 + 1), BaseRequiredPoints: 1}
			if index == 0 {
				node.Kind, node.BaseRequiredPoints = 1, 0
			} else if index == 59 {
				node.Kind = 2
			}
			definition.Nodes = append(definition.Nodes, node)
		}
		m.Definitions = append(m.Definitions, definition)
	}
	state := DivaInterceptionMapState{TemplateID: 1, MapNumber: 1}
	for index, definitionNode := range m.Definitions[0].Nodes {
		node := DivaInterceptionMapNodeState{Coordinate: definitionNode.Coordinate, Ordinal: uint8(index + 1), RequiredPoints: definitionNode.BaseRequiredPoints}
		if index < 59 {
			node.NextCoordinate = m.Definitions[0].Nodes[index+1].Coordinate
		}
		state.Nodes = append(state.Nodes, node)
	}
	m.States = append(m.States, state)
	payload, err := divaInterceptionMapPayload(m)
	if err != nil {
		t.Fatal(err)
	}
	wantSize := 2 + 8*(8+64*23) + 2 + 1 + 7 + 60*23 + 4
	if len(payload) != wantSize {
		t.Fatalf("capacity payload size %d, want %d", len(payload), wantSize)
	}
	// First/last native grid positions are accepted, external rows/columns are not.
	for _, coordinate := range []uint16{101, 112, 501, 512} {
		if !divaInterceptionMapCoordinateValid(coordinate) {
			t.Fatalf("valid coordinate %d rejected", coordinate)
		}
	}
	for _, coordinate := range []uint16{0, 100, 113, 500, 513, 601, math.MaxUint16} {
		if divaInterceptionMapCoordinateValid(coordinate) {
			t.Fatalf("unsafe coordinate %d accepted", coordinate)
		}
	}
}
