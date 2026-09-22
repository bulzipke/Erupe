package mhfpacket

import (
	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfSetUdTacticsFollower represents the MSG_MHF_SET_UD_TACTICS_FOLLOWER
type MsgMhfSetUdTacticsFollower struct {
	AckHandle uint32
	NameIndex uint16
	Voice     uint16
	Weapon    uint16
	Strength  uint16
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfSetUdTacticsFollower) Opcode() network.PacketID {
	return network.MSG_MHF_SET_UD_TACTICS_FOLLOWER
}

// Parse parses the packet from binary
func (m *MsgMhfSetUdTacticsFollower) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	m.NameIndex = bf.ReadUint16()
	m.Voice = bf.ReadUint16()
	m.Weapon = bf.ReadUint16()
	m.Strength = bf.ReadUint16()
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfSetUdTacticsFollower) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	bf.WriteUint32(m.AckHandle)
	bf.WriteUint16(m.NameIndex)
	bf.WriteUint16(m.Voice)
	bf.WriteUint16(m.Weapon)
	bf.WriteUint16(m.Strength)
	return bf.Err()
}
