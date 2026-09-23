package channelserver

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestDivaSpecialPugiBounds(t *testing.T) {
	for slot := uint8(0); slot <= 4; slot++ {
		for _, outfit := range []uint32{0, 1, 9, 10, 255, 256, 0xffffffff} {
			want := slot >= 1 && slot <= 3 && outfit <= 9
			if validDivaSpecialPugiClothing(slot, outfit) != want {
				t.Fatal(slot, outfit)
			}
		}
	}
}

type specialPugiMockRepo struct {
	mockDivaRepo
	err                 error
	calls               int
	char, guild, outfit uint32
	slot                uint8
}

func (r *specialPugiMockRepo) ChangeDivaSpecialPugi(char, guild uint32, slot uint8, outfit uint32, _ int) error {
	r.calls++
	r.char, r.guild, r.slot, r.outfit = char, guild, slot, outfit
	return r.err
}

func TestDivaSpecialPugiOperateGuildACK(t *testing.T) {
	for _, tc := range []struct {
		name                                            string
		action                                          mhfpacket.OperateGuildAction
		outfit                                          uint32
		mode                                            int
		client                                          cfg.Mode
		nilData, shortData, applicant, foreign, shifted bool
		err                                             error
		wantCall, wantSuccess                           bool
	}{
		{name: "first", action: 25, mode: -1, client: cfg.ZZ, wantCall: true, wantSuccess: true},
		{name: "second", action: 26, outfit: 5, mode: 3, client: cfg.ZZ, wantCall: true, wantSuccess: true},
		{name: "third", action: 27, outfit: 9, mode: 3, client: cfg.ZZ, wantCall: true, wantSuccess: true},
		{name: "invalid outfit", action: 25, outfit: 10, mode: 3, client: cfg.ZZ},
		{name: "no truncation", action: 25, outfit: 256, mode: 3, client: cfg.ZZ},
		{name: "missing data", action: 25, mode: 3, client: cfg.ZZ, nilData: true},
		{name: "truncated data", action: 25, mode: 3, client: cfg.ZZ, shortData: true},
		{name: "prayer", action: 25, mode: 1, client: cfg.ZZ},
		{name: "battle", action: 25, mode: 2, client: cfg.ZZ},
		{name: "disabled", action: 25, mode: 0, client: cfg.ZZ},
		{name: "old client", action: 25, mode: 3, client: cfg.Z1},
		{name: "clock shift", action: 25, mode: 3, client: cfg.ZZ, shifted: true},
		{name: "applicant", action: 25, mode: 3, client: cfg.ZZ, applicant: true},
		{name: "foreign guild", action: 25, mode: 3, client: cfg.ZZ, foreign: true},
		{name: "repository authorization", action: 25, mode: 3, client: cfg.ZZ, err: errDivaSpecialPugiUnavailable, wantCall: true},
		{name: "database failure", action: 25, mode: 3, client: cfg.ZZ, err: errors.New("db unavailable"), wantCall: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = tc.client
			srv.erupeConfig.DebugOptions.DivaOverride = tc.mode
			if tc.shifted {
				hour := 12
				srv.erupeConfig.DebugOptions.InGameTimeOverrideHour = &hour
			}
			g := &mockGuildRepo{guild: &Guild{ID: 10}, membership: &GuildMember{GuildID: 10, CharID: 1, IsLeader: true, IsApplicant: tc.applicant}}
			if tc.foreign {
				g.membership.GuildID = 11
			}
			srv.guildRepo = g
			r := &specialPugiMockRepo{err: tc.err}
			srv.divaRepo = r
			s := createMockSession(1, srv)
			bf := byteframe.NewByteFrame()
			bf.WriteUint32(tc.outfit)
			_, _ = bf.Seek(0, 0)
			if tc.shortData {
				bf = byteframe.NewByteFrameFromBytes([]byte{0, 0})
			}
			if tc.nilData {
				bf = nil
			}
			handleMsgMhfOperateGuild(s, &mhfpacket.MsgMhfOperateGuild{AckHandle: 4, GuildID: 10, Action: tc.action, Data1: bf})
			a := readAck(t, s)
			if (a.ErrorCode == 0) != tc.wantSuccess || a.IsBufferResponse || len(a.Payload) != 4 {
				t.Fatal(a)
			}
			if (r.calls == 1) != tc.wantCall || g.savedGuild != nil {
				t.Fatal("wrong mutation route", r, g.savedGuild)
			}
			if tc.wantCall && (r.char != 1 || r.guild != 10 || r.slot != uint8(tc.action-24) || r.outfit != tc.outfit) {
				t.Fatal(r)
			}
		})
	}
}

func TestDivaSpecialPugiInfoGuildIndependentClothing(t *testing.T) {
	for _, mode := range []cfg.Mode{cfg.G10, cfg.Z1, cfg.ZZ} {
		srv := guildInfoServer()
		srv.erupeConfig.RealClientMode = mode
		joined := time.Now()
		g := &Guild{ID: 10, PugiOutfit1: 12, PugiOutfit2: 13, PugiOutfit3: 14,
			DivaPugiOutfit1: 3, DivaPugiOutfit2: 5, DivaPugiOutfit3: 9, PugiOutfits: 0x01020304}
		srv.guildRepo = &mockGuildRepo{guild: g, membership: &GuildMember{GuildID: 10, CharID: 1, JoinedAt: &joined}}
		s := createMockSession(1, srv)
		handleMsgMhfInfoGuild(s, &mhfpacket.MsgMhfInfoGuild{AckHandle: 1, GuildID: 10})
		ack := readAck(t, s)
		if ack.ErrorCode != 0 || !ack.IsBufferResponse {
			t.Fatal(ack)
		}
		// 52 fixed bytes, followed by three empty Pascal8/NUL names (2 each).
		if len(ack.Payload) < 68 || !bytes.Equal(ack.Payload[58:61], []byte{12, 13, 14}) {
			t.Fatalf("normal clothes changed: mode=%v payload=%x", mode, ack.Payload)
		}
		mask := 61
		if mode >= cfg.Z1 {
			if !bytes.Equal(ack.Payload[61:64], []byte{3, 5, 9}) {
				t.Fatalf("special clothes copied normal: %x", ack.Payload[61:64])
			}
			mask += 3
		}
		if !bytes.Equal(ack.Payload[mask:mask+4], []byte{1, 2, 3, 4}) {
			t.Fatalf("following outfit-mask field moved: %x", ack.Payload)
		}
	}
}
