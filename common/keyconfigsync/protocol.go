// Package keyconfigsync carries opaque native keyconfig files over the existing
// entrance port. Session HMACs authenticate both directions without sending tokens.
package keyconfigsync

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

const (
	RequestMagic       = "ERUKCF02"
	ResponseMagic      = "ERUKCR02"
	HeaderSize         = 48
	ResponseHeaderSize = 40
	MACSize            = 32
	MaxBytes           = 512 * 1024 // Same upper bound as the native reader.
	MaxIniBytes        = 128 * 1024
	SlotCount          = 3
	MaxRevision        = 0x7ffffffe
	Load               = 0
	Save               = 1
	OK                 = 0
	Absent             = 1
	Conflict           = 2
	Unavailable        = 3
	Throttled          = 4
)

type Request struct {
	TokenID, CharacterID, Operation, Slot, Revision, Size uint32
	Nonce                                                 [16]byte
}

func ParseHeader(b []byte) (Request, error) {
	var r Request
	if len(b) != HeaderSize || string(b[:8]) != RequestMagic {
		return r, errors.New("invalid keyconfig header")
	}
	r.TokenID = binary.BigEndian.Uint32(b[8:])
	r.CharacterID = binary.BigEndian.Uint32(b[12:])
	r.Operation = binary.BigEndian.Uint32(b[16:])
	r.Slot = binary.BigEndian.Uint32(b[20:])
	r.Revision = binary.BigEndian.Uint32(b[24:])
	r.Size = binary.BigEndian.Uint32(b[28:])
	copy(r.Nonce[:], b[32:])
	if r.TokenID == 0 || r.CharacterID == 0 || r.Operation > Save || r.Slot >= SlotCount || r.Size > Limit(r.Slot) || r.Revision > MaxRevision ||
		(r.Operation == Load && (r.Size != 0 || r.Revision != 0)) || (r.Operation == Save && r.Size == 0) {
		return r, errors.New("invalid keyconfig request")
	}
	return r, nil
}

func Sign(b []byte, token string) []byte {
	m := hmac.New(sha256.New, []byte(token))
	_, _ = m.Write(b)
	return m.Sum(nil)
}

func Authenticate(b []byte, token string) bool {
	return len(token) == 16 && len(b) >= HeaderSize+2*MACSize && hmac.Equal(b[len(b)-MACSize:], Sign(b[:len(b)-MACSize], token))
}
func AuthenticateHeader(b []byte, token string) bool {
	return len(token) == 16 && len(b) == HeaderSize+MACSize && hmac.Equal(b[HeaderSize:], Sign(b[:HeaderSize], token))
}
func Limit(slot uint32) uint32 {
	if slot == 2 {
		return MaxIniBytes
	}
	return MaxBytes
}

func Response(r Request, status, revision uint32, data []byte, token string) []byte {
	b := make([]byte, ResponseHeaderSize+len(data)+MACSize)
	copy(b, ResponseMagic)
	binary.BigEndian.PutUint32(b[8:], status)
	binary.BigEndian.PutUint32(b[12:], revision)
	binary.BigEndian.PutUint32(b[16:], uint32(len(data)))
	binary.BigEndian.PutUint32(b[20:], r.Slot)
	copy(b[24:], r.Nonce[:])
	copy(b[ResponseHeaderSize:], data)
	copy(b[len(b)-MACSize:], Sign(b[:len(b)-MACSize], token))
	return b
}
