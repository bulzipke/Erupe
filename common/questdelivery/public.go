package questdelivery

import (
	"errors"
	"time"
)

const MaxPublicDocumentBytes = 16 << 20

var ErrPublicDocumentTooLarge = errors.New("public quest schedule exceeds the 16 MiB encoded size budget")

// ScheduleDocument contains public distribution windows only. A window does
// not assert that a hunter owns the required ticket, gear, or campaign right.
type ScheduleDocument struct {
	PublicationScope             string                  `json:"publication_scope,omitempty"`
	CommonPublication            *CommonPublicationProof `json:"common_publication,omitempty"`
	Version                      string                  `json:"version"`
	Coverage                     string                  `json:"coverage"`
	Timezone                     string                  `json:"timezone"`
	From                         time.Time               `json:"from"`
	Until                        time.Time               `json:"until"`
	OriginalEntryConditionsApply bool                    `json:"original_entry_conditions_apply"`
	Occurrences                  []Occurrence            `json:"occurrences"`
}

var kst = time.FixedZone("KST", 9*60*60)

// PublicDocument is shared by the HTTP endpoint and offline export command.
func (c *Catalog) PublicDocument(from, until time.Time) (ScheduleDocument, error) {
	return c.publicDocumentWithMetadataBudget(from, until, 0)
}

func (c *Catalog) publicDocumentWithMetadataBudget(from, until time.Time, metadataCost int64) (ScheduleDocument, error) {
	if metadataCost < 0 || metadataCost > MaxPublicDocumentBytes-1024 {
		return ScheduleDocument{}, ErrPublicDocumentTooLarge
	}
	occurrences, err := c.Windows(from, until)
	if err != nil {
		return ScheduleDocument{}, err
	}
	// Windows bounds rows and slice copies; also bound repeated string content
	// before json.Marshal allocates the complete response. Six bytes per UTF-8
	// byte safely covers JSON escaping, with structural overhead per row.
	remaining := int64(MaxPublicDocumentBytes-1024) - metadataCost
	for _, occurrence := range occurrences {
		cost := int64(512 + 6*(len(occurrence.Title)+len(occurrence.ScheduleID)))
		for _, requirement := range occurrence.Requirements {
			cost += int64(16 + 6*len(requirement))
		}
		for _, profile := range occurrence.Profiles {
			cost += int64(128 + 6*len(profile.ID) + 24*len(profile.Channels))
			for _, world := range profile.Worlds {
				cost += int64(16 + 6*len(world))
			}
		}
		remaining -= cost
		if remaining < 0 {
			return ScheduleDocument{}, ErrPublicDocumentTooLarge
		}
	}
	for i := range occurrences {
		occurrences[i].StartsAt = occurrences[i].StartsAt.In(kst)
		occurrences[i].EndsAt = occurrences[i].EndsAt.In(kst)
	}
	return ScheduleDocument{
		Version: c.Version(), Coverage: "managed_quests_only", Timezone: "Asia/Seoul", From: from.In(kst), Until: until.In(kst),
		OriginalEntryConditionsApply: true, Occurrences: occurrences,
	}, nil
}
