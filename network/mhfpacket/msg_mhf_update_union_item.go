package mhfpacket

import (
	"errors"
	"fmt"

	"erupe-ce/common/mhfitem"

	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfUpdateUnionItem represents the MSG_MHF_UPDATE_UNION_ITEM
type MsgMhfUpdateUnionItem struct {
	AckHandle    uint32
	UpdatedItems []mhfitem.MHFItemStack
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfUpdateUnionItem) Opcode() network.PacketID {
	return network.MSG_MHF_UPDATE_UNION_ITEM
}

// Parse parses the packet from binary
func (m *MsgMhfUpdateUnionItem) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	changes := int(bf.ReadUint16())
	bf.ReadUint8() // Zeroed
	bf.ReadUint8() // Zeroed
	if err := bf.Err(); err != nil {
		return err
	}
	const warehouseItemSize = 12
	if changes > len(bf.DataFromCurrent())/warehouseItemSize {
		return fmt.Errorf("union item count %d exceeds packet data", changes)
	}
	m.UpdatedItems = make([]mhfitem.MHFItemStack, 0, changes)
	for i := 0; i < changes; i++ {
		m.UpdatedItems = append(m.UpdatedItems, mhfitem.ReadWarehouseItem(bf))
	}
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfUpdateUnionItem) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	return errors.New("NOT IMPLEMENTED")
}
