package mhfpacket

import (
	"fmt"

	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfAcquireUdItem represents the MSG_MHF_ACQUIRE_UD_ITEM
type MsgMhfAcquireUdItem struct {
	AckHandle uint32
	Unk0      uint8 // 1: query available rewards; 0: acknowledge acquired reward IDs.
	// from gal
	// daily = 0
	// personal = 1
	// personal rank = 2
	// guild rank = 3
	// gcp = 4
	// from cat
	// treasure achievement = 5
	// personal achievement = 6
	// guild achievement = 7
	RewardType  uint8
	ItemIDCount uint8
	Unk3        []byte
	// RewardIDs are opaque IDs returned by the availability response, NOT item IDs.
	// HD FUN_114fde60 only serializes them when Unk0 == 0.
	RewardIDs []uint32
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfAcquireUdItem) Opcode() network.PacketID {
	return network.MSG_MHF_ACQUIRE_UD_ITEM
}

// Parse parses the packet from binary
func (m *MsgMhfAcquireUdItem) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.RewardIDs = nil
	m.Unk3 = nil
	m.AckHandle = bf.ReadUint32()
	m.Unk0 = bf.ReadUint8()
	m.RewardType = bf.ReadUint8()
	m.ItemIDCount = bf.ReadUint8()
	if err := bf.Err(); err != nil {
		return err
	}
	if m.Unk0 > 1 {
		return fmt.Errorf("invalid UD reward query flag %d", m.Unk0)
	}
	if m.Unk0 == 1 {
		// Query packets have no ID array, even if the count byte is nonzero.
		// Do not consume bytes belonging to the next packet in the stream.
		return nil
	}
	if m.ItemIDCount > 32 {
		return fmt.Errorf("UD reward count %d exceeds client batch limit 32", m.ItemIDCount)
	}
	if int(m.ItemIDCount) > len(bf.DataFromCurrent())/4 {
		return fmt.Errorf("UD item ID count %d exceeds packet data", m.ItemIDCount)
	}
	m.RewardIDs = make([]uint32, int(m.ItemIDCount))
	for i := range m.RewardIDs {
		m.RewardIDs[i] = bf.ReadUint32()
	}
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgMhfAcquireUdItem) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	if m.Unk0 > 1 {
		return fmt.Errorf("invalid UD reward query flag %d", m.Unk0)
	}
	if (m.Unk0 == 1 && len(m.RewardIDs) != 0) || len(m.RewardIDs) > 32 {
		return fmt.Errorf("invalid UD reward ID array for query flag %d", m.Unk0)
	}
	count := m.ItemIDCount
	if m.Unk0 == 0 {
		count = uint8(len(m.RewardIDs))
	}
	bf.WriteUint32(m.AckHandle)
	bf.WriteUint8(m.Unk0)
	bf.WriteUint8(m.RewardType)
	bf.WriteUint8(count)
	for _, id := range m.RewardIDs {
		bf.WriteUint32(id)
	}
	return nil
}
