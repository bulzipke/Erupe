package mhfpallone

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestNativeResponseRows(t *testing.T) {
	// Independent expected bytes from the client row readers, including the
	// guild score/ID order confirmed at 103860d6 and 10390440.
	tests := []struct {
		name, wire string
		encode     func() ([]byte, error)
	}{
		{"score", "123456780000000000000000000000010000000900000bb8000007d0000001f4", func() ([]byte, error) { return EncodeScores(0x12345678, []Score{{9, 3000, 2000, 500}}) }},
		{"rank", "123456780000000000000000000000010000000900000002000007d0", func() ([]byte, error) { return EncodeRanks(0x12345678, []RankScore{{9, 2, 2000}}) }},
		{"personal", "12345678000000000000000000000001000007d048756e7465720000000000000000", func() ([]byte, error) {
			return EncodePersonalRanking(0x12345678, []PersonalRanking{{2000, "Hunter"}}, 100)
		}},
		{"guild", "12345678000000000000000000000001000007d0000003844775696c64000000000000000000000000000000000000000000", func() ([]byte, error) { return EncodeGuildRanking(0x12345678, []GuildRanking{{2000, 900, "Guild"}}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, err := hex.DecodeString(tt.wire)
			if err != nil {
				t.Fatal(err)
			}
			got, err := tt.encode()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("wire = %x (%d bytes), want %x (%d bytes)", got, len(got), want, len(want))
			}
		})
	}
}

func TestNativeResponseCapacity(t *testing.T) {
	checks := []func() error{
		func() error { _, e := EncodeScores(1, make([]Score, 201)); return e },
		func() error { _, e := EncodeRanks(1, make([]RankScore, 201)); return e },
		func() error { _, e := EncodePersonalRanking(1, make([]PersonalRanking, 101), 100); return e },
		func() error { _, e := EncodePersonalRanking(1, make([]PersonalRanking, 6), 5); return e },
		func() error { _, e := EncodePersonalRanking(1, nil, 101); return e },
		func() error { _, e := EncodeGuildRanking(1, make([]GuildRanking, 101)); return e },
	}
	for i, check := range checks {
		if check() == nil {
			t.Fatalf("accepted overflowing response %d", i)
		}
	}
}

func TestRankingNamesHaveFixedTerminatedSlots(t *testing.T) {
	personal, err := EncodePersonalRanking(1, []PersonalRanking{{1, "ABCDEFGHIJKLMNO"}, {2, "B"}}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(personal) != 16+2*18 || !bytes.Equal(personal[20:34], []byte("ABCDEFGHIJKLM\x00")) || personal[34] != 0 || personal[37] != 2 || personal[38] != 'B' {
		t.Fatalf("personal name escaped its 14-byte slot: %x", personal)
	}
	guild, err := EncodeGuildRanking(1, []GuildRanking{{1, 2, "ABCDEFGHIJKLMNOPQRSTUVWXYZ"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(guild) != 50 || !bytes.Equal(guild[24:], []byte("ABCDEFGHIJKLMNOPQRSTUVWXY\x00")) {
		t.Fatalf("guild name escaped its 26-byte slot: %x", guild)
	}
}
