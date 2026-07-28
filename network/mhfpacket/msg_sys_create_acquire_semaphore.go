package mhfpacket

import (
	"errors"
	"fmt"

	"erupe-ce/common/bfutil"
	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgSysCreateAcquireSemaphore represents the MSG_SYS_CREATE_ACQUIRE_SEMAPHORE
type MsgSysCreateAcquireSemaphore struct {
	AckHandle   uint32
	Unk0        uint16
	PlayerCount uint8
	SemaphoreID string
}

// Opcode returns the ID associated with this packet type.
func (m *MsgSysCreateAcquireSemaphore) Opcode() network.PacketID {
	return network.MSG_SYS_CREATE_ACQUIRE_SEMAPHORE
}

// Parse parses the packet from binary
func (m *MsgSysCreateAcquireSemaphore) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	m.Unk0 = bf.ReadUint16()
	if ctx.RealClientMode >= cfg.S7 { // Assuming this was added with Ravi?
		m.PlayerCount = bf.ReadUint8()
	}
	semaphoreIDLength := bf.ReadUint8()
	if semaphoreIDLength == 0 || semaphoreIDLength > 64 {
		return fmt.Errorf("invalid semaphore ID length %d", semaphoreIDLength)
	}
	semaphoreID := bf.ReadBytes(uint(semaphoreIDLength))
	if err := bf.Err(); err != nil {
		return err
	}
	if semaphoreID[len(semaphoreID)-1] != 0 {
		return fmt.Errorf("semaphore ID is not null terminated")
	}
	m.SemaphoreID = string(bfutil.UpToNull(semaphoreID))
	return nil
}

// Build builds a binary packet from the current data.
func (m *MsgSysCreateAcquireSemaphore) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	return errors.New("NOT IMPLEMENTED")
}
