package mhfpacket

import (
	"errors"
	"fmt"

	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfPresentBox represents the MSG_MHF_PRESENT_BOX
type MsgMhfPresentBox struct {
	AckHandle uint32
	Unk0      uint32
	Unk1      uint32
	Unk2      uint32
	Unk3      uint32
	Unk4      uint32
	Unk5      uint32
	Unk6      uint32
	Unk7      []uint32
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfPresentBox) Opcode() network.PacketID {
	return network.MSG_MHF_PRESENT_BOX
}

// Parse parses the packet from binary
func (m *MsgMhfPresentBox) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	m.Unk0 = bf.ReadUint32()
	m.Unk1 = bf.ReadUint32()
	m.Unk2 = bf.ReadUint32()
	m.Unk3 = bf.ReadUint32()
	m.Unk4 = bf.ReadUint32()
	m.Unk5 = bf.ReadUint32()
	m.Unk6 = bf.ReadUint32()
	if err := bf.Err(); err != nil {
		return err
	}
	if m.Unk2 > maxClientBatchEntries {
		return fmt.Errorf("present box count %d exceeds maximum %d", m.Unk2, maxClientBatchEntries)
	}
	available := len(bf.DataFromCurrent()) / 4
	if uint64(m.Unk2) > uint64(available) {
		return fmt.Errorf("present box count %d exceeds %d available entries", m.Unk2, available)
	}
	m.Unk7 = make([]uint32, 0, int(m.Unk2))
	for i := uint32(0); i < m.Unk2; i++ {
		m.Unk7 = append(m.Unk7, bf.ReadUint32())
	}
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfPresentBox) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	return errors.New("NOT IMPLEMENTED")
}
