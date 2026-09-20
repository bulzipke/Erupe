package mhfpacket

import (
	"bytes"
	"reflect"
	"testing"

	"erupe-ce/common/byteframe"
)

func TestAcquireUdItemClaimRoundTrip(t *testing.T) {
	want := &MsgMhfAcquireUdItem{AckHandle: 0x12345678, RewardType: 1,
		RewardIDs: []uint32{0xabcdef01, 0, 0xffffffff}}
	bf := byteframe.NewByteFrame()
	if err := want.Build(bf, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bf.Data()[:7], []byte{0x12, 0x34, 0x56, 0x78, 0, 1, 3}) {
		t.Fatalf("header: %x", bf.Data())
	}
	var got MsgMhfAcquireUdItem
	if err := got.Parse(byteframe.NewByteFrameFromBytes(bf.Data()), nil); err != nil {
		t.Fatal(err)
	}
	if got.AckHandle != want.AckHandle || got.ItemIDCount != 3 ||
		!reflect.DeepEqual(got.RewardIDs, want.RewardIDs) {
		t.Fatalf("lost opaque reward IDs: %+v", got)
	}
}

func TestAcquireUdItemQueryDoesNotReadIDs(t *testing.T) {
	for _, count := range []byte{0, 2, 255} {
		wire := []byte{0, 0, 0, 1, 1, 0, count, 0xde, 0xad, 0xbe, 0xef}
		bf := byteframe.NewByteFrameFromBytes(wire)
		got := MsgMhfAcquireUdItem{RewardIDs: []uint32{77}}
		if err := got.Parse(bf, nil); err != nil {
			t.Fatal(err)
		}
		if len(got.RewardIDs) != 0 || !bytes.Equal(bf.DataFromCurrent(), wire[7:]) {
			t.Fatal("query consumed the next packet or retained stale claim IDs")
		}
		out := byteframe.NewByteFrame()
		if err := got.Build(out, nil); err != nil || !bytes.Equal(out.Data(), wire[:7]) {
			t.Fatalf("query round trip: %x, %v", out.Data(), err)
		}
	}
}

func TestAcquireUdItemMalformed(t *testing.T) {
	for _, wire := range [][]byte{
		nil, {0, 0, 0, 1, 0, 0}, {0, 0, 0, 1, 2, 0, 0},
		{0, 0, 0, 1, 0, 0, 1, 1, 2, 3},
		append([]byte{0, 0, 0, 1, 0, 0, 33}, make([]byte, 33*4)...),
	} {
		var got MsgMhfAcquireUdItem
		if err := got.Parse(byteframe.NewByteFrameFromBytes(wire), nil); err == nil {
			t.Fatalf("accepted malformed packet: %x", wire)
		}
	}
	for _, got := range []MsgMhfAcquireUdItem{
		{Unk0: 2}, {Unk0: 1, RewardIDs: []uint32{1}}, {RewardIDs: make([]uint32, 33)},
	} {
		bf := byteframe.NewByteFrame()
		if err := got.Build(bf, nil); err == nil || len(bf.Data()) != 0 {
			t.Fatalf("invalid build mutated output: %x, %v", bf.Data(), err)
		}
	}
}

func TestAcquireUdItemMaximumBatch(t *testing.T) {
	pkt := MsgMhfAcquireUdItem{RewardIDs: make([]uint32, 32)}
	bf := byteframe.NewByteFrame()
	if err := pkt.Build(bf, nil); err != nil {
		t.Fatal(err)
	}
	if err := pkt.Parse(byteframe.NewByteFrameFromBytes(bf.Data()), nil); err != nil || len(pkt.RewardIDs) != 32 {
		t.Fatalf("32-reward batch: %v", err)
	}
}
