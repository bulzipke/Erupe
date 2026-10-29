package channelserver

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"erupe-ce/common/mhfitem"
)

const (
	pallonePresentReach         uint32 = 3301
	pallonePresentBreak         uint32 = 3302
	pallonePresentConquer       uint32 = 3303
	pallonePresentPersonalTotal uint32 = 3201
	pallonePresentPersonalRoute uint32 = 3202
	pallonePresentGuildTotal    uint32 = 3203
	pallonePresentGuildRoute    uint32 = 3204
	palloneItem                 int32  = 7201
	palloneRP                   int32  = 7202
	palloneGP                   int32  = 7203
)

//go:embed data/pallone-26-rewards.json
var palloneRewardJSON []byte

type PalloneRewardRule struct {
	Key                 string `json:"key"`
	Kind                string `json:"kind"`
	Threshold           int32  `json:"threshold"`
	Star                int    `json:"star"`
	MinRank             int32  `json:"min_rank"`
	MaxRank             int32  `json:"max_rank"`
	MinimumContribution int32  `json:"minimum_contribution,omitempty"`
	ItemType            int32  `json:"item_type"`
	ItemID              uint16 `json:"item_id"`
	ItemName            string `json:"item_name,omitempty"` // Research metadata; the client supplies display names.
	Quantity            int32  `json:"quantity"`
	Evidence            string `json:"evidence"`
	Source              string `json:"source"`
}

type PalloneRewardCatalog struct {
	Version           string              `json:"version"`
	ReferenceRound    int                 `json:"reference_round"`
	Sources           map[string]string   `json:"sources"`
	OrbPointsPerStage int32               `json:"orb_points_per_stage"`
	RepeatAfter       int32               `json:"repeat_after"`
	RepeatStep        int32               `json:"repeat_step"`
	RepeatGP          int32               `json:"repeat_gp"`
	Rules             []PalloneRewardRule `json:"rules"`
	Notes             []string            `json:"notes"`
}

func loadPalloneRewardCatalog(data []byte) (PalloneRewardCatalog, error) {
	var c PalloneRewardCatalog
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	if c.Version == "" || c.ReferenceRound != 26 || c.OrbPointsPerStage < 1 || c.OrbPointsPerStage > 100 || c.RepeatAfter != 800000 || c.RepeatStep != 5000 || c.RepeatGP < 1 || len(c.Rules) == 0 || len(c.Rules) > 512 {
		return c, ErrPalloneInvalid
	}
	seen := make(map[string]bool)
	for _, r := range c.Rules {
		if r.Key == "" || seen[r.Key] || r.Quantity <= 0 || r.Quantity > 9999999 || r.Threshold < 0 || r.Star < -1 || r.Star > 10 || palloneRulePresentType(r.Kind) == 0 || r.Source == "" || c.Sources[r.Source] == "" ||
			(r.Evidence != "confirmed" && r.Evidence != "cross-round" && r.Evidence != "inferred") {
			return c, ErrPalloneInvalid
		}
		seen[r.Key] = true
		if r.ItemType == palloneItem {
			if _, ok := mhfitem.LookupItemMetadata(r.ItemID); !ok || r.Quantity > 99 {
				return c, fmt.Errorf("invalid Pallone item %s", r.Key)
			}
		} else if (r.ItemType != palloneGP && r.ItemType != palloneRP) || r.ItemID != 0 {
			return c, ErrPalloneInvalid
		}
		if r.MinRank < 0 || r.MaxRank < r.MinRank {
			return c, ErrPalloneInvalid
		}
	}
	return c, nil
}

func palloneRulePresentType(kind string) uint32 {
	switch kind {
	case "reach":
		return pallonePresentReach
	case "break":
		return pallonePresentBreak
	case "conquer":
		return pallonePresentConquer
	case "personal-total":
		return pallonePresentPersonalTotal
	case "personal-route":
		return pallonePresentPersonalRoute
	case "guild-total", "guild-achievement":
		return pallonePresentGuildTotal
	case "guild-route", "guild-contribution":
		return pallonePresentGuildRoute
	}
	return 0
}

type PalloneRewardOffer struct {
	ID          uint32    `db:"id"`
	EventID     int32     `db:"event_id"`
	CharID      uint32    `db:"character_id"`
	Key         string    `db:"reward_key"`
	PresentType uint32    `db:"present_type"`
	ItemType    int32     `db:"item_type"`
	ItemID      uint16    `db:"item_id"`
	Quantity    int32     `db:"quantity"`
	Threshold   int32     `db:"threshold"`
	RouteKey    int32     `db:"route_key"`
	Expires     time.Time `db:"expires_at"`
	Claimed     bool      `db:"claimed"`
}

type PalloneRewardProgress struct {
	Score            int64
	Cleared          map[int32]bool
	Routes           []PalloneRoute
	Ranks            []PalloneRewardRank
	GuildPoints      int64
	Contribution     int32
	ContributionRank int32
	RepeatThrough    int32
}

type PalloneRewardRank struct {
	Kind     int32 `db:"kind"`
	RouteKey int32 `db:"route_key"`
	Rank     int32 `db:"rank"`
}

// Achievement requires every installed route in that star; repeated clears
// never inflate breakthrough. An absent star cannot count as conquered.
func palloneConquered(p PalloneRewardProgress, star int) bool {
	count := 0
	for _, r := range p.Routes {
		if int(r.Star) == star {
			count++
			if !p.Cleared[r.Key] {
				return false
			}
		}
	}
	return count > 0
}

func palloneEligibleRewards(c PalloneRewardCatalog, p PalloneRewardProgress, current bool) []PalloneRewardOffer {
	var out []PalloneRewardOffer
	add := func(rule PalloneRewardRule, route int32) {
		key := rule.Key
		if route != 0 {
			key = fmt.Sprintf("%s:route:%d", key, route)
		}
		out = append(out, PalloneRewardOffer{Key: key, PresentType: palloneRulePresentType(rule.Kind), ItemType: rule.ItemType, ItemID: rule.ItemID, Quantity: rule.Quantity, Threshold: rule.Threshold, RouteKey: route})
	}
	for _, r := range c.Rules {
		switch r.Kind {
		case "reach":
			if current && p.Score >= int64(r.Threshold) {
				add(r, 0)
			}
		case "break":
			if current && len(p.Cleared) >= int(r.Threshold) {
				add(r, 0)
			}
		case "conquer":
			if current && palloneConquered(p, r.Star) {
				add(r, 0)
			}
		case "personal-total", "personal-route", "guild-total", "guild-route":
			if current {
				continue
			}
			kind := int32(1)
			switch r.Kind {
			case "personal-route":
				kind = 2
			case "guild-total":
				kind = 3
			case "guild-route":
				kind = 4
			}
			for _, rank := range p.Ranks {
				if rank.Kind != kind || rank.Rank < r.MinRank || rank.Rank > r.MaxRank {
					continue
				}
				if r.Star >= 0 && rank.RouteKey != 0 {
					found := false
					for _, route := range p.Routes {
						if route.Key == rank.RouteKey && int(route.Star) == r.Star {
							found = true
							break
						}
					}
					if !found {
						continue
					}
				}
				add(r, rank.RouteKey)
			}
		case "guild-achievement", "guild-contribution":
			if !current && p.GuildPoints >= int64(r.Threshold) && p.Contribution >= r.MinimumContribution {
				if r.Kind == "guild-achievement" || (p.ContributionRank >= r.MinRank && p.ContributionRank <= r.MaxRank) {
					add(r, 0)
				}
			}
		}
	}
	// Repeating GP is batched, preserving the exact number of earned steps.
	// Start after the highest already offered step, never after a mutable total.
	if current && p.Score > int64(c.RepeatAfter) {
		start := int64(p.RepeatThrough)
		if start < int64(c.RepeatAfter) {
			start = int64(c.RepeatAfter)
		}
		end := int64(c.RepeatAfter) + (p.Score-int64(c.RepeatAfter))/int64(c.RepeatStep)*int64(c.RepeatStep)
		for n := 0; start < end && n < 64; n++ {
			stop := start + 64*int64(c.RepeatStep)
			if stop > end {
				stop = end
			}
			out = append(out, PalloneRewardOffer{Key: fmt.Sprintf("repeat:%d:%d", start, stop), PresentType: pallonePresentReach, ItemType: palloneGP, Quantity: int32((stop-start)/int64(c.RepeatStep)) * c.RepeatGP, Threshold: int32(stop)})
			start = stop
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PresentType != out[j].PresentType {
			return out[i].PresentType < out[j].PresentType
		}
		if out[i].Threshold != out[j].Threshold {
			return out[i].Threshold < out[j].Threshold
		}
		return out[i].Key < out[j].Key
	})
	return out
}
