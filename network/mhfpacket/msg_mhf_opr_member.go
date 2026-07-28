package mhfpacket

import (
	"errors"
	"fmt"

	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfOprMember represents the MSG_MHF_OPR_MEMBER
type MsgMhfOprMember struct {
	AckHandle uint32
	Blacklist bool
	Operation bool
	CharIDs   []uint32
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfOprMember) Opcode() network.PacketID {
	return network.MSG_MHF_OPR_MEMBER
}

// Parse parses the packet from binary
func (m *MsgMhfOprMember) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	m.Blacklist = bf.ReadBool()
	m.Operation = bf.ReadBool()
	bf.ReadUint8()
	chars := int(bf.ReadUint8())
	if err := bf.Err(); err != nil {
		return err
	}
	if chars > len(bf.DataFromCurrent())/4 {
		return fmt.Errorf("member operation count %d exceeds packet data", chars)
	}
	m.CharIDs = make([]uint32, 0, chars)
	for i := 0; i < chars; i++ {
		m.CharIDs = append(m.CharIDs, bf.ReadUint32())
	}
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfOprMember) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	return errors.New("NOT IMPLEMENTED")
}
