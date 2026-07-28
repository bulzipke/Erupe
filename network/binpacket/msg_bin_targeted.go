package binpacket

import (
	"fmt"

	"erupe-ce/common/byteframe"
	"erupe-ce/network"
)

// MsgBinTargeted is a format used for some broadcast types
// to target specific players, instead of groups (world, stage, etc).
// It forwards a normal binpacket in it's RawDataPayload
type MsgBinTargeted struct {
	TargetCount    uint16
	TargetCharIDs  []uint32
	RawDataPayload []byte // The regular binary payload to be forwarded to the targets.
}

const maxTargetedRecipients = 256

// Opcode returns the ID associated with this packet type.
func (m *MsgBinTargeted) Opcode() network.PacketID {
	return network.MSG_SYS_CAST_BINARY
}

// Parse parses the packet from binary
func (m *MsgBinTargeted) Parse(bf *byteframe.ByteFrame) error {
	m.TargetCount = bf.ReadUint16()
	if err := bf.Err(); err != nil {
		return err
	}
	if m.TargetCount > maxTargetedRecipients {
		return fmt.Errorf("target count %d exceeds maximum %d", m.TargetCount, maxTargetedRecipients)
	}
	if int(m.TargetCount) > len(bf.DataFromCurrent())/4 {
		return fmt.Errorf("target count %d exceeds packet data", m.TargetCount)
	}

	m.TargetCharIDs = make([]uint32, m.TargetCount)
	for i := uint16(0); i < m.TargetCount; i++ {
		m.TargetCharIDs[i] = bf.ReadUint32()
	}

	m.RawDataPayload = bf.DataFromCurrent()

	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgBinTargeted) Build(bf *byteframe.ByteFrame) error {
	if int(m.TargetCount) > len(m.TargetCharIDs) {
		return fmt.Errorf("target count %d exceeds target list length %d", m.TargetCount, len(m.TargetCharIDs))
	}
	if m.TargetCount > maxTargetedRecipients {
		return fmt.Errorf("target count %d exceeds maximum %d", m.TargetCount, maxTargetedRecipients)
	}
	bf.WriteUint16(m.TargetCount)

	for i := 0; i < int(m.TargetCount); i++ {
		bf.WriteUint32(m.TargetCharIDs[i])
	}

	bf.WriteBytes(m.RawDataPayload)
	return nil
}
