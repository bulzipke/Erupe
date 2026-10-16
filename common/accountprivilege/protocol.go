// Package accountprivilege carries eligibility over the existing entrance port.
// Session HMACs authenticate both directions without transmitting the token.
package accountprivilege

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"
)

const RequestMagic = "ERUPRV01"
const ResponseMagic = "ERUPRS01"
const RequestSize = 64
const ResponseSize = 72

type Request struct {
	TokenID, CharacterID uint32
	Nonce                [16]byte
}

func Timestamp(t time.Time) uint32 {
	// Native comparisons are signed 32-bit. NULL/pre-epoch/overflow must not wrap.
	if t.IsZero() || t.Unix() <= 0 || t.Unix() > 0x7fffffff {
		return 0
	}
	return uint32(t.Unix())
}

func sign(data []byte, token string) []byte {
	m := hmac.New(sha256.New, []byte(token))
	_, _ = m.Write(data)
	return m.Sum(nil)
}

func ParseRequest(b []byte) (Request, error) {
	var r Request
	if len(b) != RequestSize || string(b[:8]) != RequestMagic {
		return r, errors.New("invalid privilege frame")
	}
	r.TokenID = binary.BigEndian.Uint32(b[8:12])
	r.CharacterID = binary.BigEndian.Uint32(b[12:16])
	copy(r.Nonce[:], b[16:32])
	return r, nil
}

func Authenticate(b []byte, token string) bool {
	return len(b) == RequestSize && len(token) == 16 && hmac.Equal(b[32:], sign(b[:32], token))
}

func Response(r Request, now, newcomer, returning time.Time, token string) []byte {
	b := make([]byte, ResponseSize)
	copy(b, ResponseMagic)
	binary.BigEndian.PutUint32(b[8:12], Timestamp(now))
	binary.BigEndian.PutUint32(b[12:16], Timestamp(newcomer))
	binary.BigEndian.PutUint32(b[16:20], Timestamp(returning))
	// Bytes 20..23 are reserved zeroes in version 1.
	copy(b[24:40], r.Nonce[:])
	copy(b[40:], sign(b[:40], token))
	return b
}
