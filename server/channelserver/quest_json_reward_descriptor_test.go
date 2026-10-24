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
	"strings"
	"testing"

	"erupe-ce/common/decryption"
)

func compileRewardDescriptorFixture(t *testing.T, tables []QuestRewardTableJSON) []byte {
	t.Helper()
	var q QuestJSON
	if err := json.Unmarshal([]byte(minimalQuestJSON), &q); err != nil {
		t.Fatal(err)
	}
	q.Rewards = tables
	input, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileQuestJSON(input, "")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestQuestJSONRewardDescriptorLegacyDefaults(t *testing.T) {
	legacy, err := CompileQuestJSON([]byte(minimalQuestJSON), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, fields := range []string{
		`"descriptor_byte1_raw":0,"descriptor_word2_raw":0,`,
		`"descriptor_byte1_raw":null,"descriptor_word2_raw":null,`,
	} {
		input := strings.Replace(minimalQuestJSON, `"table_id": 1,`, `"table_id": 1,`+fields, 1)
		got, err := CompileQuestJSON([]byte(input), "")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, legacy) {
			t.Fatal("explicit zero/null changed the legacy binary output")
		}
	}
	encoded, err := json.Marshal(QuestRewardTableJSON{TableID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("descriptor_")) {
		t.Fatal("zero raw fields must be omitted from existing JSON output")
	}
}

func TestQuestJSONRewardDescriptorPreservesWidthsAndAdjacentBytes(t *testing.T) {
	tables := []QuestRewardTableJSON{
		{TableID: 1, DescriptorByte1Raw: 0x80, DescriptorWord2Raw: 0x1234, Items: []QuestRewardItemJSON{{Rate: 1, Item: 656, Quantity: 1}, {Rate: 99, Item: 364, Quantity: 2}}},
		{TableID: 255, DescriptorByte1Raw: 254, DescriptorWord2Raw: 65535, Items: []QuestRewardItemJSON{{Rate: 65534, Item: 65535, Quantity: 65535}}},
		{TableID: 254, DescriptorByte1Raw: 255, DescriptorWord2Raw: 0xabcd},
	}
	compiled := compileRewardDescriptorFixture(t, tables)
	zero := make([]QuestRewardTableJSON, len(tables))
	copy(zero, tables)
	for i := range zero {
		zero[i].DescriptorByte1Raw, zero[i].DescriptorWord2Raw = 0, 0
	}
	legacy := compileRewardDescriptorFixture(t, zero)
	rp := int(binary.LittleEndian.Uint32(compiled[0xc:]))
	control := append([]byte(nil), compiled...)
	nextItems := rp + len(tables)*8 + 2
	for i, table := range tables {
		off := rp + i*8
		if compiled[off] != table.TableID || compiled[off+1] != table.DescriptorByte1Raw || binary.LittleEndian.Uint16(compiled[off+2:]) != table.DescriptorWord2Raw {
			t.Fatalf("descriptor %d raw bytes changed", i)
		}
		if got := int(binary.LittleEndian.Uint32(compiled[off+4:])); got != nextItems {
			t.Fatalf("table %d absolute item pointer = %#x, want %#x", i, got, nextItems)
		}
		for _, item := range table.Items {
			want := []uint16{item.Rate, item.Item, item.Quantity}
			for j, v := range want {
				if binary.LittleEndian.Uint16(compiled[nextItems+j*2:]) != v {
					t.Fatalf("table %d item scalar %d changed", i, j)
				}
			}
			nextItems += 6
		}
		if !bytes.Equal(compiled[nextItems:nextItems+2], []byte{255, 255}) {
			t.Fatal("item list lost its two-byte terminator")
		}
		nextItems += 2
		copy(control[off+1:off+4], legacy[off+1:off+4])
	}
	if !bytes.Equal(compiled[rp+len(tables)*8:rp+len(tables)*8+2], []byte{255, 255}) {
		t.Fatal("descriptor list lost its two-byte terminator")
	}
	if !bytes.Equal(control, legacy) {
		t.Fatal("raw descriptor fields changed bytes outside their three-byte ranges")
	}
	parsed, err := ParseQuestBinary(compiled)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed.Rewards, tables) {
		t.Fatalf("parse lost opaque descriptor or item values: %#v", parsed.Rewards)
	}
	if got := compileRewardDescriptorFixture(t, parsed.Rewards); !bytes.Equal(got, compiled) {
		t.Fatal("reward projection is not byte-stable after parse/compile")
	}
}

func TestQuestJSONRewardDescriptorRejectsReservedTerminator(t *testing.T) {
	for _, word := range []uint16{0, 12345, 65535} {
		q := QuestJSON{Rewards: []QuestRewardTableJSON{{TableID: 255, DescriptorByte1Raw: 255, DescriptorWord2Raw: word}}}
		input, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := CompileQuestJSON(input, ""); err == nil || !strings.Contains(err.Error(), "reserved terminator") {
			t.Fatalf("reserved descriptor accepted or wrong error: %v", err)
		}
	}
}

func TestQuestJSONRewardDescriptorRejectsOutOfRange(t *testing.T) {
	for _, field := range []struct {
		name string
		bad  []string
	}{
		{"table_id", []string{"-1", "256", "1.5", `"1"`}},
		{"descriptor_byte1_raw", []string{"-1", "256", "1.5", `"128"`}},
		{"descriptor_word2_raw", []string{"-1", "65536", "1.5", `"1"`}},
	} {
		for _, value := range field.bad {
			t.Run(field.name+"/"+value, func(t *testing.T) {
				input := []byte(`{"rewards":[{"` + field.name + `":` + value + `}]}`)
				if _, err := CompileQuestJSON(input, ""); err == nil {
					t.Fatal("invalid descriptor scalar was accepted")
				}
			})
		}
	}
}

func TestQuestJSONRewardDescriptorTerminatorsAndTruncation(t *testing.T) {
	// A list terminator is only two bytes; no descriptor/pointer read follows it.
	if got, err := parseRewardTables([]byte{255, 255}, 0); err != nil || len(got) != 0 {
		t.Fatalf("two-byte empty list rejected: %v", err)
	}
	for _, input := range [][]byte{{}, {1}, {1, 128}, {1, 128, 1, 2, 0, 0, 0}, {1, 128, 1, 2, 255, 255, 255, 127, 255, 255}} {
		if _, err := parseRewardTables(input, 0); err == nil {
			t.Fatalf("truncated header or invalid pointer accepted: %x", input)
		}
	}
	// Word2 may itself be FFFF; it is not the first u16 sentinel.
	data := []byte{1, 128, 255, 255, 10, 0, 0, 0, 255, 255, 255, 255}
	got, err := parseRewardTables(data, 0)
	if err != nil || len(got) != 1 || got[0].DescriptorWord2Raw != 65535 || len(got[0].Items) != 0 {
		t.Fatalf("raw word interpreted as sentinel or items: %#v %v", got, err)
	}
}

func TestQuestJSONRewardDescriptorCapturedNASProjection(t *testing.T) {
	dir := os.Getenv("ERUPE_QUEST_REWARD_DESCRIPTOR_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set ERUPE_QUEST_REWARD_DESCRIPTOR_CAPTURE_DIR to detached saved NAS quest captures")
	}
	for _, tc := range []struct{ file, hash string }{
		{"40060d0.bin", "014815a5fe6b77dcda473d48808e363273eba7bf0cdad92e15c4963c1d649219"},
		{"40133d0.bin", "4e480537ffdd729266a992572cc96abc3aa4641b01e9fa9def83673e5593cc88"},
		{"40134d0.bin", "4ad642c1eff9475e901e34af796b5d549981c4b99c38e48a0e41e497a684d529"},
		{"40135d0.bin", "35afc766da5835716cafc4a3b33f9ae211ff54f3f852c2cd37aa042fcdc19bec"},
		{"40136d0.bin", "d6a2ffdd4ba0b42825c64c45d429907af1cf486df898fc22d69f6fbc7fd50e8e"},
		{"40137d0.bin", "92ca1e7c66eed66532c8354da6764e805ff6cdd393c997ed756e2fc8b114ad92"},
		{"40138d0.bin", "830336eae14f769322b9d1ef2725afb2983a67cbb4ab1ef43ccc70ddd352fb81"},
		{"40162d0.bin", "28f198bd0521ba492d749a1dc048a905cdfb1199879aac099af3b5bb3c1babaf"},
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
			rp := int(binary.LittleEndian.Uint32(original[0xc:]))
			if !bytes.Equal(original[rp:rp+4], []byte{1, 128, 0, 0}) {
				t.Fatal("original descriptor differs from reviewed raw evidence")
			}
			parsed, err := ParseQuestBinary(original, "ko")
			if err != nil {
				t.Fatal(err)
			}
			if len(parsed.Rewards) != 1 || parsed.Rewards[0].TableID != 1 || parsed.Rewards[0].DescriptorByte1Raw != 128 || parsed.Rewards[0].DescriptorWord2Raw != 0 {
				t.Fatal("captured opaque descriptor was not retained")
			}
			// Project the parsed reward section into a valid minimal fixture.
			// Mixed-encoding original text and other unmodeled bytes are not rebuilt.
			compiled := compileRewardDescriptorFixture(t, parsed.Rewards)
			crp := int(binary.LittleEndian.Uint32(compiled[0xc:]))
			if !bytes.Equal(compiled[crp:crp+4], original[rp:rp+4]) {
				t.Fatal("projection lost original descriptor scalars")
			}
			round, err := ParseQuestBinary(compiled)
			if err != nil || !reflect.DeepEqual(round.Rewards, parsed.Rewards) {
				t.Fatalf("projected reward items or raw descriptor changed: %v", err)
			}
		})
	}
}
