package mhfpacket

import (
	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfCaravanMyRank represents MSG_MHF_CARAVAN_MY_RANK (0x8f).
// ZZ FUN_1153ecc0 writes four big-endian words, including AckHandle.
type MsgMhfCaravanMyRank struct {
	AckHandle uint32
	Unk0      uint32 // Reserved by ZZ constructor (0).
	Unk1      uint32 // 1 personal, 2 guild.
	Unk2      uint32 // 0 for personal; guild ID for selector 2.
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfCaravanMyRank) Opcode() network.PacketID {
	return network.MSG_MHF_CARAVAN_MY_RANK
}

// Parse parses the packet from binary
func (m *MsgMhfCaravanMyRank) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	m.Unk0 = bf.ReadUint32()
	m.Unk1 = bf.ReadUint32()
	m.Unk2 = bf.ReadUint32()
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfCaravanMyRank) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	bf.WriteUint32(m.AckHandle)
	bf.WriteUint32(m.Unk0)
	bf.WriteUint32(m.Unk1)
	bf.WriteUint32(m.Unk2)
	return bf.Err()
}
