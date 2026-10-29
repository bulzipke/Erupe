// Package mhfearth encodes the native event calendar shared by Earth events.
package mhfearth

import (
	"fmt"

	"erupe-ce/common/byteframe"
)

const (
	RecordSize = 32
	MaxRecords = 256
)

// Status is one SERVER_MSG_EARTH_STATUS. The native HD/SD receivers derive the
// row count from payloadBytes >> 5; this reply has NO count/event-ID header.
// Clients look up each row by Category and its own time window and EventID.
type Status struct {
	Start    uint32
	End      uint32
	Category int32
	EventID  int32
	Monsters [4]int32
}

func Encode(rows []Status) ([]byte, error) {
	if len(rows) > MaxRecords {
		return nil, fmt.Errorf("earth: %d records exceed the native capacity %d", len(rows), MaxRecords)
	}
	bf := byteframe.NewByteFrame()
	for _, row := range rows {
		bf.WriteUint32(row.Start)
		bf.WriteUint32(row.End)
		bf.WriteInt32(row.Category)
		bf.WriteInt32(row.EventID)
		for _, monster := range row.Monsters {
			bf.WriteInt32(monster)
		}
	}
	return bf.Data(), bf.Err()
}
