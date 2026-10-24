package channelserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"erupe-ce/common/decryption"
)

func assertQuestRawTypeFields(t *testing.T, data []byte, general uint32, main uint16, clears uint32) {
	t.Helper()
	mp := binary.LittleEndian.Uint32(data[:4])
	if got := binary.LittleEndian.Uint32(data[0x5c:]); got != general {
		t.Fatalf("general type scalar = %#x, want %#x", got, general)
	}
	if got := binary.LittleEndian.Uint16(data[mp+0x0a:]); got != main {
		t.Fatalf("main type scalar = %#x, want %#x", got, main)
	}
	if got := binary.LittleEndian.Uint32(data[mp+0xc4:]); got != clears {
		t.Fatalf("completion-limit scalar = %#x, want %#x", got, clears)
	}
}

func TestQuestJSONRawTypeFieldsWidthsAndLegacyDefault(t *testing.T) {
	legacy, err := CompileQuestJSON([]byte(minimalQuestJSON), "")
	if err != nil {
		t.Fatal(err)
	}
	assertQuestRawTypeFields(t, legacy, 0, 0, 0)
	for _, tc := range []struct {
		name    string
		general uint32
		main    uint16
		clears  uint32
	}{
		{"practice", 2, 2, 0},
		{"G_support", 7, 0x0107, 7},
		{"single_clear", 9, 9, 1},
		{"ten_clears", 1, 1, 10},
		{"independent_full_width_values", 0x89abcdef, 0xfedc, 0x76543210},
		{"maximum_values", ^uint32(0), ^uint16(0), ^uint32(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var q QuestJSON
			if err := json.Unmarshal([]byte(minimalQuestJSON), &q); err != nil {
				t.Fatal(err)
			}
			q.GeneralQuestTypeIDRaw, q.MainQuestTypeIDRaw = tc.general, tc.main
			q.QuestClearsAllowedRaw = tc.clears
			input, err := json.Marshal(q)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := CompileQuestJSON(input, "")
			if err != nil {
				t.Fatal(err)
			}
			assertQuestRawTypeFields(t, compiled, tc.general, tc.main, tc.clears)
			// On this generated fixture the new fields may alter exactly ten
			// bytes; neighboring scalars, strings and condition bytes stay put.
			control := append([]byte(nil), compiled...)
			mp := binary.LittleEndian.Uint32(control[:4])
			copy(control[0x5c:0x60], legacy[0x5c:0x60])
			copy(control[mp+0x0a:mp+0x0c], legacy[mp+0x0a:mp+0x0c])
			copy(control[mp+0xc4:mp+0xc8], legacy[mp+0xc4:mp+0xc8])
			if !bytes.Equal(control, legacy) {
				t.Fatal("raw scalar fields changed bytes beyond their own widths")
			}
			parsed, err := ParseQuestBinary(compiled)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.GeneralQuestTypeIDRaw != tc.general || parsed.MainQuestTypeIDRaw != tc.main || parsed.QuestClearsAllowedRaw != tc.clears {
				t.Fatalf("parse lost raw fields: general=%#x main=%#x clears=%#x", parsed.GeneralQuestTypeIDRaw, parsed.MainQuestTypeIDRaw, parsed.QuestClearsAllowedRaw)
			}
			encoded, err := json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip, err := CompileQuestJSON(encoded, "")
			if err != nil {
				t.Fatal(err)
			}
			assertQuestRawTypeFields(t, roundTrip, tc.general, tc.main, tc.clears)
		})
	}
}

func TestQuestJSONRawTypeFieldsRejectOutOfRange(t *testing.T) {
	for _, field := range []struct {
		name string
		bad  []string
	}{
		{"general_quest_type_id_raw", []string{"-1", "4294967296", "1.5"}},
		{"main_quest_type_id_raw", []string{"-1", "65536", "1.5"}},
		{"quest_clears_allowed_raw", []string{"-1", "4294967296", "1.5"}},
	} {
		for _, value := range field.bad {
			t.Run(field.name+"/"+value, func(t *testing.T) {
				input := []byte(`{"quest_id":1,"` + field.name + `":` + value + `}`)
				if _, err := CompileQuestJSON(input, ""); err == nil {
					t.Fatal("out-of-range raw scalar was accepted")
				}
			})
		}
	}
}

func TestQuestJSONRawTypeFieldsCapturedNAS(t *testing.T) {
	dir := os.Getenv("ERUPE_QUEST_TYPE_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set ERUPE_QUEST_TYPE_CAPTURE_DIR to the detached NAS quest capture directory")
	}
	for _, tc := range []struct {
		file    string
		hash    string
		general uint32
		main    uint16
		clears  uint32
	}{
		{"40202d0.bin", "0850bb44584b718a1b3e09e4ce4f95159dae0b8405789dec5d61645a256f8f5d", 2, 2, 0},
		{"56014d0.bin", "41de6ae9e491434c7afb865b6199bc26779d2fffc4ab8a667f60efc610930cb0", 7, 0x0107, 7},
		{"55913d0.bin", "2508537cd43d6e73727544f5e20e0ae1c4164d6322cccaf82fd42e093e4fca2f", 9, 9, 1},
		{"53291d0.bin", "8138aa9912c35a7786330ff3ce8ea379cc49c57ef2cc0a1121d79e978e9f437b", 1, 1, 10},
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
			assertQuestRawTypeFields(t, original, tc.general, tc.main, tc.clears)
			q, err := ParseQuestBinary(original, "ko")
			if err != nil {
				t.Fatal(err)
			}
			if q.GeneralQuestTypeIDRaw != tc.general || q.MainQuestTypeIDRaw != tc.main || q.QuestClearsAllowedRaw != tc.clears {
				t.Fatal("NAS parse lost a raw control scalar")
			}
			// These captures also contain mixed-encoding message text that the
			// existing parser/compiler cannot round-trip as one locale. Project
			// only the three parsed control scalars into a valid JSON fixture;
			// this test does not edit source strings or claim a full NAS rebuild.
			var projected QuestJSON
			if err := json.Unmarshal([]byte(minimalQuestJSON), &projected); err != nil {
				t.Fatal(err)
			}
			projected.GeneralQuestTypeIDRaw = q.GeneralQuestTypeIDRaw
			projected.MainQuestTypeIDRaw = q.MainQuestTypeIDRaw
			projected.QuestClearsAllowedRaw = q.QuestClearsAllowedRaw
			input, err := json.Marshal(projected)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := CompileQuestJSON(input, "ko")
			if err != nil {
				t.Fatal(err)
			}
			assertQuestRawTypeFields(t, compiled, tc.general, tc.main, tc.clears)
			// Deliberately make no whole-binary or other unknown-field claim.
			// Removing these three optional fields recreates the old zero output.
			projected.GeneralQuestTypeIDRaw, projected.MainQuestTypeIDRaw = 0, 0
			projected.QuestClearsAllowedRaw = 0
			withoutFields, err := json.Marshal(projected)
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := CompileQuestJSON(withoutFields, "ko")
			if err != nil {
				t.Fatal(err)
			}
			assertQuestRawTypeFields(t, legacy, 0, 0, 0)
		})
	}
}
