package channelserver

import (
	"bytes"
	"testing"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestEnumerateOrderUsesKoreanNames(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.tournamentRepo = &mockTournamentRepo{ranks: []TournamentRankEntry{{
		CharID: 42, Rank: 1, CharName: "한글", GuildName: "수렵단",
	}}}
	session := createMockSession(1, server)
	handleMsgMhfEnumerateOrder(session, &mhfpacket.MsgMhfEnumerateOrder{AckHandle: 1, EventID: 2})
	select {
	case packet := <-session.sendPackets:
		// ACK header followed by event, time, count, unknown, then one row.
		bf := byteframe.NewByteFrameFromBytes(packet.data[10:])
		bf.ReadUint32()
		bf.ReadUint32()
		if count := bf.ReadUint16(); count != 1 {
			t.Fatalf("entry count = %d, want 1", count)
		}
		bf.ReadUint16()
		if id := bf.ReadUint32(); id != 42 {
			t.Fatalf("character ID = %d, want 42", id)
		}
		bf.ReadUint32() // rank
		bf.ReadBytes(10) // grade, pad, HR, GR, pad
		charLength := bf.ReadUint8()
		guildLength := bf.ReadUint8()
		if charLength != 5 || guildLength != 7 {
			t.Fatalf("name lengths = %d, %d, want CP949 5, 7", charLength, guildLength)
		}
		if got := bf.ReadBytes(uint(charLength)); !bytes.Equal(got, []byte{0xc7, 0xd1, 0xb1, 0xdb, 0}) {
			t.Fatalf("character name bytes = %x", got)
		}
		if got := bf.ReadBytes(uint(guildLength)); !bytes.Equal(got, []byte{0xbc, 0xf6, 0xb7, 0xc6, 0xb4, 0xdc, 0}) {
			t.Fatalf("guild name bytes = %x", got)
		}
	default:
		t.Fatal("no response packet queued")
	}
}
