package channelserver

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func auxiliaryTestJSON(t *testing.T, some, kind string) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(minimalQuestJSON), &fields); err != nil {
		t.Fatal(err)
	}
	fields["some_string"], _ = json.Marshal(some)
	fields["quest_type_string"], _ = json.Marshal(kind)
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func auxiliaryTestPointer(t *testing.T, data []byte, index int) int {
	t.Helper()
	table := int(binary.LittleEndian.Uint32(data[0x30:]))
	if table == 0 {
		return 0
	}
	if table < 0 || table+8 > len(data) {
		t.Fatal("auxiliary table outside test binary")
	}
	pointer := int(binary.LittleEndian.Uint32(data[table+4*index:]))
	if pointer < 0 || pointer >= len(data) {
		t.Fatal("auxiliary pointer outside test binary")
	}
	return pointer
}

func auxiliaryTestRaw(t *testing.T, data []byte, index int) []byte {
	t.Helper()
	pointer := auxiliaryTestPointer(t, data, index)
	if pointer == 0 {
		return nil
	}
	end := bytes.IndexByte(data[pointer:], 0)
	if end < 0 {
		t.Fatal("test string lacks terminator")
	}
	return data[pointer : pointer+end]
}

func TestQuestAuxiliaryExportPreservesUnencodableBytesAndModeledFields(t *testing.T) {
	original, err := hex.DecodeString("88cb978a82cc956982f0915382c4945b956982b582dc82b582bd")
	if err != nil {
		t.Fatal(err)
	}
	var q QuestJSON
	if err := json.Unmarshal(auxiliaryTestJSON(t, strings.Repeat("x", len(original)), "metadata"), &q); err != nil {
		t.Fatal(err)
	}
	q.QuestClearsAllowedRaw = 1
	q.GeneralQuestTypeIDRaw = 513
	q.MainQuestTypeIDRaw = 9
	q.MainByte90Raw = 4
	q.Rewards[0].DescriptorByte1Raw = 0x80
	q.Rewards[0].DescriptorWord2Raw = 0x1234
	input, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	source, err := CompileQuestJSON(input, "ko")
	if err != nil {
		t.Fatal(err)
	}
	copy(source[auxiliaryTestPointer(t, source, 0):], original)
	before := append([]byte(nil), source...)
	legacy, err := ParseQuestBinary(source, "ko")
	if err != nil {
		t.Fatal(err)
	}
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(legacyJSON, []byte("auxiliary_text_raw")) {
		t.Fatal("the legacy parser opted into byte preservation")
	}
	if !strings.ContainsRune(legacy.SomeString, '\ufffd') {
		t.Fatal("fixture does not reproduce the lossy text projection")
	}
	if got, err := CompileQuestJSON(legacyJSON, "ko"); err == nil || got != nil {
		t.Fatal("the default encoder no longer reports the unencodable projection")
	}
	exported, err := ExportQuestJSONPreservingAuxiliaryText(source, "ko")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileQuestJSON(exported, "ko")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(auxiliaryTestRaw(t, compiled, 0), original) || !bytes.Equal(auxiliaryTestRaw(t, compiled, 1), auxiliaryTestRaw(t, source, 1)) {
		t.Fatal("the original auxiliary bytes changed")
	}
	parsed, err := ParseQuestBinary(compiled, "ko")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed, legacy) {
		t.Fatal("modeled quest data changed while preserving auxiliary bytes")
	}
	second, err := ExportQuestJSONPreservingAuxiliaryText(compiled, "ko")
	if err != nil || !bytes.Equal(second, exported) {
		t.Fatalf("preserving export is unstable: %v", err)
	}
	if !bytes.Equal(before, source) {
		t.Fatal("offline export modified its input")
	}
}

func TestQuestAuxiliaryExportDistinguishesAbsentAndEmptyPointers(t *testing.T) {
	for _, index := range []int{0, 1} {
		t.Run([]string{"some_string", "quest_type_string"}[index], func(t *testing.T) {
			texts := [2]string{}
			texts[index] = "x"
			source, err := CompileQuestJSON(auxiliaryTestJSON(t, texts[0], texts[1]), "ko")
			if err != nil {
				t.Fatal(err)
			}
			source[auxiliaryTestPointer(t, source, index)] = 0
			exported, err := ExportQuestJSONPreservingAuxiliaryText(source, "ko")
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(exported, &fields); err != nil {
				t.Fatal(err)
			}
			name := []string{"some_string", "quest_type_string"}[index]
			if !bytes.Equal(fields[name], []byte(`""`)) {
				t.Fatal("explicit empty text was omitted")
			}
			compiled, err := CompileQuestJSON(exported, "ko")
			if err != nil {
				t.Fatal(err)
			}
			if auxiliaryTestPointer(t, compiled, index) == 0 || auxiliaryTestPointer(t, compiled, 1-index) != 0 || len(auxiliaryTestRaw(t, compiled, index)) != 0 {
				t.Fatal("empty and absent auxiliary pointers were conflated")
			}
			// The supported preserving API returns JSON bytes. A typed legacy
			// re-marshal intentionally keeps omitempty and loses empty presence.
			var typed QuestJSON
			if err := json.Unmarshal(exported, &typed); err != nil {
				t.Fatal(err)
			}
			remarshaled, err := json.Marshal(typed)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := CompileQuestJSON(remarshaled, "ko"); err == nil || got != nil {
				t.Fatal("a missing required empty-text projection was silently repaired")
			}
		})
	}
	source, err := CompileQuestJSON([]byte(minimalQuestJSON), "")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseQuestBinary(source)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := ExportQuestJSONPreservingAuxiliaryText(source, "")
	if err != nil || !bytes.Equal(legacy, exported) {
		t.Fatalf("no auxiliary pointers changed default JSON: %v", err)
	}
}

func TestQuestAuxiliaryContractRejectsAmbiguousOrInconsistentJSON(t *testing.T) {
	validRaw := `{"version":1,"projection_language":"ko","some_string":{"bytes_hex":"61"}}`
	base := auxiliaryTestJSON(t, "a", "")
	makeInput := func(t *testing.T, raw string, modify func(map[string]json.RawMessage)) []byte {
		t.Helper()
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(base, &fields); err != nil {
			t.Fatal(err)
		}
		fields["auxiliary_text_raw"] = json.RawMessage(raw)
		if modify != nil {
			modify(fields)
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	for name, raw := range map[string]string{
		"null block": `null`, "array block": `[]`, "scalar block": `1`, "empty block": `{}`,
		"null version":       `{"version":null,"projection_language":"ko","some_string":{"bytes_hex":"61"}}`,
		"future version":     `{"version":2,"projection_language":"ko","some_string":{"bytes_hex":"61"}}`,
		"float version":      `{"version":1.0,"projection_language":"ko","some_string":{"bytes_hex":"61"}}`,
		"missing language":   `{"version":1,"some_string":{"bytes_hex":"61"}}`,
		"null language":      `{"version":1,"projection_language":null,"some_string":{"bytes_hex":"61"}}`,
		"different language": strings.Replace(validRaw, `"ko"`, `"KO"`, 1),
		"unknown header":     strings.Replace(validRaw, `"version":1`, `"unknown":1,"version":1`, 1),
		"duplicate header":   strings.Replace(validRaw, `"version":1`, `"version":1,"version":1`, 1),
		"no raw field":       `{"version":1,"projection_language":"ko"}`,
		"null pair":          `{"version":1,"projection_language":"ko","some_string":null}`,
		"array pair":         `{"version":1,"projection_language":"ko","some_string":[]}`,
		"missing hex":        `{"version":1,"projection_language":"ko","some_string":{}}`,
		"null hex":           strings.Replace(validRaw, `"61"`, `null`, 1),
		"numeric hex":        strings.Replace(validRaw, `"61"`, `61`, 1),
		"odd hex":            strings.Replace(validRaw, `"61"`, `"6"`, 1),
		"non hex":            strings.Replace(validRaw, `"61"`, `"zz"`, 1),
		"embedded NUL":       strings.Replace(validRaw, `"61"`, `"610062"`, 1),
		"duplicate hex":      strings.Replace(validRaw, `"bytes_hex":"61"`, `"bytes_hex":"61","bytes_hex":"61"`, 1),
		"unknown pair":       strings.Replace(validRaw, `"bytes_hex":"61"`, `"bytes_hex":"61","unknown":0`, 1),
		"pair alias":         strings.Replace(validRaw, `"some_string"`, `"SOME_STRING"`, 1),
		"hex alias":          strings.Replace(validRaw, `"bytes_hex"`, `"BYTES_HEX"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := CompileQuestJSON(makeInput(t, raw, nil), "ko"); err == nil || got != nil {
				t.Fatal("invalid raw contract accepted or returned partial bytes")
			}
		})
	}
	for name, value := range map[string]string{"edited text": `"b"`, "blank text": `""`, "null text": `null`, "numeric text": `1`, "localized text": `{"ko":"a"}`} {
		t.Run(name, func(t *testing.T) {
			data := makeInput(t, validRaw, func(fields map[string]json.RawMessage) { fields["some_string"] = json.RawMessage(value) })
			if got, err := CompileQuestJSON(data, "ko"); err == nil || got != nil {
				t.Fatal("inconsistent legacy text accepted")
			}
		})
	}
	for _, name := range []string{"missing text", "raw alias", "text alias", "duplicate raw", "duplicate text"} {
		t.Run(name, func(t *testing.T) {
			data := makeInput(t, validRaw, func(fields map[string]json.RawMessage) {
				switch name {
				case "missing text":
					delete(fields, "some_string")
				case "raw alias":
					fields["AUXILIARY_TEXT_RAW"] = fields["auxiliary_text_raw"]
					delete(fields, "auxiliary_text_raw")
				case "text alias":
					fields["SOME_STRING"] = fields["some_string"]
					delete(fields, "some_string")
				}
			})
			if name == "duplicate raw" {
				data = append([]byte(`{"auxiliary_text_raw":`+validRaw+`,`), data[1:]...)
			} else if name == "duplicate text" {
				data = append([]byte(`{"some_string":"a",`), data[1:]...)
			}
			if got, err := CompileQuestJSON(data, "ko"); err == nil || got != nil {
				t.Fatal("ambiguous or absent reserved field accepted")
			}
		})
	}
}

func TestQuestAuxiliaryLanguageIsExactAndUnpreservedTextUsesLegacyEncoder(t *testing.T) {
	for _, language := range []string{"ko", "jp", "", "en"} {
		source, err := CompileQuestJSON(auxiliaryTestJSON(t, "a", "b"), language)
		if err != nil {
			t.Fatal(err)
		}
		exported, err := ExportQuestJSONPreservingAuxiliaryText(source, language)
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := CompileQuestJSON(exported, language)
		if err != nil || !bytes.Equal(compiled, source) {
			t.Fatalf("same explicit language failed: %q: %v", language, err)
		}
		if got, err := CompileQuestJSON(exported, language+"-different"); err == nil || got != nil {
			t.Fatal("cross-language raw bytes accepted")
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(exported, &fields); err != nil {
			t.Fatal(err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(fields["auxiliary_text_raw"], &raw); err != nil {
			t.Fatal(err)
		}
		delete(raw, "quest_type_string")
		fields["auxiliary_text_raw"], _ = json.Marshal(raw)
		fields["quest_type_string"] = json.RawMessage(`"\ufffd"`)
		unpreserved, _ := json.Marshal(fields)
		if got, err := CompileQuestJSON(unpreserved, language); err == nil || got != nil {
			t.Fatal("an unpreserved field silently bypassed the legacy encoder")
		}
	}
}

func TestQuestAuxiliaryExportRejectsOutOfRangeOrUnterminatedSource(t *testing.T) {
	base, err := CompileQuestJSON(auxiliaryTestJSON(t, "abc", ""), "ko")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"short header", "partial table", "outside pointer", "missing terminator"} {
		t.Run(name, func(t *testing.T) {
			data := append([]byte(nil), base...)
			table := int(binary.LittleEndian.Uint32(data[0x30:]))
			switch name {
			case "short header":
				data = data[:0x30]
			case "partial table":
				binary.LittleEndian.PutUint32(data[0x30:], uint32(len(data)-4))
			case "outside pointer":
				binary.LittleEndian.PutUint32(data[table:], math.MaxUint32)
			case "missing terminator":
				binary.LittleEndian.PutUint32(data[table:], uint32(len(data)))
				data = append(data, 'a')
			}
			before := append([]byte(nil), data...)
			if output, err := ExportQuestJSONPreservingAuxiliaryText(data, "ko"); err == nil || output != nil {
				t.Fatal("invalid auxiliary source accepted or returned partial JSON")
			}
			if !bytes.Equal(before, data) {
				t.Fatal("failed export modified input")
			}
		})
	}
}

func TestQuestAuxiliaryLayoutRejectsVirtualU32Overflow(t *testing.T) {
	for _, tc := range []struct {
		start, extra uint64
		valid        bool
	}{{0, 0, true}, {math.MaxUint32, 0, true}, {math.MaxUint32 - 1, 1, true}, {math.MaxUint32, 1, false}, {math.MaxUint32 + 1, 0, false}, {math.MaxUint64, math.MaxUint64, false}} {
		got, err := checkedQuestU32Size(tc.start, tc.extra)
		if (err == nil) != tc.valid || (tc.valid && got != tc.start+tc.extra) {
			t.Fatalf("u32 layout %d+%d = %d, %v", tc.start, tc.extra, got, err)
		}
	}
	values := [2][]byte{{0}, nil}
	tables := []QuestGatheringTableJSON{{}}
	if err := checkAuxiliaryQuestLayout(math.MaxUint32-15, values, tables); err != nil {
		t.Fatal("exact representable tail was refused", err)
	}
	if err := checkAuxiliaryQuestLayout(math.MaxUint32-14, values, tables); err == nil {
		t.Fatal("one-byte auxiliary/gathering tail overflow was accepted")
	}
}
