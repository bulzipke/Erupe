package mhfpallone

import (
	"encoding/binary"
	"reflect"
	"testing"

	"erupe-ce/common/decryption"
)

func templateFixture() []byte {
	b := make([]byte, 0x500)
	put := func(p int, v uint32) { binary.LittleEndian.PutUint32(b[p:], v) }
	put(0, 0x100)
	put(4, 0x480)
	put(0x14, 0x490)
	put(0x18, 0x300)
	put(0x28, 0x4a0)
	binary.LittleEndian.PutUint16(b[0x44:], 100)
	binary.LittleEndian.PutUint16(b[0x46:], 2)
	put(0x48, 7)
	b[0x61] = 7
	put(0x120, 90000)
	put(0x124, 6)
	binary.LittleEndian.PutUint16(b[0x12e:], 54470)
	put(0x130, 1)
	binary.LittleEndian.PutUint16(b[0x134:], 39)
	binary.LittleEndian.PutUint16(b[0x136:], 1)
	put(0x138, 0x4004)
	binary.LittleEndian.PutUint16(b[0x13c:], 39)
	binary.LittleEndian.PutUint16(b[0x13e:], 1)
	put(0x30c, 0x320)
	for i := 0; i < 5; i++ {
		b[0x320+i*60] = 0xff
	}
	b[0x320] = 39
	return b
}

func TestInspectQuestTemplate(t *testing.T) {
	b := templateFixture()
	got, err := InspectQuestTemplate(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.EmbeddedID != 54470 || got.MapID != 6 || got.TimeFrames != 90000 || got.StatLarge != 7 || got.StatSmall != 7 || got.Size != 100 || got.SizeRange != 2 || !got.HasGeometry || !got.HasGathering || !reflect.DeepEqual(got.LargeMonsters, []uint16{39}) {
		t.Fatalf("native template inspection = %+v", got)
	}
	packed := decryption.PackSimple(b)
	fromPacked, err := InspectQuestTemplate(packed)
	if err != nil || fromPacked.DecodedSHA256 != got.DecodedSHA256 || fromPacked.SourceSHA256 == got.SourceSHA256 {
		t.Fatalf("packed provenance: %+v, %v", fromPacked, err)
	}
	for _, change := range []func([]byte) []byte{
		func(b []byte) []byte { return b[:0x85] },
		func(b []byte) []byte { binary.LittleEndian.PutUint32(b, 0xffffffff); return b },
		func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x18:], 0xffffffff); return b },
		func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x30c:], 0x4ff); return b },
		func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x28:], 0xffffffff); return b },
	} {
		if _, err := InspectQuestTemplate(change(templateFixture())); err == nil {
			t.Fatal("accepted invalid quest-body pointer")
		}
	}
}

func TestReconstructionPlanDoesNotApproveReplacement(t *testing.T) {
	q := QuestInfo{QuestID: 27438, MapID: 6, Objectives: [3]QuestInfoObjective{
		{Type: 1, Target: 39, CountOrPart: 1}, {Type: 0x4004, Target: 39, CountOrPart: 1}, {},
	}}
	a, err := InspectQuestTemplate(templateFixture())
	if err != nil {
		t.Fatal(err)
	}
	a.Filename = "a.bin"
	b := a
	b.Filename, b.StatLarge, b.StatSmall = "b.bin", 53, 53
	c := a
	c.Filename, c.Objectives[1] = "c.bin", QuestInfoObjective{}
	d := a
	d.Filename, d.MapID = "wrong-map.bin", 11
	e := a
	e.Filename, e.Objectives[0].Target = "wrong-target.bin", 45
	ref := &ReferenceStats{QuestID: 27438, CoreAgrees: true, StatLarge: 7, StatSmall: 7, Size: 100, SizeRange: 2}
	plan := PlanReconstruction(q, []QuestTemplate{b, d, c, e, a}, ref)
	if plan.Playable || len(plan.NeedsDecisions) == 0 || plan.AllMatches != 2 || plan.MainMatches != 3 || plan.TargetMatches != 3 || len(plan.Candidates) != 3 || plan.Candidates[0].Filename != "a.bin" || plan.Candidates[1].Filename != "b.bin" {
		t.Fatalf("unsafe/unranked reconstruction plan: %+v", plan)
	}
	// A historical reference that conflicts with G10 cannot steer the choice.
	ref.CoreAgrees = false
	plan = PlanReconstruction(q, []QuestTemplate{a, b}, ref)
	if plan.Candidates[0].ReferenceFits != 0 || len(plan.NeedsDecisions) != 5 {
		t.Fatal("used a conflicting historical stat reference")
	}
	// Reversing inventory order does not change the chosen review candidates.
	other := PlanReconstruction(q, []QuestTemplate{b, a}, ref)
	if !reflect.DeepEqual(plan.Candidates, other.Candidates) {
		t.Fatal("inventory order changed candidate ordering")
	}
}

func TestReconstructionExposesMissingConditionalMonster(t *testing.T) {
	q := QuestInfo{QuestID: 27438, MapID: 6, Objectives: [3]QuestInfoObjective{{Type: 1, Target: 39, CountOrPart: 1}, {Type: 0x4004, Target: 39, CountOrPart: 1}, {}}}
	a, err := InspectQuestTemplate(templateFixture())
	if err != nil {
		t.Fatal(err)
	}
	a.Filename = "same-objectives.bin"
	b := a
	b.Filename, b.Objectives[1] = "same-roster.bin", QuestInfoObjective{}
	b.LargeMonsters = []uint16{39, 38}
	ref := &ReferenceStats{QuestID: 27438, CoreAgrees: true, LargeMonsters: []uint16{38, 39}}
	plan := PlanReconstruction(q, []QuestTemplate{a, b}, ref)
	if plan.RosterOption == nil || plan.RosterOption.Filename != "same-roster.bin" || !reflect.DeepEqual(plan.Candidates[0].MissingMonsters, []uint16{38}) || plan.Playable {
		t.Fatalf("conditional-roster gap hidden: %+v", plan)
	}
}
