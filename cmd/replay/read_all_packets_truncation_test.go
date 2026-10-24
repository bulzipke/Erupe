package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"

	"erupe-ce/network/pcap"
)

func TestReadAllPacketsTimestampBoundary(t *testing.T) {
	packet := pcap.PacketRecord{TimestampNs: 123456789, Direction: pcap.DirClientToServer, Opcode: 0x001e, Payload: []byte{0x00, 0x1e, 0xaa}}
	timestamp := binary.BigEndian.AppendUint64(nil, 0x0102030405060708)
	for _, completeRecords := range []int{0, 1} {
		t.Run(fmt.Sprintf("complete_records_%d", completeRecords), func(t *testing.T) {
			for prefixBytes := 0; prefixBytes < len(timestamp); prefixBytes++ {
				t.Run(fmt.Sprintf("timestamp_bytes_%d", prefixBytes), func(t *testing.T) {
					var want []pcap.PacketRecord
					if completeRecords != 0 {
						want = []pcap.PacketRecord{packet}
					}
					path := createTestCapture(t, want)
					file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
					if err != nil {
						t.Fatal(err)
					}
					_, writeErr := file.Write(timestamp[:prefixBytes])
					closeErr := file.Close()
					if writeErr != nil || closeErr != nil {
						t.Fatalf("append timestamp: %v, close: %v", writeErr, closeErr)
					}
					reader, capture, err := openCapture(path)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = capture.Close() }()
					got, err := readAllPackets(reader)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("complete records = %+v, want %+v", got, want)
					}
					if prefixBytes == 0 {
						if err != nil {
							t.Fatalf("clean capture = %v, want success", err)
						}
					} else if err == io.EOF || !errors.Is(err, io.ErrUnexpectedEOF) {
						t.Fatalf("%d-byte timestamp prefix = %v, want io.ErrUnexpectedEOF", prefixBytes, err)
					}
				})
			}
		})
	}
}
