package channelserver

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestDivaBonusAllNativeKindsOnWire(t *testing.T) {
	event := DivaEvent{ID: 42, StartTime: uint32(divaTestTime(20, 0, 0).Unix())}
	for _, tt := range []struct {
		name string
		id   int64
		kind byte
	}{
		{"rank", 3, 1}, {"field", 69, 2}, {"time_of_day", 0, 3},
		{"monster_family", 9, 4}, {"monster", 1, 5}, {"quest", 40217, 6}, {"monster_class", 0, 7},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := divaBonusTestRule()
			r.TargetType, r.TargetID = tt.name, tt.id
			rows, err := divaBonusTargets(event, []cfg.DivaBonusTarget{r})
			if err != nil {
				t.Fatal(err)
			}
			data := divaBonusPayload(rows)
			if len(data) != 18 || data[3] != tt.kind || binary.BigEndian.Uint32(data[4:8]) != uint32(tt.id) {
				t.Fatalf("condition changed on wire: %x", data)
			}
		})
	}
}

func TestDivaBonusTargetData(t *testing.T) {
	for _, tt := range []struct {
		name, filename             string
		id                         int64
		empty, directory, wantFail bool
	}{
		{name: "missing", id: 1, wantFail: true},
		{name: "base binary", filename: "00001d0.bin", id: 1},
		{name: "seasonal night", filename: "00001n2.json", id: 1},
		{name: "event base", filename: "40217d0.json", id: 40217},
		{name: "event wrong variant", filename: "40217n2.json", id: 40217, wantFail: true},
		{name: "wrong id", filename: "00002d0.bin", id: 1, wantFail: true},
		{name: "wrong extension", filename: "00001d0.txt", id: 1, wantFail: true},
		{name: "empty file", filename: "00001d0.bin", id: 1, empty: true, wantFail: true},
		{name: "directory is not file", filename: "00001d0.bin", id: 1, directory: true, wantFail: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Literal directory names must not become glob expressions.
			base := filepath.Join(t.TempDir(), "bin[ZZ]")
			if err := os.MkdirAll(filepath.Join(base, "quests"), 0700); err != nil {
				t.Fatal(err)
			}
			if tt.filename != "" {
				path := filepath.Join(base, "quests", tt.filename)
				if tt.directory {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				} else {
					data := []byte{1} // Presence check, not a binary-format validator.
					if tt.empty {
						data = nil
					}
					if err := os.WriteFile(path, data, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			r := divaBonusTestRule()
			r.TargetType, r.TargetID = "quest", tt.id
			if err := validateDivaBonusData(base, []cfg.DivaBonusTarget{r, r}); (err != nil) != tt.wantFail {
				t.Fatalf("validation error = %v; wantFail %t", err, tt.wantFail)
			}
		})
	}
	for _, monster := range divaSongMonsterPoints {
		r := divaBonusTestRule()
		r.TargetID = int64(monster.MID)
		if err := validateDivaBonusData("", []cfg.DivaBonusTarget{r}); err != nil {
			t.Fatal(err)
		}
	}
	r := divaBonusTestRule()
	r.TargetID = 3 // Small monster absent from the song-point response.
	if err := validateDivaBonusData("", []cfg.DivaBonusTarget{r}); err == nil {
		t.Fatal("monster without a song-point entry accepted")
	}
}

func TestDivaBonusHandlerRejectsMissingData(t *testing.T) {
	for _, kind := range []string{"monster", "quest"} {
		t.Run(kind, func(t *testing.T) {
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = cfg.ZZ
			srv.erupeConfig.DebugOptions.DivaOverride = -1
			srv.erupeConfig.BinPath = t.TempDir()
			r := divaBonusTestRule()
			r.TargetType, r.TargetID = kind, 3
			srv.erupeConfig.GameplayOptions.DivaBonusTargets = []cfg.DivaBonusTarget{r}
			srv.divaRepo = &mockDivaRepo{events: []DivaEvent{{ID: 1, StartTime: uint32(TimeAdjusted().Add(-time.Hour).Unix())}}}
			s := createMockSession(1, srv)
			handleMsgMhfGetUdBonusQuestInfo(s, &mhfpacket.MsgMhfGetUdBonusQuestInfo{AckHandle: 7})
			if ack := readAck(t, s); ack.ErrorCode == 0 {
				t.Fatal("unavailable target acknowledged as valid")
			}
		})
	}
}

func TestDivaBonusEmptyBinaryShadowsJSON(t *testing.T) {
	base := t.TempDir()
	quests := filepath.Join(base, "quests")
	if err := os.Mkdir(quests, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(quests, "40217d0.bin"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(quests, "40217d0.json"), []byte{1}, 0600); err != nil {
		t.Fatal(err)
	}
	r := divaBonusTestRule()
	r.TargetType, r.TargetID = "quest", 40217
	if err := validateDivaBonusData(base, []cfg.DivaBonusTarget{r}); err == nil {
		t.Fatal("JSON cannot rescue a readable empty BIN preferred by the quest loader")
	}
}

func TestDivaBonusSymlinkQuest(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "quests"), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, "shared.bin")
	if err := os.WriteFile(target, []byte{1}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(base, "quests", "40217d0.bin")); err != nil {
		t.Skipf("symlink creation not permitted: %v", err)
	}
	r := divaBonusTestRule()
	r.TargetType, r.TargetID = "quest", 40217
	if err := validateDivaBonusData(base, []cfg.DivaBonusTarget{r}); err != nil {
		t.Fatal(err)
	}
}
