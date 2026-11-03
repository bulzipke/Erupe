package channelserver

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"erupe-ce/network/mhfpacket"
)

func TestDivaTotalPointInfoShopUnlockWire(t *testing.T) {
	srv := createMockServer()
	srv.erupeConfig.DebugOptions.DivaOverride = -1
	event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Unix() - int64(time.Hour/time.Second))}
	srv.divaRepo = &mockDivaRepo{
		events: []DivaEvent{event},
		points: map[[2]uint32][2]int64{
			{1, 42}: {70000, 1816},
			{2, 42}: {100000, 16837},
			{1, 41}: {100000000, 100000000}, // A previous round must not unlock this one.
		},
	}
	s := createMockSession(56, srv)
	handleMsgMhfGetUdTotalPointInfo(s, &mhfpacket.MsgMhfGetUdTotalPointInfo{AckHandle: 12345})
	ack := readAck(t, s)
	if ack.AckHandle != 12345 || ack.ErrorCode != 0 || !ack.IsBufferResponse {
		t.Fatalf("wrong total-info ACK: %+v", ack)
	}
	p := ack.Payload
	if len(p) != 585 || p[0] != 0 || binary.BigEndian.Uint64(p[577:]) != 188653 {
		t.Fatalf("wrong total-info header, length or total: %x", p)
	}
	wantSongs := []uint64{
		500000, 1000000, 2000000, 3000000, 5000000, 7000000, 10000000,
		15000000, 20000000, 30000000, 50000000, 70000000, 100000000,
	}
	wantUnlocks := []uint64{100000, 200000, 300000}
	for i := 0; i < 64; i++ {
		var wantTarget uint64
		var wantType uint8
		if i < len(wantSongs) {
			wantTarget = wantSongs[i]
		} else if j := i - len(wantSongs); j < len(wantUnlocks) {
			wantTarget, wantType = wantUnlocks[j], uint8(j+1)
		}
		if target, kind := binary.BigEndian.Uint64(p[1+i*8:]), p[513+i]; target != wantTarget || kind != wantType {
			t.Fatalf("slot %d: target=%d type=%d, want target=%d type=%d", i, target, kind, wantTarget, wantType)
		}
	}
}

func TestDivaTotalPointInfoUnlockAndSongBoundaries(t *testing.T) {
	if len(udMilestones)+len(udShopUnlockMilestones) > 64 {
		t.Fatal("total-point targets exceed the native 64-slot table")
	}
	for _, tt := range []struct {
		total uint64
		level uint8
		songs uint8
	}{
		{0, 0, 0}, {99999, 0, 0}, {100000, 1, 0}, {188653, 1, 0},
		{199999, 1, 0}, {200000, 2, 0}, {299999, 2, 0}, {300000, 3, 0},
		{499999, 3, 0}, {500000, 3, 1}, {999999, 3, 1}, {1000000, 3, 2}, {100000000, 3, 13},
	} {
		p := divaTotalPointPayload(tt.total)
		var level, songs uint8
		for i := 0; i < 64; i++ {
			target, kind := binary.BigEndian.Uint64(p[1+i*8:]), p[513+i]
			if target == 0 || tt.total < target {
				continue
			}
			if kind == 0 {
				songs++
			} else if kind <= 3 && kind > level {
				level = kind
			}
		}
		if level != tt.level || songs != tt.songs || divaBattleSongEarned(int64(tt.total)) != tt.songs {
			t.Fatalf("total %d: shop level=%d songs=%d, want level=%d songs=%d", tt.total, level, songs, tt.level, tt.songs)
		}
	}
}

func TestDivaTotalPointInfoReadFailure(t *testing.T) {
	for _, source := range []string{"event", "total"} {
		t.Run(source, func(t *testing.T) {
			srv := createMockServer()
			srv.erupeConfig.DebugOptions.DivaOverride = -1
			repo := &mockDivaRepo{}
			if source == "event" {
				repo.eventsErr = errors.New("event read failed")
			} else {
				repo.totalErr = errors.New("total read failed")
			}
			srv.divaRepo = repo
			s := createMockSession(56, srv)
			handleMsgMhfGetUdTotalPointInfo(s, &mhfpacket.MsgMhfGetUdTotalPointInfo{AckHandle: 7})
			if ack := readAck(t, s); ack.AckHandle != 7 || ack.ErrorCode == 0 {
				t.Fatalf("failed %s read advertised success: %+v", source, ack)
			}
		})
	}
}
