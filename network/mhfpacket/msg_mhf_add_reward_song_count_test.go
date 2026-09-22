package mhfpacket

import (
	"bytes"
	"reflect"
	"testing"

	"erupe-ce/common/byteframe"
)

func TestRewardSongCountNativeWire(t *testing.T) {
	want := MsgMhfAddRewardSongCount{AckHandle: 0x12345678, PrayerID: 0x10203040, Entries: []uint16{1, 3, 4, 8}}
	bf := byteframe.NewByteFrame()
	if err := want.Build(bf, nil); err != nil {
		t.Fatal(err)
	}
	wire := []byte{0x12, 0x34, 0x56, 0x78, 0x10, 0x20, 0x30, 0x40, 0, 8, 4, 0, 1, 0, 3, 0, 4, 0, 8}
	if !bytes.Equal(bf.Data(), wire) {
		t.Fatalf("wrong native body: %x", bf.Data())
	}
	input := byteframe.NewByteFrameFromBytes(append(wire, 0xaa, 0xbb))
	var got MsgMhfAddRewardSongCount
	if err := got.Parse(input, nil); err != nil || got.AckHandle != want.AckHandle || got.PrayerID != want.PrayerID || got.ArraySizeBytes != 8 || got.Count != 4 || !reflect.DeepEqual(got.Entries, want.Entries) {
		t.Fatalf("parsed %+v: %v", got, err)
	}
	if !bytes.Equal(input.DataFromCurrent(), []byte{0xaa, 0xbb}) {
		t.Fatal("consumed the following packet")
	}
	for size := 0; size < len(wire); size++ {
		if err := got.Parse(byteframe.NewByteFrameFromBytes(wire[:size]), nil); err == nil {
			t.Fatalf("accepted truncated length %d", size)
		}
	}
	for _, size := range []byte{0, 7, 9} {
		bad := append([]byte(nil), wire...)
		bad[9] = size
		if err := got.Parse(byteframe.NewByteFrameFromBytes(bad), nil); err == nil {
			t.Fatalf("accepted mismatched array size %d", size)
		}
	}
}

func TestRewardSongCountBuildRejectsOverflow(t *testing.T) {
	pkt := MsgMhfAddRewardSongCount{Entries: make([]uint16, 256)}
	bf := byteframe.NewByteFrame()
	if err := pkt.Build(bf, nil); err == nil || len(bf.Data()) != 0 {
		t.Fatal("oversized count was encoded or partially written")
	}
}
