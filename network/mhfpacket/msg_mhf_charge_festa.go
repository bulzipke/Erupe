package mhfpacket

import (
	"errors"
	"fmt"

	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfChargeFesta represents the MSG_MHF_CHARGE_FESTA
type MsgMhfChargeFesta struct {
	AckHandle uint32
	FestaID   uint32
	GuildID   uint32
	Souls     []uint16
	Auto      bool
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfChargeFesta) Opcode() network.PacketID {
	return network.MSG_MHF_CHARGE_FESTA
}

// Parse parses the packet from binary
func (m *MsgMhfChargeFesta) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	m.FestaID = bf.ReadUint32()
	m.GuildID = bf.ReadUint32()
	soulCount := bf.ReadUint16()
	if err := bf.Err(); err != nil {
		return err
	}
	if soulCount > maxClientBatchEntries {
		return fmt.Errorf("festa soul count %d exceeds maximum %d", soulCount, maxClientBatchEntries)
	}
	remaining := len(bf.DataFromCurrent())
	if remaining < 1 || int(soulCount) > (remaining-1)/2 {
		return fmt.Errorf("festa soul count %d exceeds packet data", soulCount)
	}
	m.Souls = make([]uint16, 0, int(soulCount))
	for i := soulCount; i > 0; i-- {
		m.Souls = append(m.Souls, bf.ReadUint16())
	}
	m.Auto = bf.ReadBool()
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfChargeFesta) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	return errors.New("NOT IMPLEMENTED")
}
