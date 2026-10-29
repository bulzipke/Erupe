package mhfearth

import (
	"encoding/hex"
	"testing"
)

func TestNativeCalendarContainsSeparateTowerAndFestivalRows(t *testing.T) {
	b, err := Encode([]Status{
		{Start: 1, End: 2, Category: 21, EventID: 36, Monsters: [4]int32{1, 2, 3, 4}},
		{Start: 3, End: 4, Category: 11, EventID: 41},
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = "0000000100000002000000150000002400000001000000020000000300000004" +
		"00000003000000040000000b0000002900000000000000000000000000000000"
	if len(b) != 2*RecordSize || hex.EncodeToString(b) != want {
		t.Fatalf("native calendar = %x", b)
	}
}

func TestNativeCalendarCapacity(t *testing.T) {
	b, err := Encode(make([]Status, MaxRecords))
	if err != nil || len(b) != MaxRecords*RecordSize {
		t.Fatal("native capacity", err, len(b))
	}
	if _, err := Encode(make([]Status, MaxRecords+1)); err == nil {
		t.Fatal("overflowed native schedule cache")
	}
	b, err = Encode(nil)
	if err != nil || len(b) != 0 {
		t.Fatal("empty calendar gained a header")
	}
}
