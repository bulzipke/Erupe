package config

import "testing"

func TestDivaBonusNativeConditions(t *testing.T) {
	for _, tt := range []struct {
		name     string
		kind     uint8
		min, max int64
	}{
		{"rank", 1, 1, 3},
		{"time_of_day", 3, 0, 1},
		{"monster_family", 4, 1, 9},
		{"monster_class", 7, 0, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := DivaBonusTarget{Color: 1, TargetType: tt.name, EndOffsetSeconds: 60, MultiplierPercent: 200}
			if kind, ok := r.NativeKind(); !ok || kind != tt.kind {
				t.Fatalf("native kind = %d, %t; want %d", kind, ok, tt.kind)
			}
			for id := tt.min; id <= tt.max; id++ {
				r.TargetID = id
				if err := ValidateDivaBonusTargets([]DivaBonusTarget{r}); err != nil {
					t.Fatalf("valid %s/%d: %v", tt.name, id, err)
				}
			}
			for _, id := range []int64{tt.min - 1, tt.max + 1} {
				r.TargetID = id
				if err := ValidateDivaBonusTargets([]DivaBonusTarget{r}); err == nil {
					t.Fatalf("accepted invalid %s/%d", tt.name, id)
				}
			}
		})
	}
	if _, ok := (DivaBonusTarget{TargetType: "unknown"}).NativeKind(); ok {
		t.Fatal("unknown type fell back to native condition")
	}
}

func TestDivaBonusCanonicalFields(t *testing.T) {
	r := DivaBonusTarget{Color: 1, TargetType: "field", EndOffsetSeconds: 60, MultiplierPercent: 200}
	if kind, ok := r.NativeKind(); !ok || kind != 2 {
		t.Fatal("wrong field kind")
	}
	for _, id := range []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 16, 18, 20, 21, 24, 25, 26, 27, 28, 31, 33, 34, 35, 37, 38, 39, 40, 41, 42, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69} {
		r.TargetID = id
		if err := ValidateDivaBonusTargets([]DivaBonusTarget{r}); err != nil {
			t.Fatalf("field %d: %v", id, err)
		}
	}
	for _, id := range []int64{-1, 0, 14, 15, 17, 19, 22, 23, 29, 30, 32, 36, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 70, 98, 65535} {
		r.TargetID = id
		if err := ValidateDivaBonusTargets([]DivaBonusTarget{r}); err == nil {
			t.Fatalf("invalid/alias field %d accepted", id)
		}
	}
}
