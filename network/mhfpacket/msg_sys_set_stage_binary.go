package mhfpacket

import (
	"fmt"

	"erupe-ce/common/bfutil"
	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgSysSetStageBinary represents the MSG_SYS_SET_STAGE_BINARY
type MsgSysSetStageBinary struct {
	BinaryType0    uint8
	BinaryType1    uint8 // Index
	StageID        string
	RawDataPayload []byte
}

// Opcode returns the ID associated with this packet type.
func (m *MsgSysSetStageBinary) Opcode() network.PacketID {
	return network.MSG_SYS_SET_STAGE_BINARY
}

// Parse parses the packet from binary
func (m *MsgSysSetStageBinary) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.BinaryType0 = bf.ReadUint8()
	m.BinaryType1 = bf.ReadUint8()
	stageIDLength := bf.ReadUint8()
	dataSize := bf.ReadUint16()
	if stageIDLength == 0 || stageIDLength > 0x20 {
		return fmt.Errorf("invalid stage ID length %d", stageIDLength)
	}
	if dataSize > 0x400 {
		return fmt.Errorf("stage binary payload too large: %d", dataSize)
	}
	stageID := bf.ReadBytes(uint(stageIDLength))
	if err := bf.Err(); err != nil {
		return err
	}
	if stageID[len(stageID)-1] != 0 {
		return fmt.Errorf("stage ID is not null terminated")
	}
	m.StageID = string(bfutil.UpToNull(stageID))
	m.RawDataPayload = bf.ReadBytes(uint(dataSize))
	return bf.Err()
}

// Build builds a binary packet from the current data.
func (m *MsgSysSetStageBinary) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	return fmt.Errorf("MsgSysSetStageBinary.Build: not implemented")
}
