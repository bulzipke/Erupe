// pallone-recover creates a read-only reconstruction plan, never quest assets.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"erupe-ce/common/mhfpallone"
)

func main() {
	metadata := flag.String("metadata", "", "original-quest-metadata.json from pallone-audit")
	binPath := flag.String("bin-path", "bin", "Erupe BinPath (read-only)")
	reference := flag.String("reference", "", "optional historical-reference.json from extract_reference.py")
	flag.Parse()
	if *metadata == "" {
		fmt.Fprintln(os.Stderr, "usage: pallone-recover -metadata file.json [-bin-path bin] [-reference file.json]")
		os.Exit(2)
	}
	if err := run(*metadata, *binPath, *reference); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(metadataPath, binPath, referencePath string) error {
	input, err := os.ReadFile(metadataPath)
	if err != nil {
		return err
	}
	var metadata struct {
		SourceSHA256 string                       `json:"source_sha256"`
		Recovery     mhfpallone.QuestInfoRecovery `json:"recovery"`
	}
	if err := json.Unmarshal(input, &metadata); err != nil {
		return err
	}
	if metadata.SourceSHA256 == "" || metadata.Recovery.Kind != "source_metadata_not_loadable_quest" || len(metadata.Recovery.Quests) == 0 {
		return fmt.Errorf("expected original metadata recovery evidence")
	}
	refs := make(map[uint16]*mhfpallone.ReferenceStats)
	if referencePath != "" {
		input, err := os.ReadFile(referencePath)
		if err != nil {
			return err
		}
		var evidence struct {
			Quests []mhfpallone.ReferenceStats `json:"quests"`
		}
		if err := json.Unmarshal(input, &evidence); err != nil {
			return err
		}
		for i := range evidence.Quests {
			q := &evidence.Quests[i]
			if refs[q.QuestID] != nil {
				return fmt.Errorf("duplicate reference ID %d", q.QuestID)
			}
			refs[q.QuestID] = q
		}
	}
	entries, err := os.ReadDir(filepath.Join(binPath, "quests"))
	if err != nil {
		return err
	}
	groups := make(map[string][]string)
	names := regexp.MustCompile(`^([0-9]{5})[dn][012]\.bin$`)
	for _, entry := range entries {
		if match := names.FindStringSubmatch(entry.Name()); !entry.IsDir() && match != nil {
			groups[match[1]] = append(groups[match[1]], entry.Name())
		}
	}
	var templates []mhfpallone.QuestTemplate
	skipped := []string{}
	for _, variants := range groups {
		sort.Strings(variants)
		name := variants[0]
		input, err := os.ReadFile(filepath.Join(binPath, "quests", name))
		if err != nil {
			return err
		}
		template, err := mhfpallone.InspectQuestTemplate(input)
		if err != nil {
			skipped = append(skipped, name+": "+err.Error())
			continue
		}
		template.Filename, template.Variants = name, variants
		templates = append(templates, template)
	}
	sort.Strings(skipped)
	sort.Slice(templates, func(i, j int) bool { return templates[i].Filename < templates[j].Filename })
	report := struct {
		Kind           string                               `json:"kind"`
		SourceSHA256   string                               `json:"original_metadata_source_sha256"`
		TemplateCount  int                                  `json:"representative_templates_inspected"`
		Skipped        []string                             `json:"unparsed_templates"`
		AllMatches     int                                  `json:"quests_with_all_objectives_and_map"`
		MainMatches    int                                  `json:"quests_with_main_objective_and_map"`
		TargetMatches  int                                  `json:"quests_with_main_target_and_map"`
		ReferenceCount int                                  `json:"quests_with_agreeing_historical_reference"`
		Conflicts      []uint16                             `json:"conflicting_reference_ids"`
		Plans          []mhfpallone.QuestReconstructionPlan `json:"plans"`
		Limits         []string                             `json:"limits"`
	}{Kind: "reconstruction_review_only", SourceSHA256: metadata.SourceSHA256, TemplateCount: len(templates), Skipped: skipped,
		Conflicts: []uint16{}, Plans: []mhfpallone.QuestReconstructionPlan{}, Limits: []string{
			"No stage quest is written, renamed, enabled or deployed.",
			"Only one representative per filename ID is inspected; variant filenames indicate presence, not verified variant equivalence.",
			"Matching maps/objectives and historical list data do not establish original spawn scripts or reward semantics.",
		}}
	for _, q := range metadata.Recovery.Quests {
		ref := refs[q.QuestID]
		plan := mhfpallone.PlanReconstruction(q, templates, ref)
		report.Plans = append(report.Plans, plan)
		if plan.AllMatches > 0 {
			report.AllMatches++
		}
		if plan.MainMatches > 0 {
			report.MainMatches++
		}
		if plan.TargetMatches > 0 {
			report.TargetMatches++
		}
		if ref != nil {
			if ref.CoreAgrees {
				report.ReferenceCount++
			} else {
				report.Conflicts = append(report.Conflicts, q.QuestID)
			}
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
