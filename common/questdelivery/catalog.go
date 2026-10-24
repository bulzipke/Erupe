// Package questdelivery evaluates a read-only quest publication manifest.
//
// Publication eligibility is independent of legacy code entitlements, quest
// prerequisites, entry tickets, completion limits, and rewards. An allowed
// result must never be treated as proof that a character may enter a quest.
package questdelivery

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// MaxManifestBytes bounds parsing work before JSON decoding begins.
	MaxManifestBytes = 16 << 20
	// MaxWindowSpan is the largest range accepted by Windows. This bounds a
	// calendar export, rather than the lifetime of a schedule.
	MaxWindowSpan = 366 * 24 * time.Hour
	// MaxOccurrences bounds the total number of rows in one calendar export.
	MaxOccurrences = 100000

	maxProfiles         = 4096
	maxSchedules        = 8192
	maxQuestID          = 65535
	maxHours            = math.MaxInt64 / int64(time.Hour)
	maxRequirements     = 64
	maxRequirementBytes = 4096
	maxVersionBytes     = 128
	// A manifest can contain many overlapping profiles. Bound nested slice
	// copies too, rather than allowing a small row count to exhaust memory.
	maxExportElements = 1000000
)

var rfc3339Shape = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`)

// Profile matches existing worlds by exact name and optional 1-based channel
// numbers. An empty Channels list matches every channel in the named worlds.
// It does not create worlds or channels, or validate their configured existence.
type Profile struct {
	ID       string   `json:"id"`
	Worlds   []string `json:"worlds"`
	Channels []int    `json:"channels,omitempty"`
}

// Occurrence is one publication window. EndsAt is exclusive. Profiles contains
// the existing-world locations where the window can be published; it does not
// assert that every character has the required rights or quest prerequisites.
//
// For a permanent schedule the window is the requested export range. For other
// schedules the original window is preserved, including starts before the
// requested range; only a schedule's own ends_at can shorten a periodic window.
type Occurrence struct {
	QuestID    int       `json:"quest_id"`
	Title      string    `json:"title,omitempty"`
	ScheduleID string    `json:"schedule_id"`
	Permanent  bool      `json:"permanent,omitempty"`
	StartsAt   time.Time `json:"starts_at"`
	EndsAt     time.Time `json:"ends_at"`
	Profiles   []Profile `json:"profiles"`
	// Requirements is descriptive manifest metadata only. It is never used to
	// grant entitlements, waive prerequisites, or decide publication eligibility.
	Requirements []string `json:"requirements,omitempty"`
}

// Catalog is an immutable, validated manifest. It is safe for concurrent reads.
// Accessors return fresh slices, including nested profile slices.
type Catalog struct {
	version    string
	profiles   map[string]Profile
	profileIDs []string
	schedules  map[string]schedule
	quests     map[int]quest
	questIDs   []int

	collabDeclared        bool
	collabPlan            []collabPresentation
	collabConfiguredWorld bool
}

type manifest struct {
	Version                string                          `json:"version"`
	Profiles               []Profile                       `json:"profiles"`
	Schedules              []scheduleJSON                  `json:"schedules"`
	Quests                 []questJSON                     `json:"quests"`
	CollabPresentations    collabPresentationJSONField     `json:"collab_presentations"`
	CollabPresentationMode collabPresentationModeJSONField `json:"collab_presentation_mode"`
}

type scheduleJSON struct {
	ID          string `json:"id"`
	StartsAt    string `json:"starts_at"`
	EndsAt      string `json:"ends_at"`
	ActiveHours int64  `json:"active_hours"`
	RepeatHours int64  `json:"repeat_hours"`
}

type questJSON struct {
	QuestID      int      `json:"quest_id"`
	Profiles     []string `json:"profiles"`
	Schedule     string   `json:"schedule"`
	Disabled     bool     `json:"disabled"`
	Title        string   `json:"title"`
	Requirements []string `json:"requirements"`
}

type scheduleMode uint8

const (
	always scheduleMode = iota
	once
	periodic
)

type schedule struct {
	mode          scheduleMode
	start, end    time.Time
	hasEnd        bool
	activeSeconds int64
	repeatSeconds int64
}

type quest struct {
	profiles     []string
	schedule     string
	disabled     bool
	title        string
	requirements []string
}

// Parse decodes one JSON document and validates its complete reference graph.
// Unknown fields and trailing documents are rejected. No files, databases, or
// network resources are accessed. Disabled quests can omit profiles/schedule;
// their IDs remain managed and are denied in every world and channel.
func Parse(data []byte) (*Catalog, error) {
	if len(data) > MaxManifestBytes {
		return nil, fmt.Errorf("quest delivery manifest exceeds %d bytes", MaxManifestBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var input manifest
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("decode quest delivery manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("quest delivery manifest must contain exactly one JSON document")
		}
		return nil, fmt.Errorf("decode trailing quest delivery data: %w", err)
	}
	if !validVersion(input.Version) {
		return nil, errors.New("quest delivery version must be 1..128 printable ASCII bytes with no surrounding whitespace")
	}
	if len(input.Profiles) > maxProfiles || len(input.Schedules) > maxSchedules || len(input.Quests) > maxQuestID {
		return nil, fmt.Errorf("quest delivery manifest exceeds profile/schedule/quest limits (%d/%d/%d)", maxProfiles, maxSchedules, maxQuestID)
	}
	c := &Catalog{
		version:   input.Version,
		profiles:  make(map[string]Profile, len(input.Profiles)),
		schedules: make(map[string]schedule, len(input.Schedules)),
		quests:    make(map[int]quest, len(input.Quests)),
	}
	for _, p := range input.Profiles {
		if err := validateProfile(p); err != nil {
			return nil, err
		}
		if _, exists := c.profiles[p.ID]; exists {
			return nil, fmt.Errorf("duplicate profile id %q", p.ID)
		}
		// Keep the representation private even if the JSON decoder's allocation
		// strategy changes or Parse gains an alternate input path in the future.
		c.profiles[p.ID] = cloneProfile(p)
		c.profileIDs = append(c.profileIDs, p.ID)
	}
	for _, raw := range input.Schedules {
		if !validID(raw.ID) {
			return nil, errors.New("schedule id must be nonempty and have no surrounding whitespace")
		}
		if _, exists := c.schedules[raw.ID]; exists {
			return nil, fmt.Errorf("duplicate schedule id %q", raw.ID)
		}
		s, err := parseSchedule(raw)
		if err != nil {
			return nil, fmt.Errorf("schedule %q: %w", raw.ID, err)
		}
		c.schedules[raw.ID] = s
	}
	for _, raw := range input.Quests {
		if raw.QuestID < 1 || raw.QuestID > maxQuestID {
			return nil, fmt.Errorf("quest_id %d is outside 1..%d", raw.QuestID, maxQuestID)
		}
		if _, exists := c.quests[raw.QuestID]; exists {
			return nil, fmt.Errorf("duplicate quest_id %d", raw.QuestID)
		}
		if !raw.Disabled && (len(raw.Profiles) == 0 || raw.Schedule == "") {
			return nil, fmt.Errorf("quest %d requires profiles and a schedule", raw.QuestID)
		}
		seen := make(map[string]bool, len(raw.Profiles))
		for _, id := range raw.Profiles {
			if _, exists := c.profiles[id]; !exists {
				return nil, fmt.Errorf("quest %d references missing profile %q", raw.QuestID, id)
			}
			if seen[id] {
				return nil, fmt.Errorf("quest %d repeats profile %q", raw.QuestID, id)
			}
			seen[id] = true
		}
		if raw.Schedule != "" {
			if _, exists := c.schedules[raw.Schedule]; !exists {
				return nil, fmt.Errorf("quest %d references missing schedule %q", raw.QuestID, raw.Schedule)
			}
		}
		if len(raw.Requirements) > maxRequirements {
			return nil, fmt.Errorf("quest %d exceeds %d requirement descriptions", raw.QuestID, maxRequirements)
		}
		for _, requirement := range raw.Requirements {
			if strings.TrimSpace(requirement) == "" || len(requirement) > maxRequirementBytes {
				return nil, fmt.Errorf("quest %d requirements must be nonempty descriptions of at most %d bytes", raw.QuestID, maxRequirementBytes)
			}
		}
		c.quests[raw.QuestID] = quest{
			profiles:     append([]string(nil), raw.Profiles...),
			schedule:     raw.Schedule,
			disabled:     raw.Disabled,
			title:        raw.Title,
			requirements: append([]string(nil), raw.Requirements...),
		}
		c.questIDs = append(c.questIDs, raw.QuestID)
	}
	sort.Strings(c.profileIDs)
	sort.Ints(c.questIDs)
	if err := c.compileCollabPresentations(input.CollabPresentations, input.CollabPresentationMode); err != nil {
		return nil, err
	}
	return c, nil
}

func validID(s string) bool { return s != "" && strings.TrimSpace(s) == s }

func validVersion(value string) bool {
	if !validID(value) || len(value) > maxVersionBytes {
		return false
	}
	// Version is also placed in an HTTP response header. Keep its size bounded
	// and exclude CR/LF, controls, DEL, and non-ASCII bytes at the parse boundary.
	for i := range value {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func validateProfile(p Profile) error {
	if !validID(p.ID) {
		return errors.New("profile id must be nonempty and have no surrounding whitespace")
	}
	if len(p.Worlds) == 0 {
		return fmt.Errorf("profile %q requires at least one world", p.ID)
	}
	worlds := make(map[string]bool, len(p.Worlds))
	for _, world := range p.Worlds {
		if !validID(world) {
			return fmt.Errorf("profile %q has an empty world or surrounding whitespace", p.ID)
		}
		if worlds[world] {
			return fmt.Errorf("profile %q repeats world %q", p.ID, world)
		}
		worlds[world] = true
	}
	channels := make(map[int]bool, len(p.Channels))
	for _, channel := range p.Channels {
		if channel < 1 || channel > 65535 {
			return fmt.Errorf("profile %q channel %d is outside 1..65535", p.ID, channel)
		}
		if channels[channel] {
			return fmt.Errorf("profile %q repeats channel %d", p.ID, channel)
		}
		channels[channel] = true
	}
	return nil
}

func parseSchedule(raw scheduleJSON) (schedule, error) {
	if raw.ActiveHours < 0 || raw.RepeatHours < 0 || raw.ActiveHours > maxHours || raw.RepeatHours > maxHours {
		return schedule{}, errors.New("active_hours and repeat_hours must be nonnegative hours that fit time.Duration")
	}
	var s schedule
	var err error
	if raw.StartsAt != "" {
		s.start, err = parseTimestamp(raw.StartsAt)
		if err != nil {
			return s, fmt.Errorf("invalid starts_at (RFC3339 with explicit offset required): %w", err)
		}
		if !calendarTime(s.start) {
			return s, errors.New("starts_at must have a year in 1..9999")
		}
	}
	if raw.EndsAt != "" {
		s.hasEnd = true
		s.end, err = parseTimestamp(raw.EndsAt)
		if err != nil {
			return s, fmt.Errorf("invalid ends_at (RFC3339 with explicit offset required): %w", err)
		}
		if !calendarTime(s.end) {
			return s, errors.New("ends_at must have a year in 1..9999")
		}
	}
	if raw.ActiveHours == 0 && raw.RepeatHours == 0 {
		switch {
		case raw.StartsAt == "" && raw.EndsAt == "":
			s.mode = always
		case raw.StartsAt != "" && raw.EndsAt != "" && s.end.After(s.start):
			s.mode = once
		default:
			return s, errors.New("one-time schedule requires starts_at and a later ends_at; permanent schedule has neither")
		}
		return s, nil
	}
	if raw.StartsAt == "" || raw.ActiveHours == 0 || raw.RepeatHours == 0 {
		return s, errors.New("periodic schedule requires starts_at, positive active_hours, and positive repeat_hours")
	}
	if raw.ActiveHours > raw.RepeatHours {
		return s, errors.New("active_hours must not exceed repeat_hours")
	}
	if raw.EndsAt != "" && !s.end.After(s.start) {
		return s, errors.New("ends_at must be later than starts_at")
	}
	s.mode = periodic
	s.activeSeconds = raw.ActiveHours * 3600
	s.repeatSeconds = raw.RepeatHours * 3600
	return s, nil
}

func calendarTime(t time.Time) bool { return t.Year() >= 1 && t.Year() <= 9999 }

func parseTimestamp(value string) (time.Time, error) {
	// time.Parse intentionally accepts some non-RFC3339 inputs such as a
	// comma fractional separator and a +24:00 offset. A manifest is stricter.
	if !rfc3339Shape.MatchString(value) {
		return time.Time{}, errors.New("expected RFC3339 timestamp")
	}
	return time.Parse(time.RFC3339, value)
}

func cloneProfile(p Profile) Profile {
	p.Worlds = append([]string(nil), p.Worlds...)
	p.Channels = append([]int(nil), p.Channels...)
	return p
}

// Version returns the opaque manifest version used by exports and logging.
func (c *Catalog) Version() string { return c.version }

// Profiles returns copies of all profiles ordered by profile ID.
func (c *Catalog) Profiles() []Profile {
	result := make([]Profile, 0, len(c.profileIDs))
	for _, id := range c.profileIDs {
		result = append(result, cloneProfile(c.profiles[id]))
	}
	return result
}

// QuestIDs returns a sorted copy of all managed IDs, including disabled quests.
func (c *Catalog) QuestIDs() []int { return append([]int(nil), c.questIDs...) }

// Allows evaluates publication only. A managed quest is denied outside its
// selected locations/time, or everywhere if disabled. An unmanaged quest returns
// (false, false), allowing the caller to apply its separate legacy behavior.
// Channels are 1-based; zero/negative channels cannot match a profile.
func (c *Catalog) Allows(questID int, worldName string, channelNumber int, now time.Time) (managed bool, allowed bool) {
	q, exists := c.quests[questID]
	if !exists {
		return false, false
	}
	if q.disabled || channelNumber < 1 || channelNumber > 65535 || !c.schedules[q.schedule].active(now) {
		return true, false
	}
	for _, id := range q.profiles {
		if matches(c.profiles[id], worldName, channelNumber) {
			return true, true
		}
	}
	return true, false
}

func matches(p Profile, world string, channel int) bool {
	foundWorld := false
	for _, candidate := range p.Worlds {
		if candidate == world {
			foundWorld = true
			break
		}
	}
	if !foundWorld {
		return false
	}
	if len(p.Channels) == 0 {
		return true
	}
	for _, candidate := range p.Channels {
		if candidate == channel {
			return true
		}
	}
	return false
}

func (s schedule) active(now time.Time) bool {
	if s.mode == always {
		return true
	}
	if now.Before(s.start) || (s.hasEnd && !now.Before(s.end)) {
		return false
	}
	if s.mode == once {
		return true
	}
	// time.Sub saturates after about 292 years. Unix seconds and a nanos
	// correction keep an old fixed anchor correct across that boundary too.
	seconds := elapsedWholeSeconds(s.start, now)
	return seconds%s.repeatSeconds < s.activeSeconds
}

func elapsedWholeSeconds(start, at time.Time) int64 {
	seconds := at.Unix() - start.Unix()
	if at.Nanosecond() < start.Nanosecond() {
		seconds--
	}
	return seconds
}

// AllowedQuestIDs returns sorted managed IDs that may be published at the given
// existing world/channel/time. It does not include unmanaged legacy quests.
func (c *Catalog) AllowedQuestIDs(worldName string, channelNumber int, now time.Time) []int {
	result := make([]int, 0)
	for _, id := range c.questIDs {
		if _, allowed := c.Allows(id, worldName, channelNumber, now); allowed {
			result = append(result, id)
		}
	}
	return result
}

type interval struct{ start, end time.Time }

// Windows exports publication occurrences overlapping [from,to), sorted by
// start, quest ID, then schedule ID. The caller chooses its display time zone.
// Windows does not update schedules or reset any quest/character state.
//
// Range and row caps bound calendar work. An overflow returns an error and no
// partial result, so a caller cannot mistake a truncated calendar for complete.
func (c *Catalog) Windows(from, to time.Time) ([]Occurrence, error) {
	if !calendarTime(from) || !calendarTime(to) || !from.Before(to) {
		return nil, errors.New("calendar range requires representable times with from before to")
	}
	if to.Sub(from) > MaxWindowSpan {
		return nil, fmt.Errorf("calendar range exceeds %s", MaxWindowSpan)
	}
	// Expand each referenced schedule only once. The combined occurrence count
	// is checked before allocating rows or profile copies for each quest.
	windows := make(map[string][]interval)
	resultCount := 0
	elementCount := 0
	for _, id := range c.questIDs {
		q := c.quests[id]
		if q.disabled {
			continue
		}
		spans, exists := windows[q.schedule]
		if !exists {
			var err error
			spans, err = c.schedules[q.schedule].windows(from, to)
			if err != nil {
				return nil, fmt.Errorf("schedule %q: %w", q.schedule, err)
			}
			windows[q.schedule] = spans
		}
		if len(spans) > MaxOccurrences-resultCount {
			return nil, fmt.Errorf("calendar exceeds %d occurrences", MaxOccurrences)
		}
		if len(spans) > 0 {
			weight := len(q.requirements)
			for _, id := range q.profiles {
				p := c.profiles[id]
				additional := 1 + len(p.Worlds) + len(p.Channels)
				if additional > maxExportElements-weight {
					return nil, fmt.Errorf("calendar exceeds %d nested profile/requirement elements", maxExportElements)
				}
				weight += additional
			}
			if weight > (maxExportElements-elementCount)/len(spans) {
				return nil, fmt.Errorf("calendar exceeds %d nested profile/requirement elements", maxExportElements)
			}
			elementCount += len(spans) * weight
		}
		resultCount += len(spans)
	}
	result := make([]Occurrence, 0, resultCount)
	for _, id := range c.questIDs {
		q := c.quests[id]
		if q.disabled {
			continue
		}
		for _, span := range windows[q.schedule] {
			p := make([]Profile, 0, len(q.profiles))
			for _, profileID := range q.profiles {
				p = append(p, cloneProfile(c.profiles[profileID]))
			}
			result = append(result, Occurrence{
				QuestID: id, Title: q.title, ScheduleID: q.schedule,
				Permanent: c.schedules[q.schedule].mode == always,
				StartsAt:  span.start, EndsAt: span.end, Profiles: p,
				Requirements: append([]string(nil), q.requirements...),
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].StartsAt.Equal(result[j].StartsAt) {
			return result[i].StartsAt.Before(result[j].StartsAt)
		}
		if result[i].QuestID != result[j].QuestID {
			return result[i].QuestID < result[j].QuestID
		}
		return result[i].ScheduleID < result[j].ScheduleID
	})
	return result, nil
}

func (s schedule) windows(from, to time.Time) ([]interval, error) {
	if s.mode == always {
		return []interval{{from, to}}, nil
	}
	if !s.start.Before(to) || (s.hasEnd && !s.end.After(from)) {
		return nil, nil
	}
	if s.mode == once {
		return []interval{{s.start, s.end}}, nil
	}
	var cycle int64
	if !from.Before(s.start) {
		cycle = elapsedWholeSeconds(s.start, from) / s.repeatSeconds
	}
	result := make([]interval, 0)
	for {
		// RFC3339 years have a small enough Unix-second range for this math;
		// do not multiply the entire elapsed anchor distance by time.Second.
		start := time.Unix(s.start.Unix()+cycle*s.repeatSeconds, int64(s.start.Nanosecond())).In(s.start.Location())
		if !start.Before(to) || (s.hasEnd && !start.Before(s.end)) {
			break
		}
		end := start.Add(time.Duration(s.activeSeconds) * time.Second)
		if s.hasEnd && end.After(s.end) {
			end = s.end
		}
		if !calendarTime(start) || !calendarTime(end) {
			return nil, errors.New("occurrence falls outside representable calendar years 1..9999")
		}
		if end.After(from) {
			if len(result) >= MaxOccurrences {
				return nil, fmt.Errorf("schedule exceeds %d calendar occurrences", MaxOccurrences)
			}
			result = append(result, interval{start, end})
		}
		cycle++
	}
	return result, nil
}
