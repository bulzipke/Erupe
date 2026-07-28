package mhfpacket

import (
	"testing"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/clientctx"
)

func TestCountedPacketsRejectTruncatedOrOversizedPayloads(t *testing.T) {
	makeData := func(write func(*byteframe.ByteFrame)) []byte {
		bf := byteframe.NewByteFrame()
		write(bf)
		return append([]byte(nil), bf.Data()...)
	}

	tests := []struct {
		name string
		pkt  MHFPacket
		data []byte
	}{
		{
			name: "campaign rewards truncated",
			pkt:  &MsgMhfAcquireItem{},
			data: makeData(func(bf *byteframe.ByteFrame) {
				bf.WriteUint32(1)
				bf.WriteUint16(0)
				bf.WriteUint16(1)
			}),
		},
		{
			name: "titles truncated",
			pkt:  &MsgMhfAcquireTitle{},
			data: makeData(func(bf *byteframe.ByteFrame) {
				bf.WriteUint32(1)
				bf.WriteUint16(1)
				bf.WriteUint16(0)
			}),
		},
		{
			name: "cafe bonuses oversized",
			pkt:  &MsgMhfPostCafeDurationBonusReceived{},
			data: makeData(func(bf *byteframe.ByteFrame) {
				bf.WriteUint32(1)
				bf.WriteUint32(maxClientBatchEntries + 1)
			}),
		},
		{
			name: "present box entries oversized",
			pkt:  &MsgMhfPresentBox{},
			data: makeData(func(bf *byteframe.ByteFrame) {
				bf.WriteUint32(1)
				for i := 0; i < 2; i++ {
					bf.WriteUint32(0)
				}
				bf.WriteUint32(maxClientBatchEntries + 1)
				for i := 0; i < 4; i++ {
					bf.WriteUint32(0)
				}
				bf.WriteBytes(make([]byte, (maxClientBatchEntries+1)*4))
			}),
		},
		{
			name: "festa souls truncated",
			pkt:  &MsgMhfChargeFesta{},
			data: makeData(func(bf *byteframe.ByteFrame) {
				bf.WriteUint32(1)
				bf.WriteUint32(2)
				bf.WriteUint32(3)
				bf.WriteUint16(1)
			}),
		},
		{
			name: "union items truncated",
			pkt:  &MsgMhfUpdateUnionItem{},
			data: makeData(func(bf *byteframe.ByteFrame) {
				bf.WriteUint32(1)
				bf.WriteUint16(1)
				bf.WriteUint16(0)
			}),
		},
		{
			name: "warehouse items truncated",
			pkt:  &MsgMhfUpdateWarehouse{},
			data: makeData(func(bf *byteframe.ByteFrame) {
				bf.WriteUint32(1)
				bf.WriteUint8(0)
				bf.WriteUint8(0)
				bf.WriteUint16(1)
				bf.WriteUint16(0)
			}),
		},
		{
			name: "goocoo slots oversized",
			pkt:  &MsgMhfUpdateGuacot{},
			data: makeData(func(bf *byteframe.ByteFrame) {
				bf.WriteUint32(1)
				bf.WriteUint16(6)
				bf.WriteUint16(0)
			}),
		},
		{
			name: "guild icon parts oversized",
			pkt:  &MsgMhfUpdateGuildIcon{},
			data: makeData(func(bf *byteframe.ByteFrame) {
				bf.WriteUint32(1)
				bf.WriteUint32(2)
				bf.WriteUint16(maxGuildIconParts + 1)
				bf.WriteUint16(0)
				bf.WriteBytes(make([]byte, (maxGuildIconParts+1)*14))
			}),
		},
		{
			name: "terminal log batch oversized",
			pkt:  &MsgSysTerminalLog{},
			data: makeData(func(bf *byteframe.ByteFrame) {
				bf.WriteUint32(1)
				bf.WriteUint32(2)
				bf.WriteUint16(maxTerminalLogEntries + 1)
				bf.WriteUint16(0)
			}),
		},
	}

	ctx := &clientctx.ClientContext{RealClientMode: cfg.ZZ}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bf := byteframe.NewByteFrameFromBytes(tt.data)
			if err := tt.pkt.Parse(bf, ctx); err == nil {
				t.Fatal("Parse() accepted malformed counted payload")
			}
		})
	}
}

func TestBoundedIdentifierPacketsRejectMalformedLengths(t *testing.T) {
	ctx := &clientctx.ClientContext{RealClientMode: cfg.ZZ}

	semaphore := byteframe.NewByteFrame()
	semaphore.WriteUint32(1)
	semaphore.WriteUint8(0)
	semaphore.WriteUint8(1)
	semaphore.WriteUint8(0)
	semaphore.WriteUint8(0)
	if err := (&MsgSysCreateAcquireSemaphore{}).Parse(
		byteframe.NewByteFrameFromBytes(semaphore.Data()), ctx,
	); err == nil {
		t.Fatal("semaphore parser accepted a zero-length identifier")
	}

	stageBinary := byteframe.NewByteFrame()
	stageBinary.WriteUint8(0)
	stageBinary.WriteUint8(0)
	stageBinary.WriteUint8(1)
	stageBinary.WriteUint16(0)
	stageBinary.WriteUint8('x') // no required null terminator
	if err := (&MsgSysSetStageBinary{}).Parse(
		byteframe.NewByteFrameFromBytes(stageBinary.Data()), ctx,
	); err == nil {
		t.Fatal("stage-binary parser accepted a non-terminated identifier")
	}
}
