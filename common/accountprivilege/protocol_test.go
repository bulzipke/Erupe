package accountprivilege

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"testing"
	"time"
)

func TestWireAuthentication(t *testing.T) {
	b := make([]byte, RequestSize)
	copy(b, RequestMagic)
	binary.BigEndian.PutUint32(b[8:], 123)
	binary.BigEndian.PutUint32(b[12:], 456)
	for i := 16; i < 32; i++ {
		b[i] = byte(i)
	}
	const token = "0123456789abcdef"
	copy(b[32:], sign(b[:32], token))
	r, err := ParseRequest(b)
	if err != nil || r.TokenID != 123 || r.CharacterID != 456 || !Authenticate(b, token) {
		t.Fatal(r, err)
	}
	response := Response(r, time.Unix(1700000000, 0), time.Unix(1702592000, 0), time.Time{}, token)
	if len(response) != ResponseSize || string(response[:8]) != ResponseMagic || binary.BigEndian.Uint32(response[12:]) != 1702592000 || binary.BigEndian.Uint32(response[16:]) != 0 || string(response[24:40]) != string(b[16:32]) {
		t.Fatal("response layout")
	}
	m := hmac.New(sha256.New, []byte(token))
	_, _ = m.Write(response[:40])
	if !hmac.Equal(m.Sum(nil), response[40:]) {
		t.Fatal("response HMAC")
	}
	for i := range b {
		changed := append([]byte(nil), b...)
		changed[i] ^= 1
		if Authenticate(changed, token) {
			t.Fatalf("accepted modification at %d", i)
		}
	}
	if Authenticate(b, "short") || Authenticate(b[:63], token) {
		t.Fatal("invalid auth accepted")
	}
	for i := 0; i < RequestSize; i++ {
		if _, err := ParseRequest(b[:i]); err == nil {
			t.Fatalf("truncated frame %d", i)
		}
	}
}

func TestTimestampRange(t *testing.T) {
	for _, v := range []time.Time{{}, time.Unix(-1, 0), time.Unix(0, 0), time.Unix(0x80000000, 0)} {
		if Timestamp(v) != 0 {
			t.Fatal(v)
		}
	}
	if Timestamp(time.Unix(0x7fffffff, 0)) != 0x7fffffff {
		t.Fatal("maximum")
	}
}
