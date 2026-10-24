package questdelivery

import (
	"fmt"
	"math/bits"
	"sort"
	"strings"
	"time"
)

const CommonPublicationScope = "all_existing_worlds_and_channels"

// The inventory is static managed publication metadata, including IDs outside
// the requested time range. It is not account authority or NPC supply proof.
type CommonPublicationQuest struct {
	QuestID  int      `json:"quest_id"`
	Profiles []string `json:"profiles"`
}
type CommonPublicationLocation struct {
	World   string `json:"world"`
	Channel int    `json:"channel"`
}

type CommonPublicationProof struct {
	Locations []CommonPublicationLocation `json:"locations"`
	Profiles  []Profile                   `json:"profiles"`
	Inventory []CommonPublicationQuest    `json:"inventory"`
}

// CommonPublicationCohort proves full location coverage for every enabled ID.
// It is independent of Collab13 and keeps the same immutable Catalog identity.
type CommonPublicationCohort struct {
	catalog      *Catalog
	proof        CommonPublicationProof
	metadataCost int64
}

func BuildCommonPublicationCohort(c *Catalog, configured []PublicationLocation) (*CommonPublicationCohort, error) {
	if c == nil || len(configured) == 0 || len(configured) > 65535 {
		return nil, fmt.Errorf("common publication requires an immutable catalog and 1..65535 configured enabled locations")
	}
	locations := make(map[PublicationLocation]int, len(configured))
	worlds := make(map[string][]int)
	cost := int64(512)
	charge := func(n int64) error {
		cost += n
		if cost > MaxPublicDocumentBytes-1024 {
			return ErrPublicDocumentTooLarge
		}
		return nil
	}
	for _, location := range configured {
		if location.World == "" || strings.TrimSpace(location.World) != location.World || location.Channel < 1 || location.Channel > 65535 {
			return nil, fmt.Errorf("invalid common publication configured location")
		}
		if _, duplicate := locations[location]; duplicate {
			return nil, fmt.Errorf("duplicate configured location")
		}
		if err := charge(int64(64 + 6*len(location.World))); err != nil {
			return nil, err
		}
		locations[location] = len(locations)
		worlds[location.World] = append(worlds[location.World], location.Channel)
	}
	// Preflight all new JSON metadata before copying nested slices or allocating
	// per-profile bitsets. Six bytes per UTF-8 byte covers worst JSON escaping.
	for _, id := range c.profileIDs {
		p := c.profiles[id]
		if err := charge(int64(128 + 6*len(p.ID) + 24*len(p.Channels))); err != nil {
			return nil, err
		}
		for _, world := range p.Worlds {
			if err := charge(int64(16 + 6*len(world))); err != nil {
				return nil, err
			}
		}
	}
	for _, id := range c.questIDs {
		q := c.quests[id]
		if q.disabled {
			continue
		}
		if err := charge(64); err != nil {
			return nil, err
		}
		seen := make(map[string]bool, len(q.profiles))
		for _, profile := range q.profiles {
			if _, exists := c.profiles[profile]; !exists || seen[profile] {
				return nil, fmt.Errorf("quest %d has missing/duplicate profile reference", id)
			}
			seen[profile] = true
			if err := charge(int64(16 + 6*len(profile))); err != nil {
				return nil, err
			}
		}
	}
	// A large location/profile cross-product is rejected by the same bounded
	// metadata budget before allocating it; never expand World*Channel blindly.
	wordCount := (len(locations) + 63) / 64
	if int64(len(c.profileIDs))*int64(wordCount)*8 > MaxPublicDocumentBytes {
		return nil, ErrPublicDocumentTooLarge
	}
	profileBits := make(map[string][]uint64, len(c.profileIDs))
	profileFull := make(map[string]bool, len(c.profileIDs))
	for _, id := range c.profileIDs {
		p := c.profiles[id]
		if err := validateProfile(p); err != nil {
			return nil, err
		}
		if len(p.Channels) != 0 && len(p.Worlds) > len(locations)/len(p.Channels) {
			return nil, fmt.Errorf("profile %q references unavailable locations", id)
		}
		mask := make([]uint64, wordCount)
		for _, world := range p.Worlds {
			channels, exists := worlds[world]
			if !exists {
				return nil, fmt.Errorf("profile %q references unknown or disabled world %q", id, world)
			}
			if len(p.Channels) != 0 {
				channels = p.Channels
			}
			for _, channel := range channels {
				index, exists := locations[PublicationLocation{World: world, Channel: channel}]
				if !exists {
					return nil, fmt.Errorf("profile %q references unknown or disabled channel", id)
				}
				mask[index/64] |= uint64(1) << uint(index%64)
			}
		}
		covered := 0
		for _, word := range mask {
			covered += bits.OnesCount64(word)
		}
		profileBits[id], profileFull[id] = mask, covered == len(locations)
	}
	proof := CommonPublicationProof{Locations: make([]CommonPublicationLocation, 0, len(configured)), Profiles: c.Profiles(), Inventory: []CommonPublicationQuest{}}
	for _, location := range configured {
		proof.Locations = append(proof.Locations, CommonPublicationLocation{World: location.World, Channel: location.Channel})
	}
	sort.Slice(proof.Locations, func(i, j int) bool {
		if proof.Locations[i].World == proof.Locations[j].World {
			return proof.Locations[i].Channel < proof.Locations[j].Channel
		}
		return proof.Locations[i].World < proof.Locations[j].World
	})
	scratch := make([]uint64, wordCount)
	for _, id := range c.questIDs {
		q := c.quests[id]
		if q.disabled {
			continue
		}
		for i := range scratch {
			scratch[i] = 0
		}
		full := false
		for _, pid := range q.profiles {
			mask, exists := profileBits[pid]
			if !exists {
				return nil, fmt.Errorf("quest %d references missing profile %q", id, pid)
			}
			if profileFull[pid] {
				full = true
				break
			}
			for i, word := range mask {
				scratch[i] |= word
			}
		}
		if !full {
			covered := 0
			for _, word := range scratch {
				covered += bits.OnesCount64(word)
			}
			if covered != len(locations) {
				return nil, fmt.Errorf("quest %d does not cover every configured enabled location", id)
			}
		}
		proof.Inventory = append(proof.Inventory, CommonPublicationQuest{QuestID: id, Profiles: append([]string(nil), q.profiles...)})
	}
	return &CommonPublicationCohort{catalog: c, proof: proof, metadataCost: cost}, nil
}

func (cohort *CommonPublicationCohort) BoundTo(c *Catalog) bool {
	return cohort != nil && c != nil && cohort.catalog == c
}
func (cohort *CommonPublicationCohort) PermitsLocation(world string, channel int) bool {
	if cohort == nil {
		return false
	}
	for _, location := range cohort.proof.Locations {
		if location.World == world && location.Channel == channel {
			return true
		}
	}
	return false
}
func (cohort *CommonPublicationCohort) PublicDocument(from, until time.Time) (ScheduleDocument, error) {
	if cohort == nil || cohort.catalog == nil {
		return ScheduleDocument{}, fmt.Errorf("common publication cohort is not prepared")
	}
	document, err := cohort.catalog.publicDocumentWithMetadataBudget(from, until, cohort.metadataCost)
	if err != nil {
		return ScheduleDocument{}, err
	}
	proof := CommonPublicationProof{Locations: append([]CommonPublicationLocation(nil), cohort.proof.Locations...), Profiles: make([]Profile, len(cohort.proof.Profiles)), Inventory: make([]CommonPublicationQuest, len(cohort.proof.Inventory))}
	for i, p := range cohort.proof.Profiles {
		proof.Profiles[i] = cloneProfile(p)
	}
	for i, q := range cohort.proof.Inventory {
		proof.Inventory[i] = CommonPublicationQuest{QuestID: q.QuestID, Profiles: append([]string(nil), q.Profiles...)}
	}
	document.PublicationScope, document.CommonPublication = CommonPublicationScope, &proof
	return document, nil
}
