package mhfpacket

import (
	"errors"
	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
	"fmt"
)

// Goocoo represents a single Goocoo (guacot) companion entry in an update packet.
type Goocoo struct {
	Index uint32
	Data1 []int16
	Data2 []uint32
	Name  []byte
}

// MsgMhfUpdateGuacot represents the MSG_MHF_UPDATE_GUACOT
type MsgMhfUpdateGuacot struct {
	AckHandle  uint32
	EntryCount uint16
	Goocoos    []Goocoo
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfUpdateGuacot) Opcode() network.PacketID {
	return network.MSG_MHF_UPDATE_GUACOT
}

// Parse parses the packet from binary
func (m *MsgMhfUpdateGuacot) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	m.EntryCount = bf.ReadUint16()
	bf.ReadUint16() // Zeroed
	if err := bf.Err(); err != nil {
		return err
	}
	const maxGoocooSlots = 5
	if m.EntryCount > maxGoocooSlots {
		return fmt.Errorf("goocoo count %d exceeds slot count %d", m.EntryCount, maxGoocooSlots)
	}
	const minimumGoocooEntrySize = 57
	available := len(bf.DataFromCurrent()) / minimumGoocooEntrySize
	if int(m.EntryCount) > available {
		return fmt.Errorf("goocoo count %d exceeds %d available entries", m.EntryCount, available)
	}
	m.Goocoos = make([]Goocoo, 0, int(m.EntryCount))
	for i := 0; i < int(m.EntryCount); i++ {
		var temp Goocoo
		temp.Index = bf.ReadUint32()
		for j := 0; j < 22; j++ {
			temp.Data1 = append(temp.Data1, bf.ReadInt16())
		}
		for j := 0; j < 2; j++ {
			temp.Data2 = append(temp.Data2, bf.ReadUint32())
		}
		temp.Name = bf.ReadBytes(uint(bf.ReadUint8()))
		m.Goocoos = append(m.Goocoos, temp)
	}
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfUpdateGuacot) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	return errors.New("NOT IMPLEMENTED")
}
