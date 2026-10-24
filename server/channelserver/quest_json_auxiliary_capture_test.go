package channelserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"erupe-ce/common/decryption"
)

func auxCapturedU32(t *testing.T, data []byte, offset int) uint32 {
	t.Helper()
	if offset < 0 || offset > len(data)-4 {
		t.Fatalf("u32 outside detached capture: offset=%#x bytes=%d", offset, len(data))
	}
	return binary.LittleEndian.Uint32(data[offset:])
}

func auxCapturedU16(t *testing.T, data []byte, offset int) uint16 {
	t.Helper()
	if offset < 0 || offset > len(data)-2 {
		t.Fatalf("u16 outside detached capture: offset=%#x bytes=%d", offset, len(data))
	}
	return binary.LittleEndian.Uint16(data[offset:])
}

type auxCapturedControlValues struct {
	general uint32
	main    uint16
	clears  uint32
	main90  byte
}

func auxCapturedControls(t *testing.T, data []byte) auxCapturedControlValues {
	t.Helper()
	mp := uint64(auxCapturedU32(t, data, 0))
	if mp == 0 || mp+questBodyLenZZ > uint64(len(data)) {
		t.Fatal("detached capture has invalid main-properties range")
	}
	return auxCapturedControlValues{
		general: auxCapturedU32(t, data, 0x5c),
		main:    auxCapturedU16(t, data, int(mp)+0x0a),
		clears:  auxCapturedU32(t, data, int(mp)+0xc4),
		main90:  data[int(mp)+0x90],
	}
}

type auxCapturedString struct {
	present bool
	withNUL []byte
}

// Read independently from the production exporter; compare content/presence,
// not rebuilt absolute pointers or an unmodeled whole-binary layout.
func auxCapturedStrings(t *testing.T, data []byte, table uint32, count int) []auxCapturedString {
	t.Helper()
	result := make([]auxCapturedString, count)
	if table == 0 {
		return result
	}
	if uint64(table)+uint64(count)*4 > uint64(len(data)) {
		t.Fatal("detached capture has invalid string-pointer table")
	}
	for i := range result {
		pointer := auxCapturedU32(t, data, int(table)+i*4)
		if pointer == 0 {
			continue
		}
		if uint64(pointer) >= uint64(len(data)) {
			t.Fatalf("string %d points outside detached capture", i)
		}
		length := bytes.IndexByte(data[int(pointer):], 0)
		if length < 0 {
			t.Fatalf("string %d has no in-file NUL", i)
		}
		result[i] = auxCapturedString{true, data[int(pointer) : int(pointer)+length+1]}
	}
	return result
}

func auxCapturedCompareStrings(t *testing.T, source, compiled []byte, auxiliary bool) {
	t.Helper()
	count := 8
	names := []string{"title", "text_main", "text_sub_a", "text_sub_b", "success_cond", "fail_cond", "contractor", "description"}
	var sourceTable, compiledTable uint32
	if auxiliary {
		count = 2
		names = []string{"some_string", "quest_type_string"}
		sourceTable = auxCapturedU32(t, source, 0x30)
		compiledTable = auxCapturedU32(t, compiled, 0x30)
	} else {
		sourceTable = auxCapturedU32(t, source, int(auxCapturedU32(t, source, 0))+0x28)
		compiledTable = auxCapturedU32(t, compiled, int(auxCapturedU32(t, compiled, 0))+0x28)
		if sourceTable == 0 || compiledTable == 0 {
			t.Fatal("pinned capture must have its original eight-text table")
		}
	}
	before := auxCapturedStrings(t, source, sourceTable, count)
	after := auxCapturedStrings(t, compiled, compiledTable, count)
	for i := range before {
		if before[i].present != after[i].present || !bytes.Equal(before[i].withNUL, after[i].withNUL) {
			t.Fatalf("%s original presence/NUL-terminated bytes changed: source=%x compiled=%x", names[i], before[i].withNUL, after[i].withNUL)
		}
	}
}

func auxCapturedRewardDescriptorBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	pointer := auxCapturedU32(t, data, 0x0c)
	if pointer == 0 {
		return nil
	}
	var result []byte
	for offset := uint64(pointer); ; offset += 8 {
		if offset+2 > uint64(len(data)) {
			t.Fatal("reward descriptor list has no bounded terminator")
		}
		if auxCapturedU16(t, data, int(offset)) == 0xffff {
			return append(result, 0xff, 0xff)
		}
		if offset+8 > uint64(len(data)) {
			t.Fatal("truncated reward descriptor")
		}
		// Only the four descriptor bytes are modeled here; its relocated
		// item pointer is checked through parsed reward values instead.
		result = append(result, data[int(offset):int(offset)+4]...)
	}
}

func auxCapturedModelDifferences(before, after *QuestJSON) []string {
	left, right := reflect.ValueOf(*before), reflect.ValueOf(*after)
	var changed []string
	for i := 0; i < left.NumField(); i++ {
		if !reflect.DeepEqual(left.Field(i).Interface(), right.Field(i).Interface()) {
			changed = append(changed, left.Type().Field(i).Name)
		}
	}
	return changed
}

func auxCapturedRawBlock(t *testing.T, data []byte) any {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	raw, exists := fields["auxiliary_text_raw"]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		t.Fatal("explicit exporter omitted the auxiliary-byte contract")
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestQuestJSONAuxiliaryCapturedPreservation(t *testing.T) {
	dir := os.Getenv("ERUPE_QUEST_AUXILIARY_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set ERUPE_QUEST_AUXILIARY_CAPTURE_DIR to the detached NAS quest captures")
	}
	for _, tc := range []struct {
		file    string
		hash    string
		questID uint16
		control auxCapturedControlValues
	}{
		{"40202d0.bin", "0850bb44584b718a1b3e09e4ce4f95159dae0b8405789dec5d61645a256f8f5d", 40202, auxCapturedControlValues{2, 2, 0, 2}},
		{"56014d0.bin", "41de6ae9e491434c7afb865b6199bc26779d2fffc4ab8a667f60efc610930cb0", 56014, auxCapturedControlValues{7, 0x0107, 7, 2}},
		{"55913d0.bin", "2508537cd43d6e73727544f5e20e0ae1c4164d6322cccaf82fd42e093e4fca2f", 55913, auxCapturedControlValues{9, 9, 1, 2}},
		{"53291d0.bin", "8138aa9912c35a7786330ff3ce8ea379cc49c57ef2cc0a1121d79e978e9f437b", 53291, auxCapturedControlValues{1, 1, 10, 4}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			packed, err := os.ReadFile(filepath.Join(dir, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			observedHash := sha256.Sum256(packed)
			if hex.EncodeToString(observedHash[:]) != tc.hash {
				t.Fatal("input is not the pinned detached NAS capture")
			}
			original := decryption.UnpackSimple(packed)
			untouched := bytes.Clone(original)
			if got := auxCapturedControls(t, original); got != tc.control {
				t.Fatalf("original raw controls changed: got=%+v want=%+v", got, tc.control)
			}
			before, err := ParseQuestBinary(original, "ko")
			if err != nil {
				t.Fatal(err)
			}
			if before.QuestID != tc.questID || len(before.AuxiliaryTextRaw) != 0 {
				t.Fatal("default parser identification or raw-absent contract changed")
			}
			legacyJSON, err := json.Marshal(before)
			if err != nil {
				t.Fatal(err)
			}
			if output, err := CompileQuestJSON(legacyJSON, "ko"); err == nil || output != nil || !strings.Contains(err.Error(), "EUC-KR encode") {
				t.Fatalf("legacy full export must retain its original encoding failure: output=%d error=%v", len(output), err)
			}
			exported, err := ExportQuestJSONPreservingAuxiliaryText(original, "ko")
			if err != nil {
				t.Fatal(err)
			}
			rawBlock := auxCapturedRawBlock(t, exported)
			compiled, err := CompileQuestJSON(exported, "ko")
			if err != nil {
				t.Fatal(err)
			}
			after, err := ParseQuestBinary(compiled, "ko")
			if err != nil {
				t.Fatal(err)
			}
			if changed := auxCapturedModelDifferences(before, after); len(changed) != 0 {
				t.Fatalf("modeled quest fields changed (no normalization/masking): %s", strings.Join(changed, ", "))
			}
			if got := auxCapturedControls(t, compiled); got != tc.control {
				t.Fatalf("raw type/limit/main90 changed: got=%+v want=%+v", got, tc.control)
			}
			auxCapturedCompareStrings(t, original, compiled, false)
			auxCapturedCompareStrings(t, original, compiled, true)
			if !bytes.Equal(auxCapturedRewardDescriptorBytes(t, original), auxCapturedRewardDescriptorBytes(t, compiled)) {
				t.Fatal("original reward descriptor bytes changed")
			}
			secondExport, err := ExportQuestJSONPreservingAuxiliaryText(compiled, "ko")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rawBlock, auxCapturedRawBlock(t, secondExport)) {
				t.Fatal("auxiliary byte contract changed on a second explicit export")
			}
			if !bytes.Equal(original, untouched) {
				t.Fatal("export/compile mutated source capture bytes")
			}
			t.Log(fmt.Sprintf("pinned quest %d: modeled fields, QuestText8, auxiliary2, descriptors and raw controls preserved; source=%d compiled=%d; whole-BIN equality not asserted", tc.questID, len(original), len(compiled)))
		})
	}
}
