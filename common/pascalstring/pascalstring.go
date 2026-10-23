package pascalstring

import (
	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
)

// encode limits the payload so its byte count plus the NUL fits the prefix.
// Transformed text uses the Korean client's CP949 encoding throughout.
func encode(x string, t bool, maxBytes uint64) []byte {
	b := []byte(x)
	if t {
		b = stringsupport.UTF8ToSJIS(x)
	}
	if uint64(len(b)) > maxBytes {
		if t {
			b = stringsupport.TruncateCP949(b, int(maxBytes))
		} else {
			b = b[:int(maxBytes)]
		}
	}
	return b
}

// Uint8 writes x as a null-terminated string with a uint8 length prefix. If t
// is true the string is first encoded to CP949.
func Uint8(bf *byteframe.ByteFrame, x string, t bool) {
	b := encode(x, t, 254)
	bf.WriteUint8(uint8(len(b) + 1))
	bf.WriteNullTerminatedBytes(b)
}

// Uint16 writes x as a null-terminated string with a uint16 length prefix. If
// t is true the string is first encoded to CP949.
func Uint16(bf *byteframe.ByteFrame, x string, t bool) {
	b := encode(x, t, 65534)
	bf.WriteUint16(uint16(len(b) + 1))
	bf.WriteNullTerminatedBytes(b)
}

// Uint32 writes x as a null-terminated string with a uint32 length prefix. If
// t is true the string is first encoded to CP949.
func Uint32(bf *byteframe.ByteFrame, x string, t bool) {
	b := encode(x, t, uint64(^uint32(0))-1)
	bf.WriteUint32(uint32(len(b) + 1))
	bf.WriteNullTerminatedBytes(b)
}
