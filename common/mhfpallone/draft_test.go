package mhfpallone

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"reflect"
	"testing"

	"erupe-ce/common/decryption"
)

func draftFixture() ([]byte, QuestInfo, DraftOptions) {
	b := append(templateFixture(), make([]byte, 0x100)...)
	put := func(p int, v uint32) { binary.LittleEndian.PutUint32(b[p:], v) }
	put(0x308, 0x450)
	put(0x450, 39)
	put(0x328, 119)
	put(0x490, 0x4a0)
	put(0x494, 0)
	put(0x4a0, 119)
	put(0xc, 0x560)
	put(0x560, 0x8001)
	put(0x564, 0x580)
	put(0x568, 0x8002)
	put(0x56c, 0x588)
	put(0x570, 0x8004) // unrelated donor reward table must disappear
	put(0x574, 0x590)
	binary.LittleEndian.PutUint16(b[0x578:], 0xffff)
	for _, off := range []int{0x580, 0x588, 0x590} {
		binary.LittleEndian.PutUint16(b[off:], 100)
		binary.LittleEndian.PutUint16(b[off+2:], 999)
		binary.LittleEndian.PutUint16(b[off+4:], 1)
		binary.LittleEndian.PutUint16(b[off+6:], 0xffff)
	}
	put(0x18c, 0x11223344) // ZZ variant region must not receive G10 HRP
	p := bytes.Clone(b[0x100:0x1a0])
	putP := func(off int, v uint32) { binary.LittleEndian.PutUint32(p[off:], v) }
	putP(0xc, 250)
	putP(0x10, 2800)
	putP(0x14, 950)
	putP(0x18, 2400)
	putP(0x1c, 1000)
	putP(0x20, 54000)
	binary.LittleEndian.PutUint16(p[0x2e:], 27438)
	putP(0x8c, 300)
	putP(0x90, 220)
	putP(0x94, 60)
	binary.LittleEndian.PutUint16(p[0x98:], 377)
	q := QuestInfo{QuestID: 27438, PrefixHex: hex.EncodeToString(p), PrefixSHA256: fmt.Sprintf("%x", sha256.Sum256(p)),
		MapID: 6, Fee: 250, ZennyMain: 2800, ZennyKO: 950, ZennySubA: 2400, ZennySubB: 1000, TimeFrames: 54000,
		Objectives: [3]QuestInfoObjective{{Type: 1, Target: 39, CountOrPart: 1}, {Type: 0x4004, Target: 39, CountOrPart: 1}, {}}}
	q.TextRawHex[0] = "82a082a2" // original Shift-JIS byte sequence, not UTF-8 re-encoding
	opts := DraftOptions{Reference: ReferenceStats{QuestID: q.QuestID, CoreAgrees: true, StatLarge: 7, StatSmall: 7,
		Size: 100, SizeRange: 2, LargeMonsters: []uint16{38, 39}},
		Rewards: []DraftRewardPool{
			{TemplateTableID: 1, Items: []DraftRewardItem{{Rate: 25, Item: 377, Quantity: 1}, {Rate: 75, Item: 322, Quantity: 3}}},
			{TemplateTableID: 2, Items: []DraftRewardItem{{Rate: 100, Item: 313, Quantity: 1}}},
		},
	}
	opts.Supplies[0] = []DraftSupply{{Item: 657, Quantity: 1}}
	donor := bytes.Clone(b)
	donor[0x320] = 38
	binary.LittleEndian.PutUint32(donor[0x330:], 60000) // preserve unknown condition bytes
	opts.AddSpawns = []DraftSpawn{{MonsterID: 38, Source: donor}}
	return b, q, opts
}

func TestBuildQuestDraftMapsG10FieldsWithoutLosingZZBody(t *testing.T) {
	source, q, opts := draftFixture()
	before := bytes.Clone(source)
	out, report, err := BuildQuestDraft(source, q, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, before) || report.Playable || report.Kind != "reconstructed_quest_draft_not_original" {
		t.Fatal("mutated source or claimed retail/playable provenance")
	}
	u32 := func(off int) uint32 { return binary.LittleEndian.Uint32(out[off:]) }
	for off, want := range map[int]uint32{0x4c: 300, 0x54: 220, 0x58: 60, 0x18c: 0x11223344, 0x1a4: 300, 0x1a8: 220, 0x1ac: 60} {
		if got := u32(off); got != want {
			t.Fatalf("field 0x%x = %d, want %d", off, got, want)
		}
	}
	if binary.LittleEndian.Uint16(out[0x1b0:]) != 377 || binary.LittleEndian.Uint16(out[0x12e:]) != q.QuestID || u32(0x120) != 54000 {
		t.Fatal("original focus items/identity/time were not mapped")
	}
	text := int(u32(0x128))
	first := int(u32(text))
	if !bytes.Equal(out[first:first+5], []byte{0x82, 0xa0, 0x82, 0xa2, 0}) {
		t.Fatal("source text bytes were changed")
	}
	for i := 1; i < 8; i++ {
		if p := int(u32(text + i*4)); p < len(source) || out[p] != 0 {
			t.Fatal("empty text slot is not a valid NUL string")
		}
	}
	reward := int(u32(12))
	if u32(reward) != 0x8001 || u32(reward+8) != 0x8002 || binary.LittleEndian.Uint16(out[reward+16:]) != 0xffff {
		t.Fatal("lost native reward flags or retained unrelated donor pools")
	}
	if pointer := int(u32(reward + 4)); pointer < reward+18 || binary.LittleEndian.Uint16(out[pointer+2:]) != 377 {
		t.Fatal("reward pointer is not an absolute appended item list")
	}
	if u32(0x454) != 38 || !bytes.Equal(out[0x35c:0x398], opts.AddSpawns[0].Source[0x320:0x35c]) {
		t.Fatal("additional native slot/ID array/condition bytes were not preserved")
	}
	// All untouched source bytes, including unknown geometry fields and the
	// original reward data left unreachable, must be byte-identical.
	allowed := func(i int) bool {
		return i >= 8 && i < 16 || i >= 0x44 && i < 0x50 || i >= 0x54 && i < 0x5c || i == 0x61 ||
			i >= 0x100 && i < 0x18c || i >= 0x1a4 && i < 0x1b6 || i >= 0x35c && i < 0x398 || i >= 0x454 && i < 0x458
	}
	for i, v := range source {
		if !allowed(i) && out[i] != v {
			t.Fatalf("unknown template byte changed at 0x%x", i)
		}
	}
	got, err := InspectQuestTemplate(decryption.PackSimple(out))
	if err != nil || got.EmbeddedID != q.QuestID || got.Objectives != q.Objectives || !reflect.DeepEqual(got.LargeMonsters, []uint16{39, 38}) {
		t.Fatalf("packed native draft: %+v, %v", got, err)
	}
	if report.OutputSHA256 != got.DecodedSHA256 {
		t.Fatal("output evidence hash mismatch")
	}
}

func TestBuildQuestDraftRejectsMissingOrConflictingEvidence(t *testing.T) {
	tests := map[string]func(*[]byte, *QuestInfo, *DraftOptions){
		"conflict":                func(_ *[]byte, _ *QuestInfo, o *DraftOptions) { o.Reference.CoreAgrees = false },
		"prefix hash":             func(_ *[]byte, q *QuestInfo, _ *DraftOptions) { q.PrefixSHA256 = "wrong" },
		"metadata mismatch":       func(_ *[]byte, q *QuestInfo, _ *DraftOptions) { q.Fee++ },
		"missing roster":          func(_ *[]byte, _ *QuestInfo, o *DraftOptions) { o.AddSpawns = nil },
		"unknown roster":          func(_ *[]byte, _ *QuestInfo, o *DraftOptions) { o.Reference.UnknownRoster = []string{"unknown"} },
		"unresolved reward table": func(_ *[]byte, _ *QuestInfo, o *DraftOptions) { o.Rewards[0].TemplateTableID = 0 },
		"wrong reward total":      func(_ *[]byte, _ *QuestInfo, o *DraftOptions) { o.Rewards[0].Items[0].Rate = 1 },
		"too many supplies":       func(_ *[]byte, _ *QuestInfo, o *DraftOptions) { o.Supplies[0] = make([]DraftSupply, 25) },
		"invalid source text":     func(_ *[]byte, q *QuestInfo, _ *DraftOptions) { q.TextRawHex[0] = "00" },
		"invalid spawn pointer": func(b *[]byte, _ *QuestInfo, _ *DraftOptions) {
			binary.LittleEndian.PutUint32((*b)[0x308:], 0xffffffff)
		},
		"wrong map spawn": func(_ *[]byte, _ *QuestInfo, o *DraftOptions) {
			binary.LittleEndian.PutUint32(o.AddSpawns[0].Source[0x124:], 11)
		},
		"unloaded spawn area": func(_ *[]byte, _ *QuestInfo, o *DraftOptions) {
			binary.LittleEndian.PutUint32(o.AddSpawns[0].Source[0x328:], 999)
		},
		"missing source monster": func(_ *[]byte, _ *QuestInfo, o *DraftOptions) { o.AddSpawns[0].MonsterID = 45 },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			b, q, o := draftFixture()
			change(&b, &q, &o)
			if _, _, err := BuildQuestDraft(b, q, o); err == nil {
				t.Fatal("accepted invalid draft evidence")
			}
		})
	}
}

func TestBuildQuestDraftNightKeepsNativeMapPair(t *testing.T) {
	b, q, opts := draftFixture()
	opts.Night = true
	binary.LittleEndian.PutUint32(b[0x124:], 20)
	binary.LittleEndian.PutUint32(opts.AddSpawns[0].Source[0x124:], 20)
	out, report, err := BuildQuestDraft(b, q, opts)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(out[0x124:]) != 20 || report.ListMapID != 6 || report.VariantMapID != 20 {
		t.Fatal("night geometry was relabeled as the daytime map")
	}
	// A night request cannot silently borrow daytime geometry.
	binary.LittleEndian.PutUint32(b[0x124:], 6)
	if _, _, err := BuildQuestDraft(b, q, opts); err == nil {
		t.Fatal("accepted a day body for the night variant")
	}
}
