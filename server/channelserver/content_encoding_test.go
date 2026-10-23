package channelserver

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestContentTextKoreanWireEncoding(t *testing.T) {
	// Include a CP949 extension syllable outside the KS X 1001 base table.
	want := []byte{0xBC, 0xF6, 0xB7, 0xC6, 0xB4, 0xDC, 0x94, 0xEE, 0}
	encoded, err := encodeContentText("수렵단뷁", "ko")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("Korean wire bytes = % X, want % X", encoded, want)
	}
	decoded, err := decodeContentText(want[:len(want)-1], "ko")
	if err != nil || decoded != "수렵단뷁" {
		t.Fatalf("Korean decode = %q, %v", decoded, err)
	}
	if _, err := encodeContentText("\U0001F600", "ko"); err == nil {
		t.Fatal("unrepresentable Korean content must return an error")
	}
}

func TestCompileQuestJSONKoreanText(t *testing.T) {
	var q QuestJSON
	if err := json.Unmarshal([]byte(minimalQuestJSON), &q); err != nil {
		t.Fatal(err)
	}
	q.Title = mustLocalized(t, `{"ko":"수렵단","en":"Guild","jp":"猟団"}`)
	q.TextMain = NewLocalizedPlain("수렵단")
	q.TextSubA = NewLocalizedPlain("수렵단")
	q.TextSubB = NewLocalizedPlain("수렵단")
	q.SuccessCond = NewLocalizedPlain("수렵단")
	q.FailCond = NewLocalizedPlain("수렵단")
	q.Contractor = NewLocalizedPlain("수렵단")
	q.Description = NewLocalizedPlain("수렵단")
	q.SomeString = "수렵단"
	q.QuestType = "수렵단"
	src, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileQuestJSON(src, "ko")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0xBC, 0xF6, 0xB7, 0xC6, 0xB4, 0xDC, 0}
	body := int(binary.LittleEndian.Uint32(compiled))
	table := int(binary.LittleEndian.Uint32(compiled[body+0x28:]))
	for i := 0; i < 8; i++ {
		off := int(binary.LittleEndian.Uint32(compiled[table+i*4:]))
		if !bytes.Equal(compiled[off:off+len(want)], want) {
			t.Errorf("quest text field %d does not contain CP949 bytes", i)
		}
	}
	parsed, err := ParseQuestBinary(compiled, "ko")
	if err != nil {
		t.Fatal(err)
	}
	texts := []LocalizedString{parsed.Title, parsed.TextMain, parsed.TextSubA,
		parsed.TextSubB, parsed.SuccessCond, parsed.FailCond, parsed.Contractor, parsed.Description}
	for i, text := range texts {
		if got := text.Resolve("ko"); got != "수렵단" {
			t.Errorf("parsed quest field %d = %q", i, got)
		}
	}
	if parsed.SomeString != "수렵단" || parsed.QuestType != "수렵단" {
		t.Errorf("extra quest strings = %q / %q", parsed.SomeString, parsed.QuestType)
	}
}

func TestCompileScenarioJSONKoreanText(t *testing.T) {
	for _, inline := range []bool{false, true} {
		name := "subheader"
		if inline {
			name = "inline"
		}
		t.Run(name, func(t *testing.T) {
			text := mustLocalized(t, `{"ko":"수렵단뷁","en":"Guild","jp":"猟団"}`)
			sh := &ScenarioSubheaderJSON{Type: 1, Strings: []LocalizedString{text}}
			input := ScenarioJSON{
				Chunk0: &ScenarioChunk0JSON{Subheader: sh},
				Chunk1: &ScenarioChunk1JSON{Subheader: sh},
			}
			if inline {
				input.Chunk0 = &ScenarioChunk0JSON{Inline: []ScenarioInlineEntry{{Index: 1, Text: text}}}
			}
			src, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := CompileScenarioJSON(src, "ko")
			if err != nil {
				t.Fatal(err)
			}
			want := []byte{0xBC, 0xF6, 0xB7, 0xC6, 0xB4, 0xDC, 0x94, 0xEE, 0}
			if got := bytes.Count(compiled, want); got != 2 {
				t.Fatalf("CP949 text count = %d, want 2", got)
			}
			parsed, err := ParseScenarioBinary(compiled, "ko")
			if err != nil {
				t.Fatal(err)
			}
			got0 := ""
			if inline {
				got0 = parsed.Chunk0.Inline[0].Text.Resolve("ko")
			} else {
				got0 = parsed.Chunk0.Subheader.Strings[0].Resolve("ko")
			}
			got1 := parsed.Chunk1.Subheader.Strings[0].Resolve("ko")
			if got0 != "수렵단뷁" || got1 != "수렵단뷁" {
				t.Fatalf("decoded scenario = %q / %q", got0, got1)
			}
			src, err = json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}
			recompiled, err := CompileScenarioJSON(src, "ko")
			if err != nil || !bytes.Equal(compiled, recompiled) {
				t.Fatalf("Korean scenario round trip changed bytes: %v", err)
			}
		})
	}
}
