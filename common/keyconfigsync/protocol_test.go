package keyconfigsync

import (
	"encoding/binary"
	"testing"
)

func TestHeaderAndAuthentication(t *testing.T) {
	b := make([]byte, HeaderSize+2*MACSize)
	copy(b, RequestMagic)
	binary.BigEndian.PutUint32(b[8:], 123)
	binary.BigEndian.PutUint32(b[12:], 456)
	copy(b[HeaderSize:], Sign(b[:HeaderSize], "0123456789abcdef"))
	copy(b[len(b)-MACSize:], Sign(b[:len(b)-MACSize], "0123456789abcdef"))
	if _, err := ParseHeader(b[:HeaderSize]); err != nil || !Authenticate(b, "0123456789abcdef") {
		t.Fatal(err)
	}
	for i := range b {
		b[i] ^= 1
		if Authenticate(b, "0123456789abcdef") {
			t.Fatalf("tamper %d", i)
		}
		b[i] ^= 1
	}
	for _, at := range []int{16, 20, 24, 28} {
		bad := append([]byte(nil), b[:HeaderSize]...)
		binary.BigEndian.PutUint32(bad[at:], 0xffffffff)
		if _, err := ParseHeader(bad); err == nil {
			t.Fatalf("invalid field %d", at)
		}
	}
	if ValidData([]byte("<keyconfig>")) || ValidData([]byte("<keyconfig>\x00</keyconfig>")) || ValidData(make([]byte, MaxBytes+1)) {
		t.Fatal("bad data accepted")
	}
}
