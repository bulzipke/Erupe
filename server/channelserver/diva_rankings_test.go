package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"erupe-ce/network/mhfpacket"
)

func TestDivaRankingNativeKinds(t *testing.T) {
	rows := []DivaRank{{Rank: 1, Name: "Guild", Points: 321}}
	for _, kind := range []uint8{0, 2} {
		p := divaRankingPayload(kind, rows)
		if len(p) != 3100 || p[0] != 0 || p[1] != 1 || string(p[2:7]) != "Guild" || binary.BigEndian.Uint32(p[27:31]) != 321 {
			t.Fatalf("kind %d: bad native row %x", kind, p[:31])
		}
		if !bytes.Equal(p[31:], make([]byte, 3100-31)) {
			t.Fatal("unused native cache rows not cleared")
		}
	}
	for _, kind := range []uint8{1, 3} {
		if p := divaRankingPayload(kind, rows); len(p) != 4700 || !bytes.Equal(p, make([]byte, 4700)) {
			t.Fatalf("unknown extended kind %d fabricated rows", kind)
		}
	}
	// The repository includes an unranked own guild for the separate 49-byte
	// personal summary. It must never become a "rank 0" leaderboard entry.
	if p := divaRankingPayload(2, []DivaRank{{IsOwn: true, Name: "Unranked"}}); !bytes.Equal(p, make([]byte, 3100)) {
		t.Fatal("unranked own guild leaked into leaderboard")
	}
	rows = make([]DivaRank, 101)
	for i := range rows {
		rows[i] = DivaRank{Rank: uint32(i + 1), Name: "Rank", Points: 1000 - int64(i)}
	}
	if p := divaRankingPayload(2, rows); len(p) != 3100 || p[99*31+1] != 100 {
		t.Fatal("top100 bound changed")
	}
}

func TestDivaOwnGuildRankingWire(t *testing.T) {
	personal := []DivaRank{{CharID: 12, Rank: 101, Points: 52}}
	guilds := []DivaRank{{GuildID: 1, Rank: 1, Name: "Other", Points: 800}, {GuildID: 2, IsOwn: true, Rank: 102, Name: "Own", Points: 42}}
	p := divaMyRankingWithGuildPayload(personal, guilds, 12)
	if len(p) != 49 || binary.BigEndian.Uint32(p[:4]) != 101 || binary.BigEndian.Uint32(p[8:12]) != 52 ||
		binary.BigEndian.Uint32(p[12:16]) != 102 || binary.BigEndian.Uint32(p[20:24]) != 42 || string(p[24:27]) != "Own" {
		t.Fatalf("own rank/guild fields: %x", p)
	}
	if binary.BigEndian.Uint32(p[4:8]) != 0 || binary.BigEndian.Uint32(p[16:20]) != 0 {
		t.Fatal("unconfirmed native fields must not be fabricated")
	}
}

func TestDivaRankingPublicationEnd(t *testing.T) {
	start := divaTestTime(20, 16, 0)
	end := start.Add(time.Duration(divaPhaseDuration) * time.Second)
	for _, tt := range []struct{ now, want time.Time }{
		{start.Add(time.Hour), start},
		{divaTestTime(20, 18, 0), divaTestTime(20, 18, 0)},
		{end, divaRankingCutoff(end.Add(-time.Nanosecond), start)},
		{end.Add(time.Duration(divaInterlude)*time.Second - time.Nanosecond), divaRankingCutoff(end.Add(-time.Nanosecond), start)},
		{end.Add(time.Duration(divaInterlude) * time.Second), end},
		{end.Add(30 * 24 * time.Hour), end},
	} {
		if got := divaSongRankingCutoff(tt.now, start); !got.Equal(tt.want) {
			t.Fatalf("at %v got %v want %v", tt.now, got, tt.want)
		}
	}
}

func TestDivaEmptyTacticsRankingClearsNativeCache(t *testing.T) {
	s := createMockSession(12, createMockServer())
	handleMsgMhfGetUdTacticsRanking(s, &mhfpacket.MsgMhfGetUdTacticsRanking{AckHandle: 9, GuildID: 999})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || len(ack.Payload) != 4041 || ack.Payload[40] != 100 {
		t.Fatalf("bad native empty ranking: code=%d len=%d", ack.ErrorCode, len(ack.Payload))
	}
	for i, b := range ack.Payload {
		if i != 40 && b != 0 {
			t.Fatalf("fabricated/stale value at byte %d", i)
		}
	}
}

type mockDivaGuildRanks struct {
	*mockDivaRepo
	guildRows                   []DivaRank
	guildErr                    error
	personalEvent, guildEvent   uint32
	personalCutoff, guildCutoff time.Time
}

func (m *mockDivaGuildRanks) GetDivaRanking(eventID, _ uint32, cutoff time.Time) ([]DivaRank, error) {
	m.personalEvent, m.personalCutoff = eventID, cutoff
	return m.ranks, m.getErr
}

func (m *mockDivaGuildRanks) GetDivaGuildRanking(eventID, _ uint32, cutoff time.Time) ([]DivaRank, error) {
	m.guildEvent, m.guildCutoff = eventID, cutoff
	return m.guildRows, m.guildErr
}

func TestDivaRankingHandlersUseRealGuildSnapshot(t *testing.T) {
	srv := createMockServer()
	repo := &mockDivaGuildRanks{mockDivaRepo: &mockDivaRepo{
		events: []DivaEvent{{ID: 8, StartTime: uint32(TimeAdjusted().Add(-24 * time.Hour).Unix())}},
		ranks:  []DivaRank{{CharID: 12, Rank: 1, Name: "Hunter", Points: 12}},
	}, guildRows: []DivaRank{{GuildID: 4, IsOwn: true, Rank: 2, Name: "Guild", Points: 120}}}
	srv.divaRepo = repo
	s := createMockSession(12, srv)
	handleMsgMhfGetUdRanking(s, &mhfpacket.MsgMhfGetUdRanking{AckHandle: 1, Unk0: 2})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || len(ack.Payload) != 3100 || string(ack.Payload[2:7]) != "Guild" {
		t.Fatal("kind2 did not use real guild ranking")
	}
	handleMsgMhfGetUdMyRanking(s, &mhfpacket.MsgMhfGetUdMyRanking{AckHandle: 2})
	ack = readAck(t, s)
	if ack.ErrorCode != 0 || binary.BigEndian.Uint32(ack.Payload[12:16]) != 2 || repo.personalEvent != repo.guildEvent || !repo.personalCutoff.Equal(repo.guildCutoff) {
		t.Fatal("personal/guild own ranking did not share the same round/cutoff")
	}
	repo.guildErr = errors.New("offline")
	handleMsgMhfGetUdRanking(s, &mhfpacket.MsgMhfGetUdRanking{AckHandle: 3, Unk0: 2})
	if ack := readAck(t, s); ack.ErrorCode == 0 {
		t.Fatal("DB error reported as a successful empty ranking")
	}
	handleMsgMhfGetUdMyRanking(s, &mhfpacket.MsgMhfGetUdMyRanking{AckHandle: 4})
	if ack := readAck(t, s); ack.ErrorCode == 0 {
		t.Fatal("guild error hidden in own ranking")
	}
	handleMsgMhfGetUdRanking(s, &mhfpacket.MsgMhfGetUdRanking{AckHandle: 5, Unk0: 4})
	if ack := readAck(t, s); ack.ErrorCode == 0 {
		t.Fatal("unknown kind accepted")
	}
}
