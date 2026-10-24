package mhfpacket

import (
	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfCaravanRanking represents MSG_MHF_CARAVAN_RANKING (0x8e).
// ZZ FUN_1153ecc0 writes four big-endian words, including AckHandle.
type MsgMhfCaravanRanking struct {
	AckHandle uint32
	Unk0      uint32 // Reserved by ZZ constructors (0).
	Unk1      uint32 // 1 route personal, 2 overall personal, 3 route guild, 4 overall guild, 5 guild members.
	Unk2      int32  // Route/ranking key, or guild ID for selector 5.
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfCaravanRanking) Opcode() network.PacketID {
	return network.MSG_MHF_CARAVAN_RANKING
}

// Parse parses the packet from binary
func (m *MsgMhfCaravanRanking) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	m.Unk0 = bf.ReadUint32()
	m.Unk1 = bf.ReadUint32()
	m.Unk2 = bf.ReadInt32()
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfCaravanRanking) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	bf.WriteUint32(m.AckHandle)
	bf.WriteUint32(m.Unk0)
	bf.WriteUint32(m.Unk1)
	bf.WriteInt32(m.Unk2)
	return bf.Err()
}
