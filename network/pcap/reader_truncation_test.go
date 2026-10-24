package pcap

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
)

func TestReadPacketTimestampBoundary(t *testing.T) {
	packet := PacketRecord{TimestampNs: 123456789, Direction: DirClientToServer, Opcode: 0x001e, Payload: []byte{0x00, 0x1e, 0xaa}}
	timestamp := binary.BigEndian.AppendUint64(nil, 0x0102030405060708)
	for _, completeRecords := range []int{0, 1} {
		t.Run(fmt.Sprintf("complete_records_%d", completeRecords), func(t *testing.T) {
			for prefixBytes := 0; prefixBytes < len(timestamp); prefixBytes++ {
				t.Run(fmt.Sprintf("timestamp_bytes_%d", prefixBytes), func(t *testing.T) {
					var capture bytes.Buffer
					writer, err := NewWriter(&capture, FileHeader{Version: FormatVersion, ServerType: ServerTypeChannel}, SessionMetadata{})
					if err != nil {
						t.Fatal(err)
					}
					if completeRecords != 0 {
						if err := writer.WritePacket(packet); err != nil {
							t.Fatal(err)
						}
					}
					if err := writer.Flush(); err != nil {
						t.Fatal(err)
					}
					capture.Write(timestamp[:prefixBytes])
					reader, err := NewReader(bytes.NewReader(capture.Bytes()))
					if err != nil {
						t.Fatal(err)
					}
					if completeRecords != 0 {
						got, err := reader.ReadPacket()
						if err != nil || !reflect.DeepEqual(got, packet) {
							t.Fatalf("complete record = %+v, %v; want %+v", got, err, packet)
						}
					}
					_, err = reader.ReadPacket()
					if prefixBytes == 0 {
						if err != io.EOF {
							t.Fatalf("clean record boundary = %v, want io.EOF", err)
						}
					} else if err == io.EOF || !errors.Is(err, io.ErrUnexpectedEOF) {
						t.Fatalf("%d-byte timestamp prefix = %v, want io.ErrUnexpectedEOF", prefixBytes, err)
					}
				})
			}
		})
	}
}
