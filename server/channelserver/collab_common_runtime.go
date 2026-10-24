package channelserver

// Common collaboration listing keeps original authority and NPC settings.
// NPC tunes are content settings, not supply/client support evidence and never
// a login/connection handshake.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"erupe-ce/common/questdelivery"
	cfg "erupe-ce/config"
)

type CommonCollabEventSource interface {
	GetEventQuests() ([]EventQuest, error)
}

// CommonCollabPreflightHold is a startup/content preparation error, not a
// player admission gate. No row is rewritten, deleted or silently substituted.
type CommonCollabPreflightHold struct {
	Kind    string
	QuestID int
	Detail  string
	Cause   error
}

func (hold *CommonCollabPreflightHold) Error() string {
	return fmt.Sprintf("common collab preflight %s (quest %d): %s", hold.Kind, hold.QuestID, hold.Detail)
}
func (hold *CommonCollabPreflightHold) Unwrap() error { return hold.Cause }

func commonCollabHold(kind string, id int, detail string, cause error) error {
	return &CommonCollabPreflightHold{Kind: kind, QuestID: id, Detail: detail, Cause: cause}
}

type commonCollabSourceRow struct {
	Quest          EventQuest
	Builtin        bool
	CanonicalScope string
}

type CommonCollabRuntimeCohort struct {
	catalog    *questdelivery.Catalog
	semantic   *questdelivery.CommonCollabCohort
	sources    []commonCollabSourceRow
	sourceHash string
}

func (s *Session) questDeliveryEnumerationTime() time.Time {
	if s.server.questDeliveryClock != nil {
		return s.server.questDeliveryClock()
	}
	return time.Now()
}

// PrepareCommonCollabRuntimeFromConfig is called once before ANY service Start.
// The existing flags only permit a scheduled ON tuple. They do not prove that
// an original NPC exists, that a patched client supports it or that gifts save.
func PrepareCommonCollabRuntimeFromConfig(config *cfg.Config, source CommonCollabEventSource) (*CommonCollabRuntimeCohort, error) {
	if config == nil || !config.QuestDeliveryCatalog.CommonCollabDeclared() {
		return nil, nil
	}
	if !config.QuestDelivery.Enabled {
		return nil, commonCollabHold("inactive_configuration", 0, "conversion declaration requires the existing quest delivery setting", nil)
	}
	locations := []questdelivery.PublicationLocation{}
	for _, world := range config.Entrance.Entries {
		for i, channel := range world.Channels {
			if channel.IsEnabled() {
				locations = append(locations, questdelivery.PublicationLocation{World: world.Name, Channel: i + 1})
			}
		}
	}
	permissions := []string{}
	options := config.GameplayOptions
	for _, setting := range []struct {
		scope   string
		allowed bool
	}{
		{collabKaiji, options.EnableKaijiEvent}, {collabHiganjima, options.EnableHiganjimaEvent},
		{collabNier, options.EnableNierEvent}, {collabEvangelion, options.EnableEvangelionEvent},
		{collabPSO2, options.EnablePSO2Event},
	} {
		if setting.allowed {
			permissions = append(permissions, setting.scope)
		}
	}
	return PrepareCommonCollabRuntime(config.QuestDeliveryCatalog, locations, permissions, source)
}

// This function performs one metadata read outside every gameplay/list lock.
// Nil/absent declaration does not activate anything and performs no query.
func PrepareCommonCollabRuntime(catalog *questdelivery.Catalog, locations []questdelivery.PublicationLocation, permissions []string, source CommonCollabEventSource) (*CommonCollabRuntimeCohort, error) {
	if !catalog.CommonCollabDeclared() {
		return nil, nil
	}
	semantic, err := questdelivery.BuildCommonCollabCohort(catalog, locations, permissions)
	if err != nil {
		return nil, commonCollabHold("presentation_or_coverage", 0, "invalid common content plan", err)
	}
	if source == nil {
		return nil, commonCollabHold("missing_repository", 0, "metadata repository is required", nil)
	}
	rows, err := source.GetEventQuests()
	if err != nil {
		// StartTime NULL/scan failure is not turned into an invented zero date.
		return nil, commonCollabHold("repository_read", 0, "metadata read failed; no builtin recovery", err)
	}
	return buildCommonCollabRuntime(catalog, semantic, rows)
}

// Private immutable index; avoid cloning the13-definition slice for every
// ordinary row in an expanded enumeration. No caller can mutate this map.
var commonCollabDefinitionIndex = func() map[int]questdelivery.CollabDefinition {
	index := make(map[int]questdelivery.CollabDefinition, 13)
	for _, definition := range questdelivery.CommonCollabDefinitions() {
		index[definition.QuestID] = definition
	}
	return index
}()

func commonCollabDefinition(id int) (questdelivery.CollabDefinition, bool) {
	definition, known := commonCollabDefinitionIndex[id]
	return definition, known
}

func buildCommonCollabRuntime(catalog *questdelivery.Catalog, semantic *questdelivery.CommonCollabCohort, rows []EventQuest) (*CommonCollabRuntimeCohort, error) {
	overrides := make(map[int]EventQuest)
	identities := make(map[uint32]int)
	for _, row := range rows {
		if definition, known := commonCollabDefinition(row.QuestID); known {
			if _, duplicate := overrides[row.QuestID]; duplicate {
				return nil, commonCollabHold("duplicate_override", row.QuestID, "multiple DB rows cannot be resolved silently", nil)
			}
			if row.ID == 0 {
				return nil, commonCollabHold("invalid_identity", row.QuestID, "DB row identity must be nonzero", nil)
			}
			if row.CollabScope != "" && row.CollabScope != definition.Scope {
				return nil, commonCollabHold("scope_conflict", row.QuestID, "explicit DB scope differs from canonical association", nil)
			}
			if err := validateCommonCollabLegacySchedule(row); err != nil {
				return nil, err
			}
			overrides[row.QuestID] = row // Value-copy. Empty scope is not overwritten.
		}
		if previous, exists := identities[row.ID]; exists {
			if _, known := commonCollabDefinition(previous); known {
				return nil, commonCollabHold("identity_collision", previous, "DB row identity is shared with another row", nil)
			}
			if _, known := commonCollabDefinition(row.QuestID); known {
				return nil, commonCollabHold("identity_collision", row.QuestID, "DB row identity is shared with another row", nil)
			}
		}
		identities[row.ID] = row.QuestID
	}
	sources := make([]commonCollabSourceRow, 0, 13)
	for _, definition := range questdelivery.CommonCollabDefinitions() {
		if row, exists := overrides[definition.QuestID]; exists {
			sources = append(sources, commonCollabSourceRow{Quest: row, CanonicalScope: definition.Scope})
			continue
		}
		found := false
		for _, builtin := range builtInCollabQuests {
			if builtin.questID == definition.QuestID && builtin.event == definition.Scope {
				sources = append(sources, commonCollabSourceRow{Quest: EventQuest{
					QuestID: builtin.questID, MaxPlayers: builtin.maxPlayers, QuestType: 18,
					Mark: 1, Flags: -1, CollabScope: builtin.event,
				}, Builtin: true, CanonicalScope: definition.Scope})
				found = true
				break
			}
		}
		if !found {
			return nil, commonCollabHold("builtin_contract", definition.QuestID, "canonical provider definition is missing or changed", nil)
		}
	}
	raw, err := json.Marshal(sources)
	if err != nil {
		return nil, commonCollabHold("source_projection", 0, "source projection cannot be represented", err)
	}
	hash := sha256.Sum256(raw)
	return &CommonCollabRuntimeCohort{catalog: catalog, semantic: semantic, sources: sources, sourceHash: hex.EncodeToString(hash[:])}, nil
}

func validateCommonCollabLegacySchedule(row EventQuest) error {
	// SQL COALESCE erases NULL for active/inactive days in the existing repo.
	// EventQuest alone cannot certify raw NULL provenance. The actual legacy
	// function returns true immediately for ActiveDays<=0, ignoring other dates.
	if row.ActiveDays < 0 || row.InactiveDays < 0 {
		return commonCollabHold("ambiguous_negative_schedule", row.QuestID, "negative metadata is held, not normalized or revived", nil)
	}
	if row.ActiveDays == 0 {
		return nil // Original always semantic, including one-sided ignored fields.
	}
	const day = int64(24 * 60 * 60)
	if row.StartTime.IsZero() || int64(row.ActiveDays) > math.MaxInt64/day || int64(row.InactiveDays) > math.MaxInt64/day-int64(row.ActiveDays) {
		return commonCollabHold("inactive_or_invalid_schedule", row.QuestID, "legacy source is inactive/invalid; no replacement allowed", nil)
	}
	// Even InactiveDays0 still has a lower start bound. Exact Catalog containment
	// has not been implemented: hold every valid nontrivial legacy schedule
	// instead of assuming alignment from one current-time/year sample.
	return commonCollabHold("schedule_alignment_required", row.QuestID, "nontrivial DB/calendar alignment is not proven", nil)
}

func (cohort *CommonCollabRuntimeCohort) permitsLocation(world string, channel int) bool {
	if cohort == nil || cohort.semantic == nil {
		return false
	}
	for _, location := range cohort.semantic.Locations() {
		if location.World == world && location.Channel == channel {
			return true
		}
	}
	return false
}

func (s *Server) validateCommonCollabRuntime() error {
	declared := s.questDelivery.CommonCollabDeclared()
	if !declared && s.commonCollab == nil {
		return nil
	}
	if declared && (s.erupeConfig == nil || !s.erupeConfig.QuestDelivery.Enabled) {
		return commonCollabHold("inactive_configuration", 0, "conversion declaration requires the existing quest delivery setting", nil)
	}
	if !declared || s.commonCollab == nil || s.commonCollab.catalog != s.questDelivery || !s.commonCollab.permitsLocation(s.worldName, s.channelNumber) {
		return commonCollabHold("cohort_binding", 0, "declared catalog/cohort/location must agree before service start", nil)
	}
	return nil
}

type commonCollabRuntimeView struct {
	activeIDs  map[int]bool
	tunes      []tuneValue
	version    string
	sourceHash string
}

func (s *Session) commonCollabRuntimeViewAt(at time.Time) (*commonCollabRuntimeView, error) {
	if err := s.server.validateCommonCollabRuntime(); err != nil {
		return nil, err
	}
	if s.server.commonCollab == nil {
		return nil, nil
	}
	semantic := s.server.commonCollab.semantic.At(at)
	view := &commonCollabRuntimeView{activeIDs: make(map[int]bool), version: semantic.CatalogVersion(), sourceHash: s.server.commonCollab.sourceHash}
	for _, id := range semantic.QuestIDs() {
		view.activeIDs[id] = true
	}
	for _, tune := range semantic.TuneValues() {
		view.tunes = append(view.tunes, tuneValue{ID: tune.ID, Value: tune.Value})
	}
	if s.server.questDelivery.UsesConfiguredWorldCollabPresentation() {
		// Preserve the existing selected/random NPC layout and world eligibility.
		// This does not filter the separately validated common quest IDs.
		view.tunes = s.appendCollabTuneValues(nil)
	}
	return view, nil
}

func (cohort *CommonCollabRuntimeCohort) merge(rows []EventQuest, view *commonCollabRuntimeView) ([]EventQuest, error) {
	result := make([]EventQuest, 0, len(rows)+13)
	used := make(map[uint32]bool)
	var next uint32
	for _, row := range rows {
		if row.ID > next {
			next = row.ID
		}
		if _, known := commonCollabDefinition(row.QuestID); !known {
			result = append(result, row)
			used[row.ID] = true
		}
	}
	for _, source := range cohort.sources {
		if source.Quest.ID > next {
			next = source.Quest.ID
		}
	}
	for _, source := range cohort.sources {
		row := source.Quest
		if !view.activeIDs[row.QuestID] {
			continue
		}
		if source.Builtin {
			if next == math.MaxUint32 {
				return nil, commonCollabHold("list_identity_overflow", row.QuestID, "no synthetic identity remains", nil)
			}
			next++
			row.ID = next
		} else if used[row.ID] {
			return nil, commonCollabHold("pinned_identity_collision", row.QuestID, "changed ordinary DB row conflicts with frozen override; prepare a new cohort", nil)
		}
		result = append(result, row)
		used[row.ID] = true
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].QuestID < result[j].QuestID })
	return result, nil
}
