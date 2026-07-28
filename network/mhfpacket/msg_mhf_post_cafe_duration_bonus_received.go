package mhfpacket

import (
	"errors"
	"fmt"

	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfPostCafeDurationBonusReceived represents the MSG_MHF_POST_CAFE_DURATION_BONUS_RECEIVED
type MsgMhfPostCafeDurationBonusReceived struct {
	AckHandle   uint32
	CafeBonusID []uint32
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfPostCafeDurationBonusReceived) Opcode() network.PacketID {
	return network.MSG_MHF_POST_CAFE_DURATION_BONUS_RECEIVED
}

// Parse parses the packet from binary
func (m *MsgMhfPostCafeDurationBonusReceived) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	ids := bf.ReadUint32()
	if err := bf.Err(); err != nil {
		return err
	}
	if ids > maxClientBatchEntries {
		return fmt.Errorf("cafe bonus ID count %d exceeds maximum %d", ids, maxClientBatchEntries)
	}
	if uint64(ids) > uint64(len(bf.DataFromCurrent())/4) {
		return fmt.Errorf("cafe bonus ID count %d exceeds packet data", ids)
	}
	m.CafeBonusID = make([]uint32, 0, int(ids))
	for i := uint32(0); i < ids; i++ {
		m.CafeBonusID = append(m.CafeBonusID, bf.ReadUint32())
	}
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfPostCafeDurationBonusReceived) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	return errors.New("NOT IMPLEMENTED")
}
