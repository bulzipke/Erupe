package mhfpallone

import (
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"testing"

	"erupe-ce/common/decryption"
)

func questInfoFixture(t *testing.T) []byte {
	t.Helper()
	// Actual G10.1 JP list-record prefix for 27438. Only the text pointer
	// is relocated; all numeric fields and unknown bytes remain original.
	record, err := hex.DecodeString(
		"41000a000101120011000000fa000000f00a0000b603000060090000e8030000f0d2000006000000" +
			"d46d0d0000002e6b0100000027000100044000002700010000000000000000000200110000001100" +
			"00000000000000000000000000000000000000000000000000000000000000000000000000000000" +
			"00000000000000000000000000000000000000002c010000dc0000003c0000000000000000000000")
	if err != nil || len(record) != questInfoPrefix {
		t.Fatalf("native prefix: %d bytes, %v", len(record), err)
	}
	b := make([]byte, 0x400)
	put := func(p int, v uint32) { binary.LittleEndian.PutUint32(b[p:], v) }
	put(0, questInfoMagic)
	put(4, 6)
	put(12, 0x78)
	put(0x10, 0x80)
	put(0x14, 0x90)
	binary.LittleEndian.PutUint16(b[0x80:], 1)
	binary.LittleEndian.PutUint16(b[0x90:], 27500)
	binary.LittleEndian.PutUint16(b[0x92:], 1)
	put(0x94, 0xa0)
	put(0xa0, 0x100)
	copy(b[0x100:], record)
	put(0x128, 0x200)
	put(0x200, 0x240)
	put(0x204, 0x260)
	copy(b[0x240:], "native title\x00")
	copy(b[0x260:], []byte{0x93, 0xfa, 0x96, 0x7b, 0}) // Japanese source text: 日本
	return b
}

func TestRecoverG10OriginalQuestInfo(t *testing.T) {
	b := questInfoFixture(t)
	got, err := RecoverQuestInfoG10(b, []uint16{27439, 27438, 27438})
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "source_metadata_not_loadable_quest" || got.Required != 2 || got.Recovered != 1 || !reflect.DeepEqual(got.MissingIDs, []uint16{27439}) || len(got.AbsentSections) == 0 {
		t.Fatalf("recovery confused metadata with stage availability: %+v", got)
	}
	q := got.Quests[0]
	if q.QuestID != 27438 || q.Fee != 250 || q.ZennyMain != 2800 || q.ZennyKO != 950 || q.ZennySubA != 2400 || q.ZennySubB != 1000 || q.TimeFrames != 54000 || q.MapID != 6 {
		t.Fatalf("native numeric fields: %+v", q)
	}
	want := [3]QuestInfoObjective{{Type: 1, Target: 39, CountOrPart: 1}, {Type: 0x4004, Target: 39, CountOrPart: 1}, {}}
	if q.Objectives != want || q.Text[0] != "native title" || q.Text[1] != "日本" || q.TextRawHex[1] != "93fa967b" {
		t.Fatalf("native objectives/text: %+v", q)
	}
	if q.PrefixHex != hex.EncodeToString(b[0x100:0x1a0]) || len(q.PrefixSHA256) != 64 || q.SourceOffset != 0x100 {
		t.Fatal("original unknown bytes or provenance lost")
	}
	packed := decryption.PackSimple(b)
	encrypted, err := decryption.EncodeECD(packed, decryption.DefaultECDKey)
	if err != nil {
		t.Fatal(err)
	}
	fromContainer, err := RecoverQuestInfoG10(encrypted, []uint16{27439, 27438})
	if err != nil || !reflect.DeepEqual(got, fromContainer) {
		t.Fatalf("container recovery differs: %v", err)
	}
}

func TestG10QuestInfoRejectsBrokenSources(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"short", func(b []byte) []byte { return b[:0x77] }},
		{"wrong file", func(b []byte) []byte { b[0] = 'm'; return b }},
		{"wrong version", func(b []byte) []byte { b[4] = 7; return b }},
		{"ZZ profile", func(b []byte) []byte { b[12] = 0x88; return b }},
		{"count pointer", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x10:], 0xffffffff); return b }},
		{"too many categories", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[0x80:], 1025); return b }},
		{"category table", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x14:], 0xffffffff); return b }},
		{"quest array", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x94:], 0xffffffff); return b }},
		{"quest record", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0xa0:], 0x3f0); return b }},
		{"duplicate ID", func(b []byte) []byte {
			binary.LittleEndian.PutUint16(b[0x92:], 2)
			binary.LittleEndian.PutUint32(b[0xa4:], 0x100)
			return b
		}},
		{"text table", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x128:], 0xffffffff); return b }},
		{"text pointer", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x200:], 0xffffffff); return b }},
		{"unterminated text", func(b []byte) []byte {
			for i := 0x240; i < len(b); i++ {
				b[i] = 'x'
			}
			return b
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := RecoverQuestInfoG10(tt.mutate(questInfoFixture(t)), []uint16{27438}); err == nil {
				t.Fatal("accepted malformed or incompatible metadata")
			}
		})
	}
	if _, err := RecoverQuestInfoG10(questInfoFixture(t), []uint16{0}); err == nil {
		t.Fatal("accepted quest ID zero")
	}
}
