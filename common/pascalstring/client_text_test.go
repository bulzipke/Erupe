package pascalstring

import (
	"bytes"
	"strings"
	"testing"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
)

func TestPascalStringKoreanWireEncoding(t *testing.T) {
	for _, tt := range []struct {
		name string
		write func(*byteframe.ByteFrame, string, bool)
		prefix int
	}{
		{"Uint8", Uint8, 1},
		{"Uint16", Uint16, 2},
		{"Uint32", Uint32, 4},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bf := byteframe.NewByteFrame()
			tt.write(bf, "한글이름", true)
			data := bf.Data()
			// Fixed CP949 fixture: 한 C7 D1, 글 B1 DB, 이 C0 CC, 름 B8 A7.
			want := []byte{0xc7, 0xd1, 0xb1, 0xdb, 0xc0, 0xcc, 0xb8, 0xa7, 0}
			if !bytes.Equal(data[tt.prefix:], want) {
				t.Fatalf("wire bytes = %x, want %x", data[tt.prefix:], want)
			}
			for i, b := range data[:tt.prefix] {
				wantLength := byte(0)
				if i == tt.prefix-1 {
					wantLength = byte(len(want))
				}
				if b != wantLength {
					t.Errorf("prefix[%d] = %d, want %d", i, b, wantLength)
				}
			}
		})
	}
}

func TestPascalStringLengthLimitsPreserveKorean(t *testing.T) {
	for _, tt := range []struct {
		name string
		write func(*byteframe.ByteFrame, string, bool)
		prefix int
		max int
	}{
		{"Uint8", Uint8, 1, 254},
		{"Uint16", Uint16, 2, 65534},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bf := byteframe.NewByteFrame()
			text := "A" + strings.Repeat("한", tt.max/2+1)
			tt.write(bf, text, true)
			bf.WriteUint8(0x5a)
			data := bf.Data()
			payload := data[tt.prefix:len(data)-1]
			if len(payload) != tt.max || payload[len(payload)-1] != 0 {
				t.Fatalf("payload length = %d, final byte = %x", len(payload), payload[len(payload)-1])
			}
			want := "A" + strings.Repeat("한", (tt.max-1)/2)
			if decoded := stringsupport.SJISToUTF8Lossy(payload[:len(payload)-1]); decoded != want {
				t.Fatalf("truncated text is not a whole Korean string")
			}
			var length uint32
			for _, b := range data[:tt.prefix] {
				length = length<<8 | uint32(b)
			}
			if int(length) != len(payload) || data[len(data)-1] != 0x5a {
				t.Fatalf("length prefix = %d, payload length = %d", length, len(payload))
			}
		})
	}
}

func TestPascalStringUnsupportedRuneKeepsSurroundingText(t *testing.T) {
	bf := byteframe.NewByteFrame()
	Uint8(bf, "앞😀뒤", true)
	if got := bf.Data(); !bytes.Equal(got, []byte{5, 0xbe, 0xd5, 0xb5, 0xda, 0}) {
		t.Fatalf("wire bytes = %x", got)
	}
}
