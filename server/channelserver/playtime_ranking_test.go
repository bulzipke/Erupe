package channelserver

import (
	"encoding/binary"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/server/channelserver/compression/nullcomp"
)

func TestExtractPlaytimeFromSavedata(t *testing.T) {
	offset := getPointers(cfg.ZZ)[pPlaytime]
	raw := make([]byte, offset+saveFieldPlaytime)
	binary.LittleEndian.PutUint32(raw[offset:], 987654)

	compressed, err := nullcomp.Compress(raw)
	if err != nil {
		t.Fatalf("compress savedata: %v", err)
	}
	for name, savedata := range map[string][]byte{
		"uncompressed": raw,
		"compressed":   compressed,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := extractPlaytimeFromSavedata(cfg.ZZ, savedata)
			if err != nil {
				t.Fatalf("extract playtime: %v", err)
			}
			if got != 987654 {
				t.Fatalf("playtime = %d, want 987654", got)
			}
		})
	}
}

func TestExtractPlaytimeRejectsShortSavedata(t *testing.T) {
	if _, err := extractPlaytimeFromSavedata(cfg.ZZ, []byte{1, 2, 3}); err == nil {
		t.Fatal("expected short savedata error")
	}
}
