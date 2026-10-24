package mhfpacket

import (
	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfCaravanMyScore represents MSG_MHF_CARAVAN_MY_SCORE (0x8d).
// ZZ FUN_1153ea30 writes eight big-endian words. Unk0 selects the
// operation: 1 reads scores (FUN_10349cd0), 2 submits them
// (FUN_10349f70). This is separate from MSG_MHF_POST_RYOUDAMA.
type MsgMhfCaravanMyScore struct {
	AckHandle uint32
	Unk0      uint32 // Operation: 1 read, 2 submit.
	Unk1      uint32 // Submit selector: 3.
	Unk2      int32  // Submit route key, zero-based (not the displayed number).
	Unk3      int32  // Submit base score, before server/event bonuses.
	Unk4      uint32 // Submit guild ID, or 0 when ineligible/unaffiliated.
	Unk5      int32  // Submit premium-course bonus.
	Unk6      int32  // Submit item bonus; the existing D463 restoration also adds antique bonuses.
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfCaravanMyScore) Opcode() network.PacketID {
	return network.MSG_MHF_CARAVAN_MY_SCORE
}

// Parse parses the packet from binary
func (m *MsgMhfCaravanMyScore) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	m.Unk0 = bf.ReadUint32()
	m.Unk1 = bf.ReadUint32()
	m.Unk2 = bf.ReadInt32()
	m.Unk3 = bf.ReadInt32()
	m.Unk4 = bf.ReadUint32()
	m.Unk5 = bf.ReadInt32()
	m.Unk6 = bf.ReadInt32()
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfCaravanMyScore) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	bf.WriteUint32(m.AckHandle)
	bf.WriteUint32(m.Unk0)
	bf.WriteUint32(m.Unk1)
	bf.WriteInt32(m.Unk2)
	bf.WriteInt32(m.Unk3)
	bf.WriteUint32(m.Unk4)
	bf.WriteInt32(m.Unk5)
	bf.WriteInt32(m.Unk6)
	return bf.Err()
}
