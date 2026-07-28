package mhfpacket

import (
	"errors"
	"fmt"

	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfAcquireTitle represents the MSG_MHF_ACQUIRE_TITLE
type MsgMhfAcquireTitle struct {
	AckHandle uint32
	TitleIDs  []uint16
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfAcquireTitle) Opcode() network.PacketID {
	return network.MSG_MHF_ACQUIRE_TITLE
}

// Parse parses the packet from binary
func (m *MsgMhfAcquireTitle) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	titles := int(bf.ReadUint16())
	bf.ReadUint16() // Zeroed
	if err := bf.Err(); err != nil {
		return err
	}
	if titles > maxClientBatchEntries {
		return fmt.Errorf("title ID count %d exceeds maximum %d", titles, maxClientBatchEntries)
	}
	if titles > len(bf.DataFromCurrent())/2 {
		return fmt.Errorf("title ID count %d exceeds packet data", titles)
	}
	m.TitleIDs = make([]uint16, 0, titles)
	for i := 0; i < titles; i++ {
		m.TitleIDs = append(m.TitleIDs, bf.ReadUint16())
	}
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfAcquireTitle) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	return errors.New("NOT IMPLEMENTED")
}
