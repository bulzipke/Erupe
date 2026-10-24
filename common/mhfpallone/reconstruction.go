package mhfpallone

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

// QuestTemplate describes an existing ZZ quest body that may supply geometry
// for a reconstruction. Matching objectives do NOT establish original content.
type QuestTemplate struct {
	Filename      string                `json:"filename"`
	Variants      []string              `json:"available_variant_filenames"`
	SourceSHA256  string                `json:"source_sha256"`
	DecodedSHA256 string                `json:"decoded_sha256"`
	EmbeddedID    uint16                `json:"embedded_quest_id"`
	MapID         uint32                `json:"map_id"`
	Objectives    [3]QuestInfoObjective `json:"objectives"`
	LargeMonsters []uint16              `json:"large_monster_ids"`
	StatLarge     uint32                `json:"large_stat_table"`
	StatSmall     uint8                 `json:"small_stat_table"`
	Size          uint16                `json:"size_percent"`
	SizeRange     uint16                `json:"size_range"`
	TimeFrames    uint32                `json:"time_frames_30hz"`
	HasGeometry   bool                  `json:"has_map_sections"`
	HasGathering  bool                  `json:"has_gathering_points"`
}

// InspectQuestTemplate reads a bounded ZZ quest body without converting it to
// QuestJSON: conversion would discard unknown fields relevant to reconstruction.
func InspectQuestTemplate(source []byte) (QuestTemplate, error) {
	var v QuestTemplate
	data, err := decodeContainer(source)
	if err != nil {
		return v, err
	}
	if len(data) < 0x86 {
		return v, fmt.Errorf("pallone: quest header shorter than 0x86")
	}
	u16 := func(p uint64) uint16 { return binary.LittleEndian.Uint16(data[p:]) }
	u32 := func(p uint64) uint32 { return binary.LittleEndian.Uint32(data[p:]) }
	valid := func(p, n uint64) bool { return p >= 0x44 && p+n <= uint64(len(data)) }
	props := uint64(u32(0))
	if !valid(props, 0x140) {
		return v, fmt.Errorf("pallone: ZZ main quest properties outside file")
	}
	v.SourceSHA256 = fmt.Sprintf("%x", sha256.Sum256(source))
	v.DecodedSHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
	v.EmbeddedID = u16(props + 0x2e)
	v.MapID = u32(props + 0x24)
	v.TimeFrames = u32(props + 0x20)
	v.StatLarge, v.StatSmall = u32(0x48), data[0x61]
	v.Size, v.SizeRange = u16(0x44), u16(0x46)
	for i := range v.Objectives {
		p := props + 0x30 + uint64(i)*8
		v.Objectives[i] = canonicalObjective(QuestInfoObjective{Type: u32(p), Target: u16(p + 4), CountOrPart: u16(p + 6)})
	}
	v.LargeMonsters = []uint16{}
	block := uint64(u32(0x18))
	if block != 0 {
		if !valid(block, 16) {
			return QuestTemplate{}, fmt.Errorf("pallone: large monster pointer block outside file")
		}
		spawns := uint64(u32(block + 12))
		if !valid(spawns, 5*60) {
			return QuestTemplate{}, fmt.Errorf("pallone: five-slot monster spawn array outside file")
		}
		for i := uint64(0); i < 5; i++ {
			id := data[spawns+i*60]
			if id != 0xff {
				v.LargeMonsters = append(v.LargeMonsters, uint16(id))
			}
		}
	}
	for _, p := range []uint64{4, 0x14, 0x28} {
		ref := uint64(u32(p))
		if ref != 0 && !valid(ref, 4) {
			return QuestTemplate{}, fmt.Errorf("pallone: geometry/gathering pointer outside file")
		}
	}
	v.HasGeometry, v.HasGathering = u32(0x14) != 0, u32(0x28) != 0
	return v, nil
}

func canonicalObjective(v QuestInfoObjective) QuestInfoObjective {
	if v.Type == 0 {
		return QuestInfoObjective{}
	}
	// Monster IDs are a byte followed by padding in these objective types.
	switch v.Type {
	case 1, 0x101, 0x201, 0x4004, 0x8004, 0x18004:
		v.Target &= 0xff
	}
	return v
}

// ReferenceStats is secondary historical evidence, not a quest body. A source
// conflict disables using its values even if an ID happens to match.
type ReferenceStats struct {
	QuestID       uint16   `json:"quest_id"`
	CoreAgrees    bool     `json:"core_agrees_with_g10"`
	Conflicts     []string `json:"conflicts"`
	StatLarge     uint32   `json:"large_stat_table"`
	StatSmall     uint8    `json:"small_stat_table"`
	Size          uint16   `json:"size_percent"`
	SizeRange     uint16   `json:"size_range"`
	LargeMonsters []uint16 `json:"known_large_monster_ids"`
	UnknownRoster []string `json:"unmapped_monster_links"`
}

type TemplateCandidate struct {
	QuestTemplate
	Match            string   `json:"match"`
	ReferenceFits    int      `json:"reference_stat_fields_matched"`
	MainSpawnSeen    bool     `json:"main_target_in_large_spawns"`
	MissingMonsters  []uint16 `json:"missing_reference_monster_ids"`
	AdditionalSpawns []uint16 `json:"additional_template_spawn_ids"`
}

type QuestReconstructionPlan struct {
	QuestID        uint16              `json:"quest_id"`
	Title          string              `json:"original_title"`
	MapID          uint32              `json:"original_map_id"`
	Candidates     []TemplateCandidate `json:"template_candidates"`
	AllMatches     int                 `json:"all_objectives_candidate_count"`
	MainMatches    int                 `json:"main_objective_candidate_count"`
	TargetMatches  int                 `json:"map_target_candidate_count"`
	Reference      *ReferenceStats     `json:"historical_reference,omitempty"`
	RosterOption   *TemplateCandidate  `json:"complete_reference_roster_alternative,omitempty"`
	NeedsDecisions []string            `json:"unresolved_reconstruction_choices"`
	Playable       bool                `json:"playable"`
}

// PlanReconstruction ranks references for review and never silently chooses a
// replacement quest. Every plan remains unplayable: matching list metadata says
// nothing about original spawn positions, scripts, reward flags or map behavior.
func PlanReconstruction(q QuestInfo, templates []QuestTemplate, reference *ReferenceStats) QuestReconstructionPlan {
	v := QuestReconstructionPlan{
		QuestID: q.QuestID, MapID: q.MapID, Title: q.Text[0], Reference: reference,
		Candidates: []TemplateCandidate{}, NeedsDecisions: []string{
			"original geometry and spawn scripts are absent; borrowing a template requires reconstruction approval",
			"validate the complete monster roster and conditional appearances",
			"restore supply/reward table semantics without adding unrelated donor rewards",
			"validate requested day/night and season variants in the client",
		},
	}
	if reference == nil || reference.QuestID != q.QuestID || !reference.CoreAgrees {
		v.NeedsDecisions = append(v.NeedsDecisions, "original stat/size parameters lack agreeing secondary evidence")
	}
	main := canonicalObjective(q.Objectives[0])
	var candidates []TemplateCandidate
	for _, t := range templates {
		if t.MapID != q.MapID || canonicalObjective(t.Objectives[0]).Target != main.Target {
			continue
		}
		c := TemplateCandidate{QuestTemplate: t, Match: "map_and_target_only", MissingMonsters: []uint16{}, AdditionalSpawns: []uint16{}}
		v.TargetMatches++
		if canonicalObjective(t.Objectives[0]) == main {
			v.MainMatches++
			c.Match = "map_and_main_objective"
			all := true
			for i := range q.Objectives {
				all = all && canonicalObjective(t.Objectives[i]) == canonicalObjective(q.Objectives[i])
			}
			if all {
				v.AllMatches++
				c.Match = "map_and_all_objectives"
			}
		}
		for _, id := range t.LargeMonsters {
			if id == main.Target {
				c.MainSpawnSeen = true
			}
		}
		if reference != nil && reference.QuestID == q.QuestID && reference.CoreAgrees {
			for _, equal := range []bool{t.StatLarge == reference.StatLarge, t.StatSmall == reference.StatSmall, t.Size == reference.Size, t.SizeRange == reference.SizeRange} {
				if equal {
					c.ReferenceFits++
				}
			}
			if len(reference.UnknownRoster) == 0 {
				present := make(map[uint16]bool)
				listed := make(map[uint16]bool)
				for _, id := range t.LargeMonsters {
					present[id] = true
				}
				for _, id := range reference.LargeMonsters {
					listed[id] = true
					if !present[id] {
						c.MissingMonsters = append(c.MissingMonsters, id)
					}
				}
				for _, id := range t.LargeMonsters {
					if !listed[id] {
						c.AdditionalSpawns = append(c.AdditionalSpawns, id)
					}
				}
			}
		}
		candidates = append(candidates, c)
	}
	priority := map[string]int{"map_and_all_objectives": 3, "map_and_main_objective": 2, "map_and_target_only": 1}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if priority[a.Match] != priority[b.Match] {
			return priority[a.Match] > priority[b.Match]
		}
		if a.ReferenceFits != b.ReferenceFits {
			return a.ReferenceFits > b.ReferenceFits
		}
		if a.MainSpawnSeen != b.MainSpawnSeen {
			return a.MainSpawnSeen
		}
		return a.Filename < b.Filename
	})
	if reference != nil && reference.QuestID == q.QuestID && reference.CoreAgrees && len(reference.UnknownRoster) == 0 {
		for i := range candidates {
			if len(candidates[i].MissingMonsters) == 0 && len(candidates[i].AdditionalSpawns) == 0 {
				option := candidates[i]
				v.RosterOption = &option
				break
			}
		}
	}
	if len(candidates) > 5 {
		candidates = candidates[:5]
	}
	v.Candidates = candidates
	return v
}
