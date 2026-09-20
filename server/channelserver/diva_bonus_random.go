package channelserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"time"

	cfg "erupe-ce/config"
)

const divaRandomBonusSlotSeconds int64 = 3 * 3600

// divaRandomBonusRules is an opt-in custom schedule, not a recovered retail
// round-40 schedule. The whole prayer phase uses one event-seeded balanced
// deck; wall clock, request order, session, channel and process RNG do not.
func divaRandomBonusRules(event DivaEvent) ([]cfg.DivaBonusTarget, error) {
	start := int64(event.StartTime)
	end := start + divaPhaseDuration
	if event.ID == 0 || end > 0xffffffff {
		return nil, fmt.Errorf("invalid Diva event or overflowing bonus timestamps")
	}
	monsters := make([]uint8, len(divaSongMonsterPoints))
	for i, monster := range divaSongMonsterPoints {
		monsters[i] = monster.MID
	}
	local := time.Unix(start, 0).In(time.FixedZone("UTC+9", 9*3600))
	firstSlot := time.Date(local.Year(), local.Month(), local.Day(), local.Hour()/3*3, 0, 0, 0, local.Location()).Unix()
	slotCount := int((end - firstSlot + divaRandomBonusSlotSeconds - 1) / divaRandomBonusSlotSeconds)
	assignments, err := divaBalancedBonusAssignments(event, slotCount, monsters)
	if err != nil {
		return nil, err
	}
	rules := make([]cfg.DivaBonusTarget, 0, 4*slotCount)
	for i, selected := range assignments {
		slot := firstSlot + int64(i)*divaRandomBonusSlotSeconds
		for color, monster := range selected {
			rules = append(rules, cfg.DivaBonusTarget{
				Color: color + 1, TargetType: "monster", TargetID: int64(monster),
				StartOffsetSeconds: max(slot, start) - start,
				EndOffsetSeconds:   min(slot+divaRandomBonusSlotSeconds, end) - start,
				MultiplierPercent:  200,
			})
		}
	}
	if err := cfg.ValidateDivaBonusTargets(rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// SHA-256 defines one stable permutation, independent of catalog ordering.
// Four cursors start at floor(color*N/4), then advance one position per slot.
// Each color therefore visits every monster before repeating. The evenly
// spaced cursors also make total appearances floor(4*slots/N) or ceil(4*slots/N)
// for every monster; per-color appearances are floor(slots/N) or ceil(slots/N).
// With N>=8, adjacent slots share no monsters. For N=4..7, their overlap is the
// unavoidable minimum 8-N. Fewer than four distinct monsters cannot satisfy the
// same-slot uniqueness rule and are rejected instead of silently duplicating.
func divaBalancedBonusAssignments(event DivaEvent, slots int, monsters []uint8) ([][4]uint8, error) {
	if slots < 0 {
		return nil, fmt.Errorf("invalid negative Diva bonus slot count")
	}
	type candidate struct {
		id   uint8
		hash [sha256.Size]byte
	}
	// Version 2 intentionally replaces independent per-slot draws. Keep this
	// layout and the catalog stable during an event to avoid reshuffling clients.
	var seed [10]byte
	seed[0] = 2
	binary.BigEndian.PutUint32(seed[1:5], event.ID)
	binary.BigEndian.PutUint32(seed[5:9], event.StartTime)
	candidates := make([]candidate, 0, len(monsters))
	seen := make(map[uint8]bool, len(monsters))
	for _, id := range monsters {
		if seen[id] {
			continue
		}
		seen[id] = true
		seed[9] = id
		candidates = append(candidates, candidate{id, sha256.Sum256(seed[:])})
	}
	n := len(candidates)
	if n < 4 {
		return nil, fmt.Errorf("DivaBonusRandom needs at least four distinct song-point monsters")
	}
	sort.Slice(candidates, func(i, j int) bool {
		order := bytes.Compare(candidates[i].hash[:], candidates[j].hash[:])
		return order < 0 || (order == 0 && candidates[i].id < candidates[j].id)
	})
	assignments := make([][4]uint8, slots)
	for slot := range assignments {
		for color := range assignments[slot] {
			assignments[slot][color] = candidates[(slot+color*n/4)%n].id
		}
	}
	return assignments, nil
}
