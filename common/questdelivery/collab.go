package questdelivery

// Common quest publication and NPC presentation remain separate. Neither
// publication mode grants account authority, items or progression.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

const maxCommonCollabLocations = 65535

// CollabDefinition identifies the exact existing automatic provider inventory.
// Scope is presentation metadata, never proof of supply or native eligibility.
type CollabDefinition struct {
	QuestID int
	Scope   string
}

var commonCollabDefinitions = [...]CollabDefinition{
	{40211, "evangelion"}, {40212, "evangelion"}, {40213, "evangelion"}, {40214, "evangelion"},
	{40215, "kaiji"}, {40217, "higanjima"},
	{40221, "nier"}, {40223, "nier"}, {40224, "nier"},
	{40225, "nier"}, {40226, "nier"}, {40227, "nier"}, {40239, "pso2"},
}

func CommonCollabDefinitions() []CollabDefinition {
	return append([]CollabDefinition(nil), commonCollabDefinitions[:]...)
}

func commonCollabID(id int) bool {
	for _, definition := range commonCollabDefinitions {
		if definition.QuestID == id {
			return true
		}
	}
	return false
}

func collabTuneID(scope string) (uint16, bool) {
	switch scope {
	case "kaiji":
		return 1106, true
	case "higanjima":
		return 1144, true
	case "nier":
		return 1153, true
	case "evangelion":
		return 1156, true // Custom contract; not a verified original NPC flag.
	case "pso2":
		return 1130, true // Separate custom client contract.
	default:
		return 0, false
	}
}

type collabPresentationJSON struct {
	Event    string `json:"event"`
	Schedule string `json:"schedule"`
}

// The custom field preserves absent versus explicit[] and rejects null/repeated
// declarations. Other manifest fields retain their existing Parse behavior.
type collabPresentationJSONField struct {
	present bool
	rules   []collabPresentationJSON
}

func (field *collabPresentationJSONField) UnmarshalJSON(data []byte) error {
	if field.present {
		return errors.New("collab_presentations must not be repeated")
	}
	field.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("collab_presentations must be an array, not null")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&field.rules); err != nil {
		return fmt.Errorf("decode collab_presentations: %w", err)
	}
	if len(field.rules) > maxSchedules {
		return fmt.Errorf("collab_presentations exceeds %d rules", maxSchedules)
	}
	return nil
}

type collabPresentation struct {
	event      string
	scheduleID string
	schedule   schedule
}

type collabPresentationModeJSONField struct {
	present bool
	mode    string
}

func (field *collabPresentationModeJSONField) UnmarshalJSON(data []byte) error {
	if field.present {
		return errors.New("collab_presentation_mode must not be repeated")
	}
	field.present = true
	if err := json.Unmarshal(data, &field.mode); err != nil {
		return fmt.Errorf("decode collab_presentation_mode: %w", err)
	}
	if field.mode != "scheduled" && field.mode != "configured_world" {
		return errors.New("collab_presentation_mode must be scheduled or configured_world")
	}
	return nil
}

func (c *Catalog) compileCollabPresentations(field collabPresentationJSONField, mode collabPresentationModeJSONField) error {
	configuredWorld := mode.mode == "configured_world"
	if configuredWorld && field.present {
		return errors.New("configured_world cannot also declare collab_presentations")
	}
	if !configuredWorld && !field.present {
		return nil
	}
	for _, definition := range commonCollabDefinitions {
		if _, exists := c.quests[definition.QuestID]; !exists {
			return fmt.Errorf("collab conversion requires explicit managed quest %d", definition.QuestID)
		}
	}
	if configuredWorld {
		c.collabDeclared, c.collabConfiguredWorld = true, true
		return nil
	}
	plan := make([]collabPresentation, 0, len(field.rules))
	for _, rule := range field.rules {
		if _, known := collabTuneID(rule.Event); !known {
			return fmt.Errorf("unknown collab presentation event %q", rule.Event)
		}
		s, exists := c.schedules[rule.Schedule]
		if !exists {
			return fmt.Errorf("collab presentation references missing schedule %q", rule.Schedule)
		}
		// First candidate intentionally rejects periodic presentation instead of
		// asserting no overlap after a sampled year. Quest periodic schedules
		// are unchanged. Exact infinite/finite phase overlap is future work.
		if s.mode == periodic {
			return fmt.Errorf("periodic collab presentation %q is unsupported by this bounded candidate", rule.Schedule)
		}
		plan = append(plan, collabPresentation{rule.Event, rule.Schedule, s})
	}
	if len(plan) > 1 {
		for _, rule := range plan {
			if rule.schedule.mode == always {
				return errors.New("an always collab presentation must be the only presentation rule")
			}
		}
	}
	sort.Slice(plan, func(i, j int) bool {
		if !plan[i].schedule.start.Equal(plan[j].schedule.start) {
			return plan[i].schedule.start.Before(plan[j].schedule.start)
		}
		return plan[i].scheduleID < plan[j].scheduleID
	})
	for i := 1; i < len(plan); i++ {
		// once windows have exclusive ends. Adjacent is valid, equal/overlap
		// is rejected even when both rules name the same event.
		if plan[i].schedule.start.Before(plan[i-1].schedule.end) {
			return fmt.Errorf("collab presentations %q and %q overlap", plan[i-1].scheduleID, plan[i].scheduleID)
		}
	}
	c.collabDeclared = true
	c.collabPlan = plan
	return nil
}

func (c *Catalog) CommonCollabDeclared() bool { return c != nil && c.collabDeclared }

// UsesConfiguredWorldCollabPresentation leaves NPC tune selection to the
// existing world/session settings. The common quest roster remains independent.
func (c *Catalog) UsesConfiguredWorldCollabPresentation() bool {
	return c != nil && c.collabDeclared && c.collabConfiguredWorld
}

// AllowsCommonCollab never inherits Allows' unmanaged legacy policy. Callers
// must route here only for an explicitly declared converted cohort; absence of
// that declaration must not change existing pre-cutover operating behavior.
func (c *Catalog) AllowsCommonCollab(id int, world string, channel int, at time.Time) bool {
	if !c.CommonCollabDeclared() || !commonCollabID(id) {
		return false
	}
	managed, allowed := c.Allows(id, world, channel, at)
	return managed && allowed
}

// PublicationLocation is authoritative existing enabled server configuration
// supplied by the future integration. It creates no new world or channel.
type PublicationLocation struct {
	World   string
	Channel int
}

// CommonCollabCohort is immutable. Capability inputs mean static content/tune
// contracts only: no client handshake, hashes, login or access authorization.
// This first phase does not include the future DB source projection/provider.
type CommonCollabCohort struct {
	catalog   *Catalog
	locations []PublicationLocation
}

// BuildCommonCollabCohort checks common coverage against the actual finite set
// of configured locations. Disabled13 remain managed and are never published.
// No catalog, flags, schedules or configuration are changed by this function.
func BuildCommonCollabCohort(c *Catalog, locations []PublicationLocation, presentationCapabilities []string) (*CommonCollabCohort, error) {
	if !c.CommonCollabDeclared() {
		return nil, errors.New("common collab conversion is not explicitly declared")
	}
	if len(locations) == 0 || len(locations) > maxCommonCollabLocations {
		return nil, fmt.Errorf("common collab requires 1..%d existing locations", maxCommonCollabLocations)
	}
	configured := make(map[string]map[int]bool)
	for _, location := range locations {
		if !validID(location.World) || location.Channel < 1 || location.Channel > maxQuestID {
			return nil, fmt.Errorf("invalid existing collab location %+v", location)
		}
		channels := configured[location.World]
		if channels == nil {
			channels = make(map[int]bool)
			configured[location.World] = channels
		}
		if channels[location.Channel] {
			return nil, fmt.Errorf("duplicate existing collab location %+v", location)
		}
		channels[location.Channel] = true
	}
	for _, definition := range commonCollabDefinitions {
		q, exists := c.quests[definition.QuestID]
		if !exists { // Defensive invariant even if a future alternate parser appears.
			return nil, fmt.Errorf("missing common collab quest %d", definition.QuestID)
		}
		if q.disabled {
			continue
		}
		covered := make(map[PublicationLocation]bool, len(locations))
		for _, profileID := range q.profiles {
			profile := c.profiles[profileID]
			for _, world := range profile.Worlds {
				channels, configuredWorld := configured[world]
				if !configuredWorld {
					return nil, fmt.Errorf("collab quest %d references unconfigured world %q", definition.QuestID, world)
				}
				if len(profile.Channels) == 0 {
					for channel := range channels {
						covered[PublicationLocation{world, channel}] = true
					}
				} else {
					for _, channel := range profile.Channels {
						if !channels[channel] {
							return nil, fmt.Errorf("collab quest %d references unconfigured channel %q/%d", definition.QuestID, world, channel)
						}
						covered[PublicationLocation{world, channel}] = true
					}
				}
			}
		}
		if len(covered) != len(locations) {
			return nil, fmt.Errorf("collab quest %d is not common across all existing locations", definition.QuestID)
		}
	}
	capabilities := make(map[string]bool, len(presentationCapabilities))
	for _, scope := range presentationCapabilities {
		if _, known := collabTuneID(scope); !known || capabilities[scope] {
			return nil, fmt.Errorf("invalid or duplicate collab content capability %q", scope)
		}
		capabilities[scope] = true
	}
	for _, rule := range c.collabPlan {
		if !capabilities[rule.event] {
			return nil, fmt.Errorf("collab presentation %q lacks a static content capability", rule.event)
		}
	}
	return &CommonCollabCohort{catalog: c, locations: append([]PublicationLocation(nil), locations...)}, nil
}

func (cohort *CommonCollabCohort) Locations() []PublicationLocation {
	if cohort == nil {
		return nil
	}
	return append([]PublicationLocation(nil), cohort.locations...)
}

type CollabTuneValue struct {
	ID    uint16
	Value uint16
}

// CollabView owns one semantic point-in-time projection. Accessors clone slices
// so a paged consumer can retain it without a later clock/config mutation.
// ON pairs do not prove prior-NPC OFF/reset or instantaneous town refresh.
type CollabView struct {
	version      string
	evaluatedAt  time.Time
	questIDs     []int
	presentation string
	tunes        []CollabTuneValue
}

func (view CollabView) CatalogVersion() string { return view.version }
func (view CollabView) EvaluatedAt() time.Time { return view.evaluatedAt }
func (view CollabView) Presentation() string   { return view.presentation }
func (view CollabView) QuestIDs() []int        { return append([]int(nil), view.questIDs...) }
func (view CollabView) TuneValues() []CollabTuneValue {
	return append([]CollabTuneValue(nil), view.tunes...)
}

func (cohort *CommonCollabCohort) At(at time.Time) CollabView {
	view := CollabView{evaluatedAt: at}
	if cohort == nil || cohort.catalog == nil || len(cohort.locations) == 0 {
		return view
	}
	view.version = cohort.catalog.Version()
	location := cohort.locations[0] // All locations' structural coverage was proven.
	for _, definition := range commonCollabDefinitions {
		if cohort.catalog.AllowsCommonCollab(definition.QuestID, location.World, location.Channel, at) {
			view.questIDs = append(view.questIDs, definition.QuestID)
		}
	}
	for _, rule := range cohort.catalog.collabPlan {
		if rule.schedule.active(at) {
			view.presentation = rule.event
			id, _ := collabTuneID(rule.event) // Validated; no fallback value.
			view.tunes = []CollabTuneValue{{ID: id, Value: 1}}
			break
		}
	}
	return view
}

// PublicDocument delegates to the exact same immutable publication Catalog.
// It exposes no NPC delivery assertion, account data or capability inputs.
func (cohort *CommonCollabCohort) PublicDocument(from, until time.Time) (ScheduleDocument, error) {
	if cohort == nil || cohort.catalog == nil {
		return ScheduleDocument{}, errors.New("common collab cohort is nil")
	}
	return cohort.catalog.PublicDocument(from, until)
}
