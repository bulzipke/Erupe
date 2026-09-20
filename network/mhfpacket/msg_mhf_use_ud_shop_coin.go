package mhfpacket

import (
	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfUseUdShopCoin represents the MSG_MHF_USE_UD_SHOP_COIN
type MsgMhfUseUdShopCoin struct {
	AckHandle uint32
	Cost      uint8 // ZZ 0x114ff190: selected shop row's melody cost, not an item ID.
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfUseUdShopCoin) Opcode() network.PacketID {
	return network.MSG_MHF_USE_UD_SHOP_COIN
}

// Parse parses the packet from binary
func (m *MsgMhfUseUdShopCoin) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	m.Cost = bf.ReadUint8()
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfUseUdShopCoin) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	bf.WriteUint32(m.AckHandle)
	bf.WriteUint8(m.Cost)
	return bf.Err()
}
