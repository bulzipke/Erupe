package config

import "fmt"

// DivaBonusTarget describes one song-phase bonus window, relative to the
// event's first phase start (seconds). EndOffsetSeconds is exclusive.
// No round-40 schedule is assumed when this list is empty.
type DivaBonusTarget struct {
	Color              int
	TargetType         string // See NativeKind and docs/diva-bonus-targets.md.
	TargetID           int64
	StartOffsetSeconds int64
	EndOffsetSeconds   int64
	MultiplierPercent  int // 200 = total x2, not +200%.
}

const (
	MaxDivaBonusTargets   = 320 // Native storage: 8 days * 4 colors * 10 entries.
	DivaBonusPhaseSeconds = 601200
	// An operator safety limit, not a claimed retail maximum.
	MaxDivaBonusPercent = 10000
)

// NativeKind is shared by validation and the wire builder; unknown types must
// never silently fall back to a monster condition.
func (target DivaBonusTarget) NativeKind() (uint8, bool) {
	switch target.TargetType {
	case "rank":
		return 1, true
	case "field":
		return 2, true
	case "time_of_day":
		return 3, true
	case "monster_family":
		return 4, true
	case "monster":
		return 5, true
	case "quest":
		return 6, true
	case "monster_class":
		return 7, true
	default:
		return 0, false
	}
}

func ValidateDivaBonusTargets(targets []DivaBonusTarget) error {
	if len(targets) > MaxDivaBonusTargets {
		return fmt.Errorf("DivaBonusTargets: at most %d entries are supported", MaxDivaBonusTargets)
	}
	for i, target := range targets {
		invalid := func(reason string) error {
			return fmt.Errorf("GameplayOptions.DivaBonusTargets[%d]: %s", i, reason)
		}
		if target.Color < 1 || target.Color > 4 {
			return invalid("Color must be 1..4")
		}
		switch target.TargetType {
		case "rank":
			if target.TargetID < 1 || target.TargetID > 3 {
				return invalid("rank TargetID must be 1 (HR2-4), 2 (HR5-7), or 3 (GR1+)")
			}
		case "field":
			aliases := map[int64]int64{14: 13, 15: 13, 17: 5, 19: 8, 22: 9, 23: 10, 29: 28, 30: 28, 32: 13, 36: 35, 43: 8}
			if canonical, ok := aliases[target.TargetID]; ok {
				return invalid(fmt.Sprintf("use canonical field TargetID %d instead of %d", canonical, target.TargetID))
			}
			if !isDivaBonusField(target.TargetID) {
				return invalid("field TargetID must be a named canonical ZZ destination ID; see docs/diva-bonus-targets.md")
			}
		case "time_of_day":
			if target.TargetID < 0 || target.TargetID > 1 {
				return invalid("time_of_day TargetID must be 0 (day) or 1 (night)")
			}
		case "monster_family":
			if target.TargetID < 1 || target.TargetID > 9 {
				return invalid("monster_family TargetID must be 1..9; zero is an unnamed native category")
			}
		case "monster_class":
			if target.TargetID >= 4 && target.TargetID <= 6 {
				return invalid("use canonical monster_class TargetID 3; IDs 4..6 are not separate Zenith star levels")
			}
			if target.TargetID < 0 || target.TargetID > 3 {
				return invalid("monster_class TargetID must be 0 (Resshu), 1 (Shishu), 2 (Sen'yu), or 3 (Zenith)")
			}
		case "monster":
			if target.TargetID < 1 || target.TargetID > 176 {
				return invalid("monster TargetID must be 1..176")
			}
			aliases := map[int64]int64{0x3c: 0x36, 0x9b: 0x93, 0xa3: 0x9f, 0xac: 0xaa, 0xae: 0x7a}
			if canonical, ok := aliases[target.TargetID]; ok {
				return invalid(fmt.Sprintf("use canonical monster TargetID %d instead of %d", canonical, target.TargetID))
			}
		case "quest":
			if target.TargetID < 1 || target.TargetID > 65535 {
				return invalid("quest TargetID must be 1..65535 (the client compares uint16 quest IDs)")
			}
		default:
			return invalid("unsupported TargetType")
		}
		if target.StartOffsetSeconds < 0 || target.StartOffsetSeconds >= target.EndOffsetSeconds ||
			target.EndOffsetSeconds > DivaBonusPhaseSeconds {
			return invalid("window must satisfy 0 <= StartOffsetSeconds < EndOffsetSeconds <= 601200")
		}
		if target.MultiplierPercent < 100 || target.MultiplierPercent > MaxDivaBonusPercent {
			return invalid(fmt.Sprintf("MultiplierPercent must be 100..%d (200 means x2)", MaxDivaBonusPercent))
		}
		// Different native point paths either replace or add matching bonuses.
		// Disallow ambiguity instead of depending on either behavior.
		for j := 0; j < i; j++ {
			other := targets[j]
			if target.Color == other.Color && target.StartOffsetSeconds < other.EndOffsetSeconds &&
				other.StartOffsetSeconds < target.EndOffsetSeconds {
				return invalid(fmt.Sprintf("window overlaps entry %d for the same Color", j))
			}
		}
	}
	return nil
}

// FUN_10b4c510 normalizes destination aliases before matching. The UI looks up
// the first matching value in DAT_118649c0. Exclude dummy/blank rows and aliases
// which would be displayed but could never equal the normalized destination.
func isDivaBonusField(id int64) bool {
	switch id {
	case 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13,
		16, 18, 20, 21, 24, 25, 26, 27, 28, 31, 33, 34, 35,
		37, 38, 39, 40, 41, 42, 53, 54, 55, 56, 57, 58, 59,
		60, 61, 62, 63, 64, 65, 66, 67, 68, 69:
		return true
	default:
		return false
	}
}
