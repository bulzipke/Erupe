package mhfpacket

import (
	"errors"
	"fmt"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/mhfitem"
	cfg "erupe-ce/config"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfUpdateWarehouse represents the MSG_MHF_UPDATE_WAREHOUSE
type MsgMhfUpdateWarehouse struct {
	AckHandle        uint32
	BoxType          uint8
	BoxIndex         uint8
	UpdatedItems     []mhfitem.MHFItemStack
	UpdatedEquipment []mhfitem.MHFEquipment
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfUpdateWarehouse) Opcode() network.PacketID {
	return network.MSG_MHF_UPDATE_WAREHOUSE
}

// Parse parses the packet from binary
func (m *MsgMhfUpdateWarehouse) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	m.BoxType = bf.ReadUint8()
	m.BoxIndex = bf.ReadUint8()
	changes := int(bf.ReadUint16())
	bf.ReadUint8() // Zeroed
	bf.ReadUint8() // Zeroed
	if err := bf.Err(); err != nil {
		return err
	}
	remaining := len(bf.DataFromCurrent())
	switch m.BoxType {
	case 0:
		const warehouseItemSize = 12
		if changes > remaining/warehouseItemSize {
			return fmt.Errorf("warehouse item count %d exceeds packet data", changes)
		}
		m.UpdatedItems = make([]mhfitem.MHFItemStack, 0, changes)
	case 1:
		equipmentSize := 16
		if ctx.RealClientMode >= cfg.G1 {
			equipmentSize += 48
		}
		if ctx.RealClientMode >= cfg.Z1 {
			equipmentSize += 2
		}
		if changes > remaining/equipmentSize {
			return fmt.Errorf("warehouse equipment count %d exceeds packet data", changes)
		}
		m.UpdatedEquipment = make([]mhfitem.MHFEquipment, 0, changes)
	default:
		return fmt.Errorf("unsupported warehouse box type %d", m.BoxType)
	}
	for i := 0; i < changes; i++ {
		switch m.BoxType {
		case 0:
			m.UpdatedItems = append(m.UpdatedItems, mhfitem.ReadWarehouseItem(bf))
		case 1:
			m.UpdatedEquipment = append(m.UpdatedEquipment, mhfitem.ReadWarehouseEquipment(bf, ctx.RealClientMode))
		}
	}
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfUpdateWarehouse) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	return errors.New("NOT IMPLEMENTED")
}
