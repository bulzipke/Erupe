package mhfquest

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyHuntVariant(t *testing.T) {
	tests := []struct {
		name               string
		variant1, variant3 uint8
		want               HuntVariant
	}{
		{name: "normal", want: HuntVariantNormal},
		{name: "fixed hardcore", variant1: questVariant1FixedHC, want: HuntVariantHardcore},
		{name: "optional hardcore", variant1: questVariant1HCToUL, want: HuntVariantHardcoreOptional},
		{name: "fixed UL", variant1: questVariant1ULFixed, want: HuntVariantULFixed},
		{name: "zenith wins", variant1: questVariant1FixedHC, variant3: questVariant3Zenith, want: HuntVariantZenith},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyHuntVariant(tt.variant1, tt.variant3); got != tt.want {
				t.Fatalf("classifyHuntVariant(%02x, %02x) = %q, want %q", tt.variant1, tt.variant3, got, tt.want)
			}
		})
	}
}

func TestResolveHuntVariantBinary(t *testing.T) {
	binPath := t.TempDir()
	questsDir := filepath.Join(binPath, "quests")
	if err := os.MkdirAll(questsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	questID := uint16(23618)
	writeVariantQuest(t, questsDir, questID, "d0", 0x08, questVariant3Zenith)
	writeVariantQuest(t, questsDir, questID, "n0", 0x08, questVariant3Zenith)

	if got := ResolveHuntVariant(binPath, questID); got != HuntVariantZenith {
		t.Fatalf("ResolveHuntVariant() = %q, want %q", got, HuntVariantZenith)
	}
}

func TestResolveHuntVariantRejectsConflictingFiles(t *testing.T) {
	binPath := t.TempDir()
	questsDir := filepath.Join(binPath, "quests")
	if err := os.MkdirAll(questsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	questID := uint16(20028)
	writeVariantQuest(t, questsDir, questID, "d0", questVariant1FixedHC, 0)
	writeVariantQuest(t, questsDir, questID, "n0", 0, 0)

	if got := ResolveHuntVariant(binPath, questID); got != HuntVariantUnknown {
		t.Fatalf("ResolveHuntVariant() = %q, want %q", got, HuntVariantUnknown)
	}
}

func TestResolveHuntVariantRejectsTruncatedBinary(t *testing.T) {
	binPath := t.TempDir()
	questsDir := filepath.Join(binPath, "quests")
	if err := os.MkdirAll(questsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	questID := uint16(20028)
	path := filepath.Join(questsDir, fmt.Sprintf("%05dd0.bin", questID))
	if err := os.WriteFile(path, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveHuntVariant(binPath, questID); got != HuntVariantUnknown {
		t.Fatalf("ResolveHuntVariant() = %q, want %q", got, HuntVariantUnknown)
	}
}

func TestResolveHuntQuestMetadataComparesEveryAvailableSource(t *testing.T) {
	binPath := t.TempDir()
	questsDir := filepath.Join(binPath, "quests")
	if err := os.MkdirAll(questsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	questID := uint16(23603)
	want := HuntQuestMetadata{
		RankKind:   HuntRankG,
		Variant1:   questVariant1GRank,
		Variant2:   questVariant2HighConquest,
		Variant3:   0x04,
		Variant4:   0x08,
		RankBand:   1,
		StatTable1: 55,
		StatTable2: 53,
		Valid:      true,
	}
	for i, suffix := range questVariantSuffixes {
		if i%2 == 0 {
			writeMetadataBinaryQuest(t, questsDir, questID, suffix, want)
		} else {
			writeMetadataJSONQuest(t, questsDir, questID, suffix, want)
		}
	}
	// Both representations for one suffix must also agree.
	writeMetadataJSONQuest(t, questsDir, questID, "d0", want)

	if got := ResolveHuntQuestMetadata(binPath, questID); got != want {
		t.Fatalf("ResolveHuntQuestMetadata() = %+v, want %+v", got, want)
	}
}

func TestResolveHuntQuestMetadataRejectsAnyMetadataConflict(t *testing.T) {
	base := HuntQuestMetadata{
		RankKind:   HuntRankG,
		Variant1:   questVariant1GRank,
		Variant2:   0x20,
		Variant3:   0x04,
		Variant4:   0x08,
		RankBand:   500,
		StatTable1: 54,
		StatTable2: 53,
		Valid:      true,
	}
	tests := []struct {
		name   string
		mutate func(*HuntQuestMetadata)
	}{
		{name: "rank kind via variant1", mutate: func(m *HuntQuestMetadata) { m.Variant1 &^= questVariant1GRank; m.RankKind = HuntRankHR }},
		{name: "variant2", mutate: func(m *HuntQuestMetadata) { m.Variant2++ }},
		{name: "variant3", mutate: func(m *HuntQuestMetadata) { m.Variant3++ }},
		{name: "variant4", mutate: func(m *HuntQuestMetadata) { m.Variant4++ }},
		{name: "rank band", mutate: func(m *HuntQuestMetadata) { m.RankBand++ }},
		{name: "stat table1", mutate: func(m *HuntQuestMetadata) { m.StatTable1++ }},
		{name: "stat table2", mutate: func(m *HuntQuestMetadata) { m.StatTable2++ }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binPath := t.TempDir()
			questsDir := filepath.Join(binPath, "quests")
			if err := os.MkdirAll(questsDir, 0o755); err != nil {
				t.Fatal(err)
			}
			questID := uint16(54774)
			writeMetadataBinaryQuest(t, questsDir, questID, "d0", base)
			other := base
			tt.mutate(&other)
			writeMetadataJSONQuest(t, questsDir, questID, "n0", other)

			got := ResolveHuntQuestMetadata(binPath, questID)
			if got.Valid || got.RankKind != HuntRankUnknown {
				t.Fatalf("conflicting metadata resolved as %+v", got)
			}
		})
	}
}

func TestResolveHuntQuestMetadataRejectsMalformedPresentSource(t *testing.T) {
	binPath := t.TempDir()
	questsDir := filepath.Join(binPath, "quests")
	if err := os.MkdirAll(questsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	questID := uint16(23618)
	metadata := HuntQuestMetadata{RankKind: HuntRankG, Variant1: questVariant1GRank, Valid: true}
	writeMetadataBinaryQuest(t, questsDir, questID, "d0", metadata)
	path := filepath.Join(questsDir, fmt.Sprintf("%05dn0.bin", questID))
	if err := os.WriteFile(path, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}

	got := ResolveHuntQuestMetadata(binPath, questID)
	if got.Valid || got.RankKind != HuntRankUnknown {
		t.Fatalf("malformed source resolved as %+v", got)
	}
}

func TestResolveHuntQuestMetadataNoSourceIsInvalid(t *testing.T) {
	got := ResolveHuntQuestMetadata(t.TempDir(), 12345)
	if got.Valid || got.RankKind != HuntRankUnknown {
		t.Fatalf("missing quest resolved as %+v", got)
	}
}

func TestMonsterHuntVariantForGuaranteedPhantomEvents(t *testing.T) {
	tests := []struct {
		name      string
		questID   uint16
		monsterID int
		want      HuntVariant
	}{
		{name: "red Rajang", questID: 54340, monsterID: 53, want: HuntVariantPhantomRedRajang},
		{name: "phantom Doragyurosu", questID: 54340, monsterID: 95, want: HuntVariantPhantomDora},
		{name: "Voljang in paired event remains normal", questID: 55394, monsterID: 158, want: HuntVariantNormal},
		{name: "ordinary HC Rajang is not guessed", questID: 20028, monsterID: 53, want: HuntVariantHardcore},
		{name: "ordinary HC Doragyurosu is not guessed", questID: 20028, monsterID: 95, want: HuntVariantHardcore},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := HuntVariantNormal
			if tt.questID == 20028 {
				base = HuntVariantHardcore
			}
			if got := MonsterHuntVariantFor(tt.questID, tt.monsterID, base); got != tt.want {
				t.Fatalf("MonsterHuntVariantFor(%d, %d, %q) = %q, want %q", tt.questID, tt.monsterID, base, got, tt.want)
			}
		})
	}
}

func TestMonsterHuntVariantForGuaranteedSharedIDForms(t *testing.T) {
	tests := []struct {
		name      string
		questIDs  []uint16
		monsterID int
		want      HuntVariant
	}{
		{
			name:      "violent Raviente",
			questIDs:  []uint16{62101, 62102, 62103, 62104},
			monsterID: 93,
			want:      HuntVariantViolentRaviente,
		},
		{
			name:      "extreme Zinogre",
			questIDs:  []uint16{54926, 55192, 55345, 55398, 55535, 55919, 56131},
			monsterID: 146,
			want:      HuntVariantExtremeZinogre,
		},
		{
			name:      "extreme Guanzorumu",
			questIDs:  []uint16{55121, 55196, 55348, 55529, 56126},
			monsterID: 154,
			want:      HuntVariantExtremeGuanzorumu,
		},
		{
			name:      "extreme Deviljho",
			questIDs:  []uint16{54849, 55194, 55343, 55396, 55530, 55917},
			monsterID: 155,
			want:      HuntVariantExtremeDeviljho,
		},
		{
			name:      "extreme Elzelion",
			questIDs:  []uint16{55583, 55714, 55936, 56133, 56153, 56158},
			monsterID: 166,
			want:      HuntVariantExtremeElzelion,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, questID := range tt.questIDs {
				if got := MonsterHuntVariantFor(questID, tt.monsterID, HuntVariantNormal); got != tt.want {
					t.Errorf("MonsterHuntVariantFor(%d, %d, normal) = %q, want %q", questID, tt.monsterID, got, tt.want)
				}
			}
		})
	}
}

func TestMonsterHuntVariantForSharedIDExceptionsDoNotLeak(t *testing.T) {
	tests := []struct {
		name      string
		questID   uint16
		monsterID int
		base      HuntVariant
		want      HuntVariant
	}{
		{
			name:      "Flame Tyrant collaboration stays raw normal Deviljho ID",
			questID:   40219,
			monsterID: 155,
			base:      HuntVariantNormal,
			want:      HuntVariantNormal,
		},
		{
			name:      "ordinary Zinogre quest is not extreme",
			questID:   54925,
			monsterID: 146,
			base:      HuntVariantNormal,
			want:      HuntVariantNormal,
		},
		{
			name:      "Voljang companion ignores red Rajang FixedHC flag",
			questID:   55394,
			monsterID: 158,
			base:      HuntVariantHardcore,
			want:      HuntVariantNormal,
		},
		{
			name:      "republished Voljang companion ignores red Rajang FixedHC flag",
			questID:   55938,
			monsterID: 158,
			base:      HuntVariantHardcore,
			want:      HuntVariantNormal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MonsterHuntVariantFor(tt.questID, tt.monsterID, tt.base); got != tt.want {
				t.Fatalf("MonsterHuntVariantFor(%d, %d, %q) = %q, want %q", tt.questID, tt.monsterID, tt.base, got, tt.want)
			}
		})
	}
}

func TestMonsterHuntVariantForMetadataPriority(t *testing.T) {
	validHR := HuntQuestMetadata{RankKind: HuntRankHR, Valid: true}
	validG := HuntQuestMetadata{RankKind: HuntRankG, Variant1: questVariant1GRank, Valid: true}
	tests := []struct {
		name      string
		questID   uint16
		monsterID int
		metadata  HuntQuestMetadata
		want      HuntVariant
	}{
		{name: "exact phantom beats challenge", questID: 54340, monsterID: 53, want: HuntVariantPhantomRedRajang},
		{name: "exact extreme beats Senyu", questID: 54926, monsterID: 146, metadata: HuntQuestMetadata{RankKind: HuntRankG, Variant1: questVariant1GRank, Variant3: questVariant3Senyu, Valid: true}, want: HuntVariantExtremeZinogre},
		{name: "generic challenge", questID: 54338, monsterID: 6, want: HuntVariantChallenge},
		{name: "upper Shiten exact ID", questID: 23603, monsterID: 107, want: HuntVariantUpperShiten},
		{name: "G high conquest", questID: 23602, monsterID: 107, metadata: HuntQuestMetadata{RankKind: HuntRankG, Variant1: questVariant1GRank, Variant2: questVariant2HighConquest, Valid: true}, want: HuntVariantShiten},
		{name: "HR high conquest bit is ignored", questID: 26477, monsterID: 11, metadata: HuntQuestMetadata{RankKind: HuntRankHR, Variant2: questVariant2HighConquest, Valid: true}, want: HuntVariantNormal},
		{name: "G low conquest beats Zenith", questID: 23585, monsterID: 116, metadata: HuntQuestMetadata{RankKind: HuntRankG, Variant1: questVariant1GRank, Variant2: questVariant2LowConquest, Variant3: questVariant3Zenith, Valid: true}, want: HuntVariantConquest},
		{name: "Zenith", questID: 23618, monsterID: 99, metadata: HuntQuestMetadata{RankKind: HuntRankG, Variant1: questVariant1GRank, Variant3: questVariant3Zenith, Valid: true}, want: HuntVariantZenith},
		{name: "confirmed Senyu monster", questID: 54774, monsterID: 148, metadata: HuntQuestMetadata{RankKind: HuntRankG, Variant1: questVariant1GRank, Variant3: questVariant3Senyu, Valid: true}, want: HuntVariantSenyu},
		{name: "Senyu bit does not classify native monster", questID: 54774, monsterID: 99, metadata: HuntQuestMetadata{RankKind: HuntRankG, Variant1: questVariant1GRank, Variant3: questVariant3Senyu, Valid: true}, want: HuntVariantNormal},
		{name: "interception excludes Senyu", questID: 58120, monsterID: 148, metadata: HuntQuestMetadata{RankKind: HuntRankG, Variant1: questVariant1GRank, Variant3: questVariant3Senyu | questVariant3Interception, Valid: true}, want: HuntVariantNormal},
		{name: "UL beats fixed HC", questID: 56008, monsterID: 6, metadata: HuntQuestMetadata{RankKind: HuntRankG, Variant1: questVariant1GRank | questVariant1ULFixed | questVariant1FixedHC, Valid: true}, want: HuntVariantULFixed},
		{name: "fixed HC beats optional", questID: 23306, monsterID: 91, metadata: HuntQuestMetadata{RankKind: HuntRankG, Variant1: questVariant1GRank | questVariant1FixedHC | questVariant1HCToUL, Valid: true}, want: HuntVariantHardcore},
		{name: "optional HC stays unresolved", questID: 23275, monsterID: 11, metadata: HuntQuestMetadata{RankKind: HuntRankG, Variant1: questVariant1GRank | questVariant1HCToUL, Valid: true}, want: HuntVariantHardcoreOptional},
		{name: "normal HR", questID: 11001, monsterID: 6, metadata: validHR, want: HuntVariantNormal},
		{name: "normal G", questID: 23161, monsterID: 6, metadata: validG, want: HuntVariantNormal},
		{name: "invalid metadata", questID: 12345, monsterID: 6, want: HuntVariantUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MonsterHuntVariantForMetadata(tt.questID, tt.monsterID, tt.metadata); got != tt.want {
				t.Fatalf("MonsterHuntVariantForMetadata(%d, %d, %+v) = %q, want %q", tt.questID, tt.monsterID, tt.metadata, got, tt.want)
			}
		})
	}
}

func TestMonsterHuntVariantForMetadataRawExtremeIDs(t *testing.T) {
	tests := []struct {
		monsterID int
		want      HuntVariant
	}{
		{monsterID: 163, want: HuntVariantExtremeNargacuga},
		{monsterID: 167, want: HuntVariantExtremeDuremudira},
		{monsterID: 172, want: HuntVariantExtremeBogabadoru},
		{monsterID: 174, want: HuntVariantExtremeZerureusu},
	}
	for _, tt := range tests {
		if got := MonsterHuntVariantForMetadata(0, tt.monsterID, HuntQuestMetadata{}); got != tt.want {
			t.Errorf("raw extreme monster %d = %q, want %q", tt.monsterID, got, tt.want)
		}
		if got := MonsterHuntVariantFor(0, tt.monsterID, HuntVariantNormal); got != tt.want {
			t.Errorf("legacy resolver raw extreme monster %d = %q, want %q", tt.monsterID, got, tt.want)
		}
	}
}

func TestFeaturedGroup(t *testing.T) {
	tests := []struct {
		variant HuntVariant
		group   string
	}{
		{variant: HuntVariantZenith, group: "zenith"},
		{variant: HuntVariantUpperShiten, group: "upper_shiten"},
		{variant: HuntVariantChallenge, group: "challenge"},
		{variant: HuntVariantExtremeZinogre, group: "challenge"},
		{variant: HuntVariantExtremeNargacuga, group: "challenge"},
		{variant: HuntVariantPhantomRedRajang, group: ""},
		{variant: HuntVariantPhantomDora, group: ""},
		{variant: HuntVariantViolentRaviente, group: ""},
		{variant: HuntVariantShiten, group: ""},
		{variant: HuntVariantConquest, group: ""},
		{variant: HuntVariantNormal, group: ""},
	}
	for _, tt := range tests {
		if got := FeaturedGroup(tt.variant); got != tt.group {
			t.Errorf("FeaturedGroup(%q) = %q, want %q", tt.variant, got, tt.group)
		}
		if got := IsFeaturedHuntVariant(tt.variant); got != (tt.group != "") {
			t.Errorf("IsFeaturedHuntVariant(%q) = %v, want %v", tt.variant, got, tt.group != "")
		}
	}
}

func writeVariantQuest(t *testing.T, dir string, questID uint16, suffix string, variant1, variant3 uint8) {
	t.Helper()
	writeMetadataBinaryQuest(t, dir, questID, suffix, HuntQuestMetadata{
		Variant1: variant1,
		Variant3: variant3,
	})
}

func writeMetadataBinaryQuest(t *testing.T, dir string, questID uint16, suffix string, metadata HuntQuestMetadata) {
	t.Helper()
	const main = 0x86
	data := make([]byte, main+questVariant4Offset+1)
	binary.LittleEndian.PutUint32(data[:4], main)
	binary.LittleEndian.PutUint32(data[0x48:0x4c], metadata.StatTable1)
	data[0x61] = metadata.StatTable2
	binary.LittleEndian.PutUint16(data[main+0x08:main+0x0a], metadata.RankBand)
	binary.LittleEndian.PutUint16(data[main+0x2e:main+0x30], questID)
	data[main+questVariant1Offset] = metadata.Variant1
	data[main+questVariant2Offset] = metadata.Variant2
	data[main+questVariant3Offset] = metadata.Variant3
	data[main+questVariant4Offset] = metadata.Variant4
	path := filepath.Join(dir, fmt.Sprintf("%05d%s.bin", questID, suffix))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeMetadataJSONQuest(t *testing.T, dir string, questID uint16, suffix string, metadata HuntQuestMetadata) {
	t.Helper()
	payload := struct {
		QuestID       uint16 `json:"quest_id"`
		QuestVariant1 uint8  `json:"quest_variant1"`
		QuestVariant2 uint8  `json:"quest_variant2"`
		QuestVariant3 uint8  `json:"quest_variant3"`
		QuestVariant4 uint8  `json:"quest_variant4"`
		RankBand      uint16 `json:"rank_band"`
		StatTable1    uint32 `json:"stat_table_1"`
		StatTable2    uint8  `json:"stat_table_2"`
	}{
		QuestID:       questID,
		QuestVariant1: metadata.Variant1,
		QuestVariant2: metadata.Variant2,
		QuestVariant3: metadata.Variant3,
		QuestVariant4: metadata.Variant4,
		RankBand:      metadata.RankBand,
		StatTable1:    metadata.StatTable1,
		StatTable2:    metadata.StatTable2,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%05d%s.json", questID, suffix))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
