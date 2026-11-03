package channelserver

import (
	"math/bits"
	"math/rand"
)

// These are the public effects, not the client's internal effect indices.
// Weights blend original rounds 9..24 once and 25..40 twice. A floor of one
// keeps Striking available despite its absence from those 32 observed rounds.
var divaBeadSupport = [...]int{3, 4, 8, 9, 10, 11, 15, 20, 21, 22, 23, 24, 25}
var divaBeadSupportWeights = [...]float64{10, 15, 8, 1, 2, 4, 1, 2, 1, 1, 5, 1, 9}
var divaBeadCoreSizeWeights = [...]float64{0, 0, 20, 19, 9}
var divaBeadOverlapWeights = [...]float64{10, 18, 5, 2, 2}

type divaBeadRotationState struct {
	Slots   []int
	Absence [13]int
	Same    int
}

type divaBeadCandidate struct {
	ids         [4]int
	mask        uint32
	support     []int // indices into divaBeadSupport
	probability float64
}

var divaBeadCandidates = buildDivaBeadCandidates()

func buildDivaBeadCandidates() []divaBeadCandidate {
	var candidates []divaBeadCandidate
	for _, core := range divaBeadCoreSubsets {
		add := func(support ...int) {
			c := divaBeadCandidate{support: support, probability: core.probability}
			copy(c.ids[:], core.ids)
			for i, index := range support {
				c.ids[len(core.ids)+i] = divaBeadSupport[index]
			}
			for _, id := range c.ids {
				c.mask |= 1 << id
			}
			candidates = append(candidates, c)
		}
		switch len(core.ids) {
		case 4:
			add()
		case 3:
			for i := range divaBeadSupport {
				add(i)
			}
		case 2:
			for i := range divaBeadSupport {
				for j := i + 1; j < len(divaBeadSupport); j++ {
					add(i, j)
				}
			}
		}
	}
	return candidates
}

func divaBeadWeighted(rng *rand.Rand, weights []float64) int {
	total := 0.0
	last := 0
	for i, w := range weights {
		total += w
		if w > 0 {
			last = i
		}
	}
	needle := rng.Float64() * total
	for i, w := range weights {
		needle -= w
		if needle < 0 {
			return i
		}
	}
	return last
}

func divaBeadMask(ids []int) uint32 {
	var mask uint32
	for _, id := range ids {
		mask |= 1 << id
	}
	return mask
}

// Pure draw: no shared PRNG, clock, DB or mutable global state. A stored seed
// can reproduce a generated round. Slot shuffling uses a separate PRNG.
func nextDivaBeadRotation(previous divaBeadRotationState, seed int64) divaBeadRotationState {
	rng := rand.New(rand.NewSource(seed))
	slotRNG := rand.New(rand.NewSource(seed ^ 0x534c4f5453))
	var weights [13]float64
	total, squares, oldest := 0.0, 0.0, 0
	for i, base := range divaBeadSupportWeights {
		weights[i] = base
		if previous.Absence[i] >= 20 {
			weights[i] *= 4
		} else if previous.Absence[i] >= 12 {
			weights[i] *= 2
		}
		total += weights[i]
		squares += weights[i] * weights[i]
		oldest = max(oldest, previous.Absence[i])
	}
	z := [...]float64{1, total, (total*total - squares) / 2}
	coreSizes := divaBeadCoreSizeWeights
	if oldest >= 24 {
		coreSizes[4] = 0
	}
	coreSize := divaBeadWeighted(rng, coreSizes[:])
	previousMask := divaBeadMask(previous.Slots)
	var groups [5][]int
	var chances [5][]float64
	var overlaps [5]float64
	for i, c := range divaBeadCandidates {
		if 4-len(c.support) != coreSize || (previous.Same >= 2 && c.mask == previousMask) {
			continue
		}
		due := oldest < 24
		p := c.probability / z[len(c.support)]
		for _, index := range c.support {
			p *= weights[index]
			due = due || previous.Absence[index] == oldest
		}
		if !due {
			continue
		}
		overlap := bits.OnesCount32(c.mask & previousMask)
		groups[overlap] = append(groups[overlap], i)
		chances[overlap] = append(chances[overlap], p)
		overlaps[overlap] = divaBeadOverlapWeights[overlap]
	}
	overlap := divaBeadWeighted(rng, overlaps[:])
	c := divaBeadCandidates[groups[overlap][divaBeadWeighted(rng, chances[overlap])]]
	result := divaBeadRotationState{Slots: make([]int, 4), Same: 1}
	if c.mask == previousMask {
		result.Same = previous.Same + 1
	}
	var retained uint32
	for i, id := range previous.Slots {
		if c.mask&(1<<id) != 0 {
			result.Slots[i] = id
			retained |= 1 << id
		}
	}
	var added []int
	// Sort before shuffling so candidate construction order cannot affect slots.
	for id := 1; id <= 25; id++ {
		if c.mask&(1<<id) != 0 && retained&(1<<id) == 0 {
			added = append(added, id)
		}
	}
	slotRNG.Shuffle(len(added), func(i, j int) { added[i], added[j] = added[j], added[i] })
	for i, id := range result.Slots {
		if id == 0 {
			result.Slots[i] = added[len(added)-1]
			added = added[:len(added)-1]
		}
	}
	for i, id := range divaBeadSupport {
		if c.mask&(1<<id) == 0 {
			result.Absence[i] = previous.Absence[i] + 1
		}
	}
	return result
}
