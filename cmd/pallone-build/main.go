// pallone-build builds an isolated, explicitly approved reconstruction bundle.
// It never writes to BinPath or publishes an event.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"erupe-ce/common/decryption"
	"erupe-ce/common/mhfpallone"
)

type recipeStage struct {
	QuestID    uint16 `json:"quest_id"`
	TemplateID uint16 `json:"template_quest_id"`
	AddSpawns  []struct {
		MonsterID  uint8  `json:"monster_id"`
		TemplateID uint16 `json:"template_quest_id"`
	} `json:"additional_spawn_sources"`
	Rewards []struct {
		TableID      uint8 `json:"template_table_id"`
		SectionIndex int   `json:"reference_reward_section_index"`
	} `json:"reward_pools"`
}

type recipe struct {
	Kind     string        `json:"kind"`
	Approved bool          `json:"quest_body_reconstruction_approved"`
	Order    []uint16      `json:"stage_order"`
	Stages   []recipeStage `json:"stages"`
}

type sourceItem struct {
	Item     *uint16 `json:"item_id"`
	Quantity uint16  `json:"quantity"`
	Rate     *uint16 `json:"rate_percent"`
}

type referenceQuest struct {
	mhfpallone.ReferenceStats
	Sections map[string][]struct {
		Items []sourceItem `json:"items"`
	} `json:"source_item_sections"`
}

type builtFile struct {
	Filename       string                 `json:"filename"`
	PackedSHA256   string                 `json:"packed_sha256"`
	Template       string                 `json:"template_filename"`
	SpawnTemplates []string               `json:"additional_spawn_templates"`
	Report         mhfpallone.DraftReport `json:"construction"`
	data           []byte
}

func main() {
	metadata := flag.String("metadata", "", "recovered original quest metadata")
	reference := flag.String("reference", "", "historical-reference.json")
	recipePath := flag.String("recipe", "", "approved, explicit reconstruction recipe")
	binPath := flag.String("bin-path", "bin", "read-only template BinPath")
	out := flag.String("out", "", "new isolated output directory, outside BinPath")
	flag.Parse()
	if err := run(*metadata, *reference, *recipePath, *binPath, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readJSON(path string, v any) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

func run(metadataPath, referencePath, recipePath, binPath, output string) error {
	if metadataPath == "" || referencePath == "" || recipePath == "" || output == "" {
		return fmt.Errorf("usage: pallone-build -metadata file -reference file -recipe file -out new-directory [-bin-path bin]")
	}
	if err := validateOutput(binPath, output); err != nil {
		return err
	}
	var metadata struct {
		Recovery mhfpallone.QuestInfoRecovery `json:"recovery"`
	}
	metadataHash, err := readJSON(metadataPath, &metadata)
	if err != nil {
		return err
	}
	if metadata.Recovery.Kind != "source_metadata_not_loadable_quest" {
		return fmt.Errorf("expected original metadata evidence")
	}
	var reference struct {
		Kind   string           `json:"kind"`
		Quests []referenceQuest `json:"quests"`
	}
	referenceHash, err := readJSON(referencePath, &reference)
	if err != nil {
		return err
	}
	if reference.Kind != "historical_reference_not_loadable_quest" {
		return fmt.Errorf("expected historical reference evidence")
	}
	var plan recipe
	recipeHash, err := readJSON(recipePath, &plan)
	if err != nil {
		return err
	}
	if plan.Kind != "approved_reconstruction_recipe" || !plan.Approved || len(plan.Stages) == 0 || len(plan.Stages) != len(plan.Order) || len(plan.Stages) > 11 {
		return fmt.Errorf("expected approved recipe with 1..11 ordered stages")
	}
	quests, refs := make(map[uint16]mhfpallone.QuestInfo), make(map[uint16]referenceQuest)
	for _, q := range metadata.Recovery.Quests {
		if _, exists := quests[q.QuestID]; exists {
			return fmt.Errorf("duplicate metadata ID %d", q.QuestID)
		}
		quests[q.QuestID] = q
	}
	for _, q := range reference.Quests {
		if _, exists := refs[q.QuestID]; exists {
			return fmt.Errorf("duplicate reference ID %d", q.QuestID)
		}
		refs[q.QuestID] = q
	}
	files := []builtFile{}
	seen := make(map[uint16]bool)
	for index, stage := range plan.Stages {
		q, ok := quests[stage.QuestID]
		ref, refOK := refs[stage.QuestID]
		if !ok || !refOK || stage.QuestID != plan.Order[index] || seen[stage.QuestID] || stage.TemplateID == 0 {
			return fmt.Errorf("invalid recipe stage %d", index)
		}
		seen[stage.QuestID] = true
		opts := mhfpallone.DraftOptions{Reference: ref.ReferenceStats}
		for i, key := range []string{"s", "sa"} {
			for j, section := range ref.Sections[key] {
				if i == 0 && j != 0 || i == 1 && j > 1 {
					return fmt.Errorf("unexpected supply section structure for %d", q.QuestID)
				}
				target := 0
				if i == 1 {
					target = j + 1
				}
				for _, item := range section.Items {
					if item.Item == nil || item.Rate != nil {
						return fmt.Errorf("unresolved supply item for %d", q.QuestID)
					}
					opts.Supplies[target] = append(opts.Supplies[target], mhfpallone.DraftSupply{Item: *item.Item, Quantity: item.Quantity})
				}
			}
		}
		for _, pool := range stage.Rewards {
			sections := ref.Sections["t"]
			if pool.SectionIndex < 0 || pool.SectionIndex >= len(sections) {
				return fmt.Errorf("invalid reward reference section")
			}
			out := mhfpallone.DraftRewardPool{TemplateTableID: pool.TableID}
			for _, item := range sections[pool.SectionIndex].Items {
				if item.Item == nil || item.Rate == nil {
					return fmt.Errorf("unresolved/guaranteed item cannot be used as a weighted pool")
				}
				out.Items = append(out.Items, mhfpallone.DraftRewardItem{Item: *item.Item, Quantity: item.Quantity, Rate: *item.Rate})
			}
			opts.Rewards = append(opts.Rewards, out)
		}
		for _, suffix := range []string{"d0", "d1", "d2", "n0", "n1", "n2"} {
			opts.Night = suffix[0] == 'n'
			name := fmt.Sprintf("%05d%s.bin", stage.TemplateID, suffix)
			source, err := os.ReadFile(filepath.Join(binPath, "quests", name))
			if err != nil {
				return err
			}
			info, err := mhfpallone.InspectQuestTemplate(source)
			if err != nil || info.EmbeddedID != stage.TemplateID {
				return fmt.Errorf("template %s has invalid/mismatched embedded ID", name)
			}
			opts.AddSpawns = nil
			spawnNames := []string{}
			for _, spawn := range stage.AddSpawns {
				spawnName := fmt.Sprintf("%05d%s.bin", spawn.TemplateID, suffix)
				source, err := os.ReadFile(filepath.Join(binPath, "quests", spawnName))
				if err != nil {
					return err
				}
				info, err := mhfpallone.InspectQuestTemplate(source)
				if err != nil || info.EmbeddedID != spawn.TemplateID {
					return fmt.Errorf("spawn template %s has invalid/mismatched embedded ID", spawnName)
				}
				opts.AddSpawns = append(opts.AddSpawns, mhfpallone.DraftSpawn{MonsterID: spawn.MonsterID, Source: source})
				spawnNames = append(spawnNames, spawnName)
			}
			raw, report, err := mhfpallone.BuildQuestDraft(source, q, opts)
			if err != nil {
				return fmt.Errorf("stage %d variant %s: %w", q.QuestID, suffix, err)
			}
			packed := decryption.PackSimple(raw)
			files = append(files, builtFile{Filename: fmt.Sprintf("%05d%s.bin", q.QuestID, suffix), Template: name,
				SpawnTemplates: spawnNames, PackedSHA256: fmt.Sprintf("%x", sha256.Sum256(packed)), Report: report, data: packed})
		}
	}
	// Finish all construction before creating output. Mkdir refuses an existing
	// bundle; neither input assets nor an earlier result can be overwritten.
	if err := os.MkdirAll(filepath.Dir(output), 0750); err != nil {
		return err
	}
	if err := os.Mkdir(output, 0750); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(output, "quests"), 0750); err != nil {
		return err
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(output, "quests", file.Filename), file.data, 0600); err != nil {
			return err
		}
	}
	manifest := struct {
		Kind          string      `json:"kind"`
		Order         []uint16    `json:"stage_order"`
		Playable      bool        `json:"live_client_verified"`
		EventEnabled  bool        `json:"event_enabled"`
		MetadataHash  string      `json:"metadata_sha256"`
		ReferenceHash string      `json:"reference_sha256"`
		RecipeHash    string      `json:"recipe_sha256"`
		Files         []builtFile `json:"files"`
	}{Kind: "offline_reconstructed_route_bundle", Order: plan.Order, MetadataHash: metadataHash,
		ReferenceHash: referenceHash, RecipeHash: recipeHash, Files: files}
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, "manifest.json"), append(b, '\n'), 0600); err != nil {
		return err
	}
	fmt.Printf("Built %d draft variants in %s; live client verification remains required.\n", len(files), output)
	return nil
}

func validateOutput(binPath, output string) error {
	bin, err := filepath.Abs(binPath)
	if err != nil {
		return err
	}
	bin, err = filepath.EvalSymlinks(bin)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(out); err == nil || !os.IsNotExist(err) {
		return fmt.Errorf("output must be a new directory")
	}
	// Resolve existing ancestors too, so a symlink/junction cannot route this
	// offline build into BinPath. Output may have several new parent folders.
	parent := filepath.Dir(out)
	tail := filepath.Base(out)
	for {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			out = filepath.Join(resolved, tail)
			break
		}
		if !os.IsNotExist(err) || filepath.Dir(parent) == parent {
			return err
		}
		tail = filepath.Join(filepath.Base(parent), tail)
		parent = filepath.Dir(parent)
	}
	rel, err := filepath.Rel(bin, out)
	if err != nil {
		return err
	}
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("output must be outside the runtime BinPath")
	}
	return nil
}
