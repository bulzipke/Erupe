package channelserver

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"erupe-ce/common/decryption"
	"erupe-ce/common/mhfpallone"
)

// This optional integration check consumes the actual offline bundle and retail
// donor variants, without opening a server, connecting to a DB or enabling play.
func TestPalloneDraftRetailBundle(t *testing.T) {
	bundle := os.Getenv("PALLONE_DRAFT_BUNDLE")
	if bundle == "" {
		t.Skip("set PALLONE_DRAFT_BUNDLE to the isolated route-001 output")
	}
	binPath := os.Getenv("PALLONE_DRAFT_TEMPLATE_BIN")
	if binPath == "" {
		t.Fatal("set PALLONE_DRAFT_TEMPLATE_BIN to the read-only retail template BinPath")
	}
	var manifest struct {
		Order        []uint16 `json:"stage_order"`
		Playable     bool     `json:"live_client_verified"`
		EventEnabled bool     `json:"event_enabled"`
		MetadataHash string   `json:"metadata_sha256"`
		Files        []struct {
			Filename     string                 `json:"filename"`
			PackedSHA256 string                 `json:"packed_sha256"`
			Template     string                 `json:"template_filename"`
			Report       mhfpallone.DraftReport `json:"construction"`
		} `json:"files"`
	}
	read := func(path string) []byte {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if err := json.Unmarshal(read(filepath.Join(bundle, "manifest.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest.Order, []uint16{27438, 27479, 27348, 27365}) || len(manifest.Files) != 24 || manifest.Playable || manifest.EventEnabled {
		t.Fatal("unexpected route/file count or playable/enabled claim")
	}
	metadataBytes := read(filepath.Join("..", "..", "docs", "data", "pallone-festival-20261023", "original-quest-metadata.json"))
	if fmt.Sprintf("%x", sha256.Sum256(metadataBytes)) != manifest.MetadataHash {
		t.Fatal("metadata evidence changed since construction")
	}
	var metadata struct {
		Recovery mhfpallone.QuestInfoRecovery `json:"recovery"`
	}
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatal(err)
	}
	quests := make(map[uint16]mhfpallone.QuestInfo)
	for _, q := range metadata.Recovery.Quests {
		quests[q.QuestID] = q
	}
	seen := make(map[string]bool)
	for _, file := range manifest.Files {
		t.Run(file.Filename, func(t *testing.T) {
			if seen[file.Filename] {
				t.Fatal("duplicate variant")
			}
			seen[file.Filename] = true
			packed := read(filepath.Join(bundle, "quests", file.Filename))
			if fmt.Sprintf("%x", sha256.Sum256(packed)) != file.PackedSHA256 {
				t.Fatal("packed output hash mismatch")
			}
			raw := decryption.UnpackSimple(packed)
			if fmt.Sprintf("%x", sha256.Sum256(raw)) != file.Report.OutputSHA256 {
				t.Fatal("decoded output hash mismatch")
			}
			q, err := ParseQuestBinary(raw)
			if err != nil {
				t.Fatal(err)
			}
			original, ok := quests[q.QuestID]
			if !ok || q.QuestID != file.Report.QuestID || !strings.HasPrefix(file.Filename, fmt.Sprintf("%05d", q.QuestID)) {
				t.Fatal("wrong stage ID")
			}
			wantMap := original.MapID
			if file.Filename[5] == 'n' {
				wantMap = map[uint32]uint32{6: 20, 11: 21, 45: 46, 57: 58}[original.MapID]
			}
			if q.Map != wantMap || q.TimeLimitMinutes != 30 || q.Fee != original.Fee || q.RewardMain != original.ZennyMain || uint32(q.RewardSubA) != original.ZennySubA || uint32(q.RewardSubB) != original.ZennySubB || q.Title.Resolve("jp") != original.Text[0] {
				t.Fatal("original quest properties/text or native variant map disagree")
			}
			info, err := mhfpallone.InspectQuestTemplate(raw)
			if err != nil || info.Objectives != original.Objectives {
				t.Fatal("original objective types/counts/part IDs disagree", err)
			}
			wantRoster := map[uint16][]uint16{27438: {39, 38}, 27479: {15, 45}, 27348: {}, 27365: {101, 102}}[q.QuestID]
			if !reflect.DeepEqual(info.LargeMonsters, wantRoster) {
				t.Fatalf("spawn roster %v, want %v", info.LargeMonsters, wantRoster)
			}
			for _, pool := range q.Rewards {
				if pool.TableID != 1 && !(q.QuestID == 27479 && (pool.TableID == 2 || pool.TableID == 3)) {
					t.Fatal("unrelated donor reward pool retained")
				}
			}
			if len(q.Rewards) == 0 {
				t.Fatal("missing evidenced rewards")
			}
			donorRaw := decryption.UnpackSimple(read(filepath.Join(binPath, "quests", file.Template)))
			donor, err := ParseQuestBinary(donorRaw)
			if err != nil {
				t.Fatal("native donor parse:", err)
			}
			for name, pair := range map[string][2]any{
				"stages": {q.Stages, donor.Stages}, "map sections": {q.MapSections, donor.MapSections},
				"transitions": {q.AreaTransitions, donor.AreaTransitions}, "mappings": {q.AreaMappings, donor.AreaMappings},
				"map info": {q.MapInfo, donor.MapInfo}, "gathering": {q.GatheringPoints, donor.GatheringPoints},
				"facilities": {q.AreaFacilities, donor.AreaFacilities}, "gathering tables": {q.GatheringTables, donor.GatheringTables},
			} {
				if !reflect.DeepEqual(pair[0], pair[1]) {
					t.Fatalf("lost native %s", name)
				}
			}
			// Variant bytes and unknown ZZ-only fields must remain donor data;
			// G10's compact HRP values cannot overwrite these fields.
			main, donorMain := int(binary.LittleEndian.Uint32(raw)), int(binary.LittleEndian.Uint32(donorRaw))
			if !reflect.DeepEqual(raw[main+0x8c:main+0xa4], donorRaw[donorMain+0x8c:donorMain+0xa4]) {
				t.Fatal("G10 tail was copied over ZZ-only fields")
			}
		})
	}
}
