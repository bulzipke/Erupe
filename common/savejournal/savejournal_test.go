package savejournal

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var testRegions = []Region{
	{Name: "zenny", Off: 0xB0, Len: 4, Numeric: true},
	{Name: "hr", Off: 0x200, Len: 2, Numeric: true},
	{Name: "house", Off: 0x300, Len: 64},
}

func blob(size int) []byte { return make([]byte, size) }

func TestDiffIdenticalBlobsHaveNoRanges(t *testing.T) {
	a := blob(4096)
	a[10] = 7
	res := Diff(a, append([]byte(nil), a...), testRegions, DefaultByteBudget)
	if res.Changed != 0 || len(res.Ranges) != 0 || res.Truncated {
		t.Fatalf("identical blobs produced %+v", res)
	}
}

func TestDiffReportsChangedFieldWithRegionName(t *testing.T) {
	before, after := blob(4096), blob(4096)
	copy(before[0xB0:], []byte{0xE8, 0x03, 0x00, 0x00}) // 1000
	copy(after[0xB0:], []byte{0x88, 0x13, 0x00, 0x00})  // 5000
	res := Diff(before, after, testRegions, DefaultByteBudget)
	if len(res.Ranges) != 1 {
		t.Fatalf("ranges = %d, want 1: %+v", len(res.Ranges), res.Ranges)
	}
	r := res.Ranges[0]
	if r.Off != 0xB0 || r.Old != "e803" || r.New != "8813" || r.At != "zenny" {
		t.Fatalf("range = %+v", r)
	}
	if res.Changed != 2 {
		t.Fatalf("changed = %d, want 2 differing bytes", res.Changed)
	}
	fields := Fields(before, after, testRegions)
	if got := fields["zenny"]; got != [2]uint64{1000, 5000} {
		t.Fatalf("zenny field = %v, want [1000 5000]", got)
	}
	if _, ok := fields["hr"]; ok {
		t.Fatal("unchanged numeric region was reported")
	}
}

func TestDiffMergesRunsSeparatedByShortGap(t *testing.T) {
	before, after := blob(1024), blob(1024)
	after[100], after[105] = 1, 1 // 4 equal bytes between them
	after[400], after[420] = 1, 1 // 19 equal bytes between them
	res := Diff(before, after, nil, DefaultByteBudget)
	if len(res.Ranges) != 3 {
		t.Fatalf("ranges = %+v, want one merged and two separate", res.Ranges)
	}
	if r := res.Ranges[0]; r.Off != 100 || r.New != "010000000001" || r.Old != "000000000000" {
		t.Fatalf("merged range = %+v", r)
	}
	if res.Ranges[1].Off != 400 || res.Ranges[2].Off != 420 {
		t.Fatalf("separate ranges = %+v", res.Ranges[1:])
	}
	if res.Changed != 4 {
		t.Fatalf("changed = %d, want 4", res.Changed)
	}
}

func TestDiffRecordsGrowthAndShrinkAsTailRange(t *testing.T) {
	short := []byte{1, 2, 3, 4}
	long := []byte{1, 2, 3, 4, 9, 9}

	grown := Diff(short, long, nil, DefaultByteBudget)
	if len(grown.Ranges) != 1 || grown.Ranges[0] != (Range{Off: 4, New: "0909"}) || grown.Changed != 2 {
		t.Fatalf("growth = %+v", grown)
	}
	shrunk := Diff(long, short, nil, DefaultByteBudget)
	if len(shrunk.Ranges) != 1 || shrunk.Ranges[0] != (Range{Off: 4, Old: "0909"}) || shrunk.Changed != 2 {
		t.Fatalf("shrink = %+v", shrunk)
	}
}

func TestDiffKeepsOffsetsWhenByteBudgetIsSpent(t *testing.T) {
	before, after := blob(1024), blob(1024)
	after[10] = 1
	for i := 500; i < 600; i++ {
		after[i] = 0xFF
	}
	res := Diff(before, after, nil, 8) // room for the first range only
	if !res.Truncated || len(res.Ranges) != 2 {
		t.Fatalf("result = %+v", res)
	}
	if res.Ranges[0].New != "01" {
		t.Fatalf("first range lost its bytes: %+v", res.Ranges[0])
	}
	if r := res.Ranges[1]; r.Off != 500 || r.Old != "" || r.New != "" || r.OldLen != 100 || r.NewLen != 100 {
		t.Fatalf("over-budget range = %+v", r)
	}
	if res.Changed != 101 {
		t.Fatalf("changed = %d, want the exact 101 even when truncated", res.Changed)
	}
}

func TestDiffCapsRangeCount(t *testing.T) {
	size := (maxRanges + 10) * (mergeGap + 2)
	before, after := blob(size), blob(size)
	for i := 0; i < size; i += mergeGap + 2 {
		after[i] = 1
	}
	res := Diff(before, after, nil, DefaultByteBudget)
	if len(res.Ranges) != maxRanges || !res.Truncated {
		t.Fatalf("ranges = %d truncated = %v", len(res.Ranges), res.Truncated)
	}
	if res.Changed != maxRanges+10 {
		t.Fatalf("changed = %d, want %d", res.Changed, maxRanges+10)
	}
}

func TestFieldsIgnoreRegionsOutsideEitherBlob(t *testing.T) {
	before, after := blob(0x100), blob(0x400)
	after[0x200] = 5
	if fields := Fields(before, after, testRegions); len(fields) != 0 {
		t.Fatalf("out-of-range region decoded: %v", fields)
	}
}

func TestActivityCountsEventsAndResetsOnTake(t *testing.T) {
	var a Activity
	start := time.Unix(1_000, 0)
	a.Count(0x60, start)
	a.Count(0x60, start.Add(time.Second))
	a.Count(0x19A, start.Add(2*time.Second))
	a.Add(Event{Kind: "quest_enter", Quest: 23527}, start.Add(90*time.Second))

	w := a.Take(start.Add(120 * time.Second))
	if w.Seconds != 120 || w.Ops[0x60] != 2 || w.Ops[0x19A] != 1 {
		t.Fatalf("window = %+v", w)
	}
	if len(w.Events) != 1 || w.Events[0].At != 90 || w.Events[0].Quest != 23527 {
		t.Fatalf("events = %+v", w.Events)
	}
	next := a.Take(start.Add(150 * time.Second))
	if next.Seconds != 30 || len(next.Ops) != 0 || len(next.Events) != 0 {
		t.Fatalf("window was not reset: %+v", next)
	}
}

func TestActivityBoundsEvents(t *testing.T) {
	var a Activity
	now := time.Unix(1_000, 0)
	for i := 0; i < maxEvents+5; i++ {
		a.Add(Event{Kind: "quest_enter"}, now)
	}
	w := a.Take(now)
	if len(w.Events) != maxEvents || w.Dropped != 5 {
		t.Fatalf("events = %d dropped = %d", len(w.Events), w.Dropped)
	}
}

func TestOptionsValidate(t *testing.T) {
	if err := DefaultOptions().Validate(); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
	if err := (Options{}).Validate(); err != nil {
		t.Fatalf("disabled journal must not be validated: %v", err)
	}
	bad := DefaultOptions()
	bad.MaxBackups = 0
	if err := bad.Validate(); err == nil {
		t.Fatal("zero backups accepted")
	}
}

func TestDisabledJournalIsNoop(t *testing.T) {
	j, err := Open(Options{}, t.TempDir())
	if err != nil || j != nil {
		t.Fatalf("Open(disabled) = %v, %v", j, err)
	}
	if j.Enabled() {
		t.Fatal("nil journal reports enabled")
	}
	if err := j.Write(&Record{}); err != nil {
		t.Fatalf("Write on nil journal: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close on nil journal: %v", err)
	}
}

func TestJournalWritesOneJSONLinePerRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "logs")
	opts := DefaultOptions()
	j, err := Open(opts, dir) // empty Directory falls back to the log directory
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cleared := true
	const writers, perWriter = 8, 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				rec := &Record{V: RecordVersion, Time: "2026-11-15T00:00:00.000Z", CharID: uint32(w), Kind: "blob",
					Fields: map[string][2]uint64{"zenny": {1, 2}},
					Ranges: []Range{{Off: 176, Old: "01", New: "02", At: "zenny"}},
					Activity: &ActivityRecord{Seconds: 5, Ops: map[string]uint32{"MSG_MHF_ENUMERATE_SHOP": 1},
						Events: []Event{{Kind: "quest_result", Quest: 1, Frames: 900, Cleared: &cleared}}}}
				if err := j.Write(rec); err != nil {
					t.Errorf("Write: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("journal file: %v", err)
	}
	defer func() { _ = f.Close() }()
	lines := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec Record
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("line %d is not one JSON record: %v", lines+1, err)
		}
		if rec.V != RecordVersion || rec.Ranges[0].Off != 176 || rec.Activity.Events[0].Cleared == nil {
			t.Fatalf("line %d lost data: %+v", lines+1, rec)
		}
		lines++
	}
	if lines != writers*perWriter {
		t.Fatalf("lines = %d, want %d", lines, writers*perWriter)
	}
}

func putStack(b []byte, off int, item, quantity uint16) {
	b[off], b[off+1] = byte(item), byte(item>>8)
	b[off+2], b[off+3] = byte(quantity), byte(quantity>>8)
}

func TestStackTotalsReportQuantitiesNotPositions(t *testing.T) {
	regions := []Region{
		{Name: "box", Off: 0x100, Len: 6 * 8, Stacks: true},
		{Name: "pouch", Off: 0x400, Len: 2 * 8, Stacks: true},
		{Name: "zenny", Off: 0xB0, Len: 4, Numeric: true},
	}
	before := blob(4096)
	putStack(before, 0x100, 7976, 20)  // sold below
	putStack(before, 0x108, 181, 999)  // moved to another slot below
	putStack(before, 0x110, 8070, 300) // two stacks of one item
	putStack(before, 0x118, 8070, 5)
	putStack(before, 0x400, 8070, 1)
	after := append([]byte(nil), before...)
	putStack(after, 0x100, 181, 999)
	putStack(after, 0x108, 0, 0)
	putStack(after, 0x118, 8070, 4)
	putStack(after, 0x120, 13771, 2)
	putStack(after, 0x400, 8070, 2)

	got := StackTotals(before, after, regions)
	box, pouch := got["box"], got["pouch"]
	if len(got) != 2 || len(box) != 3 || len(pouch) != 1 {
		t.Fatalf("totals = %+v", got)
	}
	if box["7976"] != [2]uint32{20, 0} || box["8070"] != [2]uint32{305, 304} || box["13771"] != [2]uint32{0, 2} {
		t.Fatalf("box totals = %+v", box)
	}
	if _, moved := box["181"]; moved {
		t.Fatal("a stack that only changed its slot was reported")
	}
	if pouch["8070"] != [2]uint32{1, 2} {
		t.Fatalf("pouch totals = %+v", pouch)
	}

	// Sorting alone changes bytes but no total.
	sorted := append([]byte(nil), before...)
	copy(sorted[0x100:0x108], before[0x118:0x120])
	copy(sorted[0x118:0x120], before[0x100:0x108])
	if got := StackTotals(before, sorted, regions); got != nil {
		t.Fatalf("a rearranged box reported %+v", got)
	}
	if got := StackTotals(before, append([]byte(nil), before...), regions); got != nil {
		t.Fatalf("an unchanged box reported %+v", got)
	}
}

func TestStackTotalsStayInsideShortBlobs(t *testing.T) {
	regions := []Region{{Name: "box", Off: 0x100, Len: 4 * 8, Stacks: true}}
	before := blob(0x100 + 12) // ends inside the second stack
	putStack(before, 0x100, 5, 9)
	after := append([]byte(nil), before...)
	putStack(after, 0x100, 5, 10)
	after[0x100+8] = 7 // half a stack: not counted, not read past the end
	if got := StackTotals(before, after, regions); len(got["box"]) != 1 || got["box"]["5"] != [2]uint32{9, 10} {
		t.Fatalf("totals = %+v", got)
	}
	if got := StackTotals(blob(16), blob(16), regions); got != nil {
		t.Fatalf("a region outside the blob reported %+v", got)
	}
}
