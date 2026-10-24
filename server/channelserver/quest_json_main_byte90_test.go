package channelserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"erupe-ce/common/decryption"
)

func TestQuestJSONMainByte90LegacyDefaults(t *testing.T) {
	legacy, err := CompileQuestJSON([]byte(minimalQuestJSON), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"0", "null"} {
		input := strings.Replace(minimalQuestJSON, `"quest_id": 1,`, `"quest_id": 1,"main_byte_90_raw":`+value+`,`, 1)
		got, err := CompileQuestJSON([]byte(input), "")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, legacy) {
			t.Fatal("explicit zero/null changed the legacy binary")
		}
	}
	encoded, err := json.Marshal(QuestJSON{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("main_byte_90_raw")) {
		t.Fatal("zero raw field must remain omitted in JSON")
	}
}

func TestQuestJSONMainByte90WidthsAndAdjacentBytes(t *testing.T) {
	legacy, err := CompileQuestJSON([]byte(minimalQuestJSON), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []uint8{0, 1, 2, 3, 4, 127, 128, 255} {
		t.Run(strconv.Itoa(int(value)), func(t *testing.T) {
			var q QuestJSON
			if err := json.Unmarshal([]byte(minimalQuestJSON), &q); err != nil {
				t.Fatal(err)
			}
			q.MainByte90Raw = value
			input, err := json.Marshal(q)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := CompileQuestJSON(input, "")
			if err != nil {
				t.Fatal(err)
			}
			mp := int(binary.LittleEndian.Uint32(compiled[:4]))
			if compiled[mp+0x90] != value {
				t.Fatal("raw byte width/value not preserved")
			}
			control := append([]byte(nil), compiled...)
			control[mp+0x90] = 0
			if !bytes.Equal(control, legacy) {
				t.Fatal("main90 changed bytes outside its single-byte field")
			}
			parsed, err := ParseQuestBinary(compiled)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.MainByte90Raw != value {
				t.Fatal("parser lost raw byte")
			}
			encoded, err := json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}
			round, err := CompileQuestJSON(encoded, "")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(round, compiled) {
				t.Fatal("generated fixture lost raw byte or adjacent data after roundtrip")
			}
		})
	}
}

func TestQuestJSONMainByte90RejectsOutOfRange(t *testing.T) {
	for _, value := range []string{"-1", "256", "1.5", `"4"`, "true"} {
		t.Run(value, func(t *testing.T) {
			if _, err := CompileQuestJSON([]byte(`{"main_byte_90_raw":`+value+`}`), ""); err == nil {
				t.Fatal("invalid raw byte accepted")
			}
		})
	}
}

func TestQuestJSONMainByte90CapturedRewardProjection(t *testing.T) {
	dir := os.Getenv("ERUPE_QUEST_MAIN_BYTE90_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set ERUPE_QUEST_MAIN_BYTE90_CAPTURE_DIR to detached saved NAS quest captures")
	}
	for _, tc := range []struct {
		file, hash string
		value      uint8
	}{
		{"40060d0.bin", "014815a5fe6b77dcda473d48808e363273eba7bf0cdad92e15c4963c1d649219", 0},
		{"40133d0.bin", "4e480537ffdd729266a992572cc96abc3aa4641b01e9fa9def83673e5593cc88", 0},
		{"40134d0.bin", "4ad642c1eff9475e901e34af796b5d549981c4b99c38e48a0e41e497a684d529", 0},
		{"40135d0.bin", "35afc766da5835716cafc4a3b33f9ae211ff54f3f852c2cd37aa042fcdc19bec", 0},
		{"40136d0.bin", "d6a2ffdd4ba0b42825c64c45d429907af1cf486df898fc22d69f6fbc7fd50e8e", 0},
		{"40137d0.bin", "92ca1e7c66eed66532c8354da6764e805ff6cdd393c997ed756e2fc8b114ad92", 0},
		{"40138d0.bin", "830336eae14f769322b9d1ef2725afb2983a67cbb4ab1ef43ccc70ddd352fb81", 0},
		{"40162d0.bin", "28f198bd0521ba492d749a1dc048a905cdfb1199879aac099af3b5bb3c1babaf", 4},
	} {
		t.Run(tc.file, func(t *testing.T) {
			packed, err := os.ReadFile(filepath.Join(dir, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(packed)
			if hex.EncodeToString(hash[:]) != tc.hash {
				t.Fatal("input is not the verified detached NAS capture")
			}
			original := decryption.UnpackSimple(packed)
			mp := int(binary.LittleEndian.Uint32(original[:4]))
			if original[mp+0x90] != tc.value {
				t.Fatal("captured main90 differs from reviewed native evidence")
			}
			parsed, err := ParseQuestBinary(original, "ko")
			if err != nil {
				t.Fatal(err)
			}
			if parsed.MainByte90Raw != tc.value {
				t.Fatal("parse lost main90 raw byte")
			}
			// Project only parsed rewards and byte90 into valid minimal JSON.
			// Do not rebuild mixed-encoding source text or unmodeled conditions.
			var projected QuestJSON
			if err := json.Unmarshal([]byte(minimalQuestJSON), &projected); err != nil {
				t.Fatal(err)
			}
			projected.Rewards = parsed.Rewards
			projected.MainByte90Raw = parsed.MainByte90Raw
			input, err := json.Marshal(projected)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := CompileQuestJSON(input, "")
			if err != nil {
				t.Fatal(err)
			}
			cmp := int(binary.LittleEndian.Uint32(compiled[:4]))
			if compiled[cmp+0x90] != tc.value {
				t.Fatal("compile lost main90 raw byte")
			}
			round, err := ParseQuestBinary(compiled)
			if err != nil || round.MainByte90Raw != tc.value || !reflect.DeepEqual(round.Rewards, parsed.Rewards) {
				t.Fatalf("reward/byte90 projection changed: %v", err)
			}
			// Clearing just the field must reproduce the former zero behavior,
			// with the descriptor/item pointers and all other bytes unchanged.
			projected.MainByte90Raw = 0
			zeroInput, err := json.Marshal(projected)
			if err != nil {
				t.Fatal(err)
			}
			zero, err := CompileQuestJSON(zeroInput, "")
			if err != nil {
				t.Fatal(err)
			}
			control := append([]byte(nil), compiled...)
			control[cmp+0x90] = 0
			if !bytes.Equal(control, zero) {
				t.Fatal("byte90 projection changed more than one byte")
			}
		})
	}
}
