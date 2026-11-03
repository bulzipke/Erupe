package channelserver

import (
	"math"
	"math/bits"
	"math/rand"
	"reflect"
	"testing"
)

func TestDivaBeadRotationHistoricalWeights(t *testing.T) {
	if len(divaBeadCandidates) != 915 {
		t.Fatal("candidate count", len(divaBeadCandidates))
	}
	want := map[int]float64{1: 28.0 / 48, 14: 20.0 / 48, 17: 33.0 / 48, 18: 28.0 / 48, 19: 24.0 / 48}
	moments := map[int]float64{}
	var sizes [5]float64
	for _, row := range divaBeadCoreSubsets {
		if row.probability <= 0 {
			t.Fatal("unavailable core subset", row.ids)
		}
		sizes[len(row.ids)] += row.probability
		for _, id := range row.ids {
			moments[id] += row.probability
		}
	}
	for id, target := range want {
		if math.Abs(moments[id]-target) > 1e-7 {
			t.Fatalf("effect %d inclusion %.9f want %.9f", id, moments[id], target)
		}
	}
	for k := 2; k <= 4; k++ {
		if math.Abs(sizes[k]-divaBeadCoreSizeWeights[k]/48) > 1e-9 {
			t.Fatal("core-size probability", k, sizes[k])
		}
	}
	seen := map[uint32]bool{}
	for _, row := range divaBeadCandidates {
		if seen[row.mask] || bits.OnesCount32(row.mask) != 4 {
			t.Fatal("duplicate/non-quartet candidate", row)
		}
		seen[row.mask] = true
		for _, id := range row.ids {
			if _, ok := divaBeadDefinitionFor(id); !ok {
				t.Fatal("internal effect exposed", id)
			}
		}
	}
}

func TestDivaBeadRotationLongRun(t *testing.T) {
	if testing.Short() {
		t.Skip("100,000-round distribution check")
	}
	rng := rand.New(rand.NewSource(20261104))
	state := divaBeadRotationState{Slots: []int{1, 3, 4, 8}, Same: 1}
	counts := map[int]int{}
	var overlaps [5]int
	coreMask := divaBeadMask([]int{1, 14, 17, 18, 19})
	maxAge := 0
	for round := 0; round < 100000; round++ {
		previous := state
		state = nextDivaBeadRotation(previous, rng.Int63())
		mask := divaBeadMask(state.Slots)
		if len(state.Slots) != 4 || bits.OnesCount32(mask) != 4 || bits.OnesCount32(mask&coreMask) < 2 || state.Same > 2 {
			t.Fatalf("round %d invalid: %+v", round, state)
		}
		overlaps[bits.OnesCount32(mask&divaBeadMask(previous.Slots))]++
		for i, id := range previous.Slots {
			if mask&(1<<id) != 0 && state.Slots[i] != id {
				t.Fatalf("retained effect moved color: %v -> %v", previous.Slots, state.Slots)
			}
		}
		for _, id := range state.Slots {
			counts[id]++
		}
		for _, age := range state.Absence {
			maxAge = max(maxAge, age)
			if age > 36 {
				t.Fatalf("absence bound exceeded at round %d: %+v", round, state)
			}
		}
	}
	// Independent 100,000-round Python prototype, tolerances in percentage
	// points. Different PRNGs intentionally do not promise identical draws.
	expected := map[int]float64{17: 62.283, 18: 55.993, 1: 55.940, 19: 51.549, 14: 47.396,
		4: 21.243, 3: 16.423, 25: 15.070, 8: 14.296, 23: 10.398, 11: 9.319,
		20: 6.662, 10: 6.638, 15: 5.379, 21: 5.360, 9: 5.355, 22: 5.349, 24: 5.347}
	if len(counts) != 18 {
		t.Fatal("some public effects missing", counts)
	}
	for id, target := range expected {
		percent := float64(counts[id]) / 1000
		if math.Abs(percent-target) > 1.5 {
			t.Fatalf("effect %d %.3f%% differs from prototype %.3f%%", id, percent, target)
		}
	}
	for overlap, target := range []float64{15.11, 49.79, 22.89, 9.30, 2.91} {
		if math.Abs(float64(overlaps[overlap])/1000-target) > 1.5 {
			t.Fatalf("overlap %d: %v", overlap, overlaps)
		}
	}
	t.Logf("100000 rounds: incidence=%v overlap=%v longest absence=%d", counts, overlaps, maxAge)
}

func TestDivaBeadRotationOldestAndReproduction(t *testing.T) {
	previous := divaBeadRotationState{Slots: []int{1, 17, 18, 19}, Same: 2}
	for i := range previous.Absence {
		previous.Absence[i] = 24
	}
	for seed := int64(0); seed < 1000; seed++ {
		a := nextDivaBeadRotation(previous, seed)
		b := nextDivaBeadRotation(previous, seed)
		if !reflect.DeepEqual(a, b) || divaBeadMask(a.Slots) == divaBeadMask(previous.Slots) {
			t.Fatalf("seed=%d draw not reproducible, or third identical quartet: %+v %+v", seed, a, b)
		}
		chosen := 0
		for _, age := range a.Absence {
			if age == 0 {
				chosen++
			}
		}
		if chosen < 1 || chosen > 2 {
			t.Fatal("overdue support not served", a)
		}
	}
}
