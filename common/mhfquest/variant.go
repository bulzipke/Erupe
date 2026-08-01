package mhfquest

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"erupe-ce/common/decryption"
)

// HuntVariant is the quest form used to keep otherwise identical monster IDs
// in separate dashboard hunt rankings.
type HuntVariant string

const (
	HuntVariantNormal              HuntVariant = "normal"
	HuntVariantHardcore            HuntVariant = "hardcore"
	HuntVariantZenith              HuntVariant = "zenith"
	HuntVariantSenyu               HuntVariant = "senyu"
	HuntVariantConquest            HuntVariant = "conquest"
	HuntVariantShiten              HuntVariant = "shiten"
	HuntVariantUpperShiten         HuntVariant = "upper_shiten"
	HuntVariantChallenge           HuntVariant = "challenge"
	HuntVariantHardcoreOptional    HuntVariant = "hardcore_optional"
	HuntVariantULFixed             HuntVariant = "ul_fixed"
	HuntVariantUnknown             HuntVariant = "unknown"
	HuntVariantPhantomRedRajang    HuntVariant = "phantom_red_rajang"
	HuntVariantPhantomDora         HuntVariant = "phantom_doragyurosu"
	HuntVariantViolentRaviente     HuntVariant = "violent_raviente"
	HuntVariantExtremeZinogre      HuntVariant = "extreme_zinogre"
	HuntVariantExtremeGuanzorumu   HuntVariant = "extreme_guanzorumu"
	HuntVariantExtremeDeviljho     HuntVariant = "extreme_deviljho"
	HuntVariantExtremeElzelion     HuntVariant = "extreme_elzelion"
	HuntVariantExtremeNargacuga    HuntVariant = "extreme_nargacuga"
	HuntVariantExtremeDuremudira   HuntVariant = "extreme_duremudira"
	HuntVariantExtremeBogabadorumu HuntVariant = "extreme_bogabadorumu"
	// HuntVariantExtremeBogabadoru is kept as a source-compatible alias for
	// code written before the full romanized monster name was standardized.
	HuntVariantExtremeBogabadoru             = HuntVariantExtremeBogabadorumu
	HuntVariantExtremeZerureusu  HuntVariant = "extreme_zerureusu"
)

// HuntRankKind is the progression rank family required by a quest.
type HuntRankKind string

const (
	HuntRankUnknown HuntRankKind = "unknown"
	HuntRankHR      HuntRankKind = "hr"
	HuntRankG       HuntRankKind = "g"

	// Long-form aliases make the values discoverable alongside the type name.
	HuntRankKindUnknown = HuntRankUnknown
	HuntRankKindHR      = HuntRankHR
	HuntRankKindG       = HuntRankG
)

// HuntQuestMetadata contains only quest fields that can be compared safely
// across every day/night/season representation of the same quest ID.
type HuntQuestMetadata struct {
	RankKind   HuntRankKind
	Variant1   uint8
	Variant2   uint8
	Variant3   uint8
	Variant4   uint8
	RankBand   uint16
	StatTable1 uint32
	StatTable2 uint8
	Valid      bool
}

const (
	questVariant1FixedHC      = uint8(1 << 1)
	questVariant1HCToUL       = uint8(1 << 2)
	questVariant1GRank        = uint8(1 << 3)
	questVariant1ULFixed      = uint8(1 << 7)
	questVariant2LowConquest  = uint8(1 << 0)
	questVariant2HighConquest = uint8(1 << 6)
	questVariant3Senyu        = uint8(1 << 1)
	questVariant3Zenith       = uint8(1 << 4)
	questVariant3Interception = uint8(1 << 5)
	questVariant1Offset       = 0x97
	questVariant2Offset       = 0x98
	questVariant3Offset       = 0x99
	questVariant4Offset       = 0x9a
)

// ResolveHuntQuestMetadata reads every available binary and JSON form of all
// day/night/season variants. Any present but malformed source, embedded quest
// ID mismatch, or metadata disagreement invalidates the result. Missing forms
// are allowed because many retail quests do not provide all six suffixes.
func ResolveHuntQuestMetadata(binPath string, questID uint16) HuntQuestMetadata {
	if questID == 0 {
		return invalidHuntQuestMetadata()
	}

	var resolved HuntQuestMetadata
	found := false
	questsDir := filepath.Join(binPath, "quests")
	for _, suffix := range questVariantSuffixes {
		base := filepath.Join(questsDir, fmt.Sprintf("%05d%s", questID, suffix))
		for _, source := range []struct {
			path  string
			parse func(string, uint16) (HuntQuestMetadata, bool)
		}{
			{path: base + ".bin", parse: huntMetadataFromBinaryFile},
			{path: base + ".json", parse: huntMetadataFromJSONFile},
		} {
			if _, err := os.Stat(source.path); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return invalidHuntQuestMetadata()
			}
			metadata, ok := source.parse(source.path, questID)
			if !ok {
				return invalidHuntQuestMetadata()
			}
			if !found {
				resolved = metadata
				found = true
				continue
			}
			if !sameHuntQuestMetadata(resolved, metadata) {
				return invalidHuntQuestMetadata()
			}
		}
	}
	if !found {
		return invalidHuntQuestMetadata()
	}
	resolved.Valid = true
	return resolved
}

// ResolveHuntVariant preserves the original quest-wide classification API.
// More detailed ranking categories require MonsterHuntVariantForMetadata.
func ResolveHuntVariant(binPath string, questID uint16) HuntVariant {
	metadata := ResolveHuntQuestMetadata(binPath, questID)
	if !metadata.Valid {
		return HuntVariantUnknown
	}
	return classifyHuntVariant(metadata.Variant1, metadata.Variant3)
}

// MonsterHuntVariantFor applies forms that are guaranteed by a specific quest
// and monster-ID pair after the quest-wide form has been resolved. Some forms
// reuse a raw monster ID and cannot be inferred from the quest-wide HC/Zenith
// flags. Restricting these overrides to confirmed quest IDs avoids guessing on
// ordinary and collaboration quests that use the same raw IDs.
func MonsterHuntVariantFor(questID uint16, monsterID int, base HuntVariant) HuntVariant {
	if exact, ok := exactMonsterHuntVariant(questID, monsterID); ok {
		return exact
	}
	// These two quests use FixedHC to produce the guaranteed red Rajang, but
	// their Voljang companion is the ordinary form. Do not let the quest-wide
	// flag create a non-existent "hardcore Voljang" ranking.
	if monsterID == 158 && (questID == 55394 || questID == 55938) {
		return HuntVariantNormal
	}
	return base
}

// MonsterHuntVariantForMetadata resolves both monster-specific forms and the
// quest difficulty family. Exact identities take precedence over quest-wide
// flags. Optional HC remains an internal sentinel until the channel session
// applies the runtime selection reported by the ZZ quest setup/result data.
func MonsterHuntVariantForMetadata(questID uint16, monsterID int, metadata HuntQuestMetadata) HuntVariant {
	if exact, ok := exactMonsterHuntVariant(questID, monsterID); ok {
		return exact
	}
	if questIsChallenge(questID) {
		return HuntVariantChallenge
	}
	if questID == 23603 || questID == 23605 {
		return HuntVariantUpperShiten
	}
	if !metadata.Valid {
		return HuntVariantUnknown
	}
	if metadata.RankKind == HuntRankG && metadata.Variant2&questVariant2HighConquest != 0 {
		return HuntVariantShiten
	}
	if metadata.RankKind == HuntRankG && metadata.Variant2&questVariant2LowConquest != 0 {
		return HuntVariantConquest
	}
	if metadata.Variant3&questVariant3Zenith != 0 {
		return HuntVariantZenith
	}
	if metadata.Variant3&questVariant3Senyu != 0 &&
		metadata.Variant3&questVariant3Interception == 0 &&
		isConfirmedSenyuMonster(monsterID) {
		return HuntVariantSenyu
	}
	if metadata.Variant1&questVariant1ULFixed != 0 {
		return HuntVariantULFixed
	}
	if metadata.Variant1&questVariant1FixedHC != 0 {
		return HuntVariantHardcore
	}
	if metadata.Variant1&questVariant1HCToUL != 0 {
		return HuntVariantHardcoreOptional
	}
	return HuntVariantNormal
}

// IsFeaturedHuntVariant reports whether a variant belongs in the dashboard's
// always-visible high-difficulty groups.
func IsFeaturedHuntVariant(variant HuntVariant) bool {
	return FeaturedGroup(variant) != ""
}

// FeaturedGroup maps a ranking identity to its always-visible dashboard
// group. An empty string means the ranking belongs in the expandable section.
func FeaturedGroup(variant HuntVariant) string {
	switch variant {
	case HuntVariantZenith:
		return "zenith"
	case HuntVariantUpperShiten:
		return "upper_shiten"
	case HuntVariantChallenge,
		HuntVariantExtremeZinogre,
		HuntVariantExtremeGuanzorumu,
		HuntVariantExtremeDeviljho,
		HuntVariantExtremeElzelion,
		HuntVariantExtremeNargacuga,
		HuntVariantExtremeDuremudira,
		HuntVariantExtremeBogabadoru,
		HuntVariantExtremeZerureusu:
		return "challenge"
	default:
		return ""
	}
}

func exactMonsterHuntVariant(questID uint16, monsterID int) (HuntVariant, bool) {
	switch monsterID {
	case 163:
		return HuntVariantExtremeNargacuga, true
	case 167:
		return HuntVariantExtremeDuremudira, true
	case 172:
		return HuntVariantExtremeBogabadoru, true
	case 174:
		return HuntVariantExtremeZerureusu, true
	}
	if monsterID == 53 && questHasGuaranteedRedRajang(questID) {
		return HuntVariantPhantomRedRajang, true
	}
	if monsterID == 95 && questHasGuaranteedPhantomDoragyurosu(questID) {
		return HuntVariantPhantomDora, true
	}
	if monsterID == 93 && questHasViolentRaviente(questID) {
		return HuntVariantViolentRaviente, true
	}
	if monsterID == 146 && questHasExtremeZinogre(questID) {
		return HuntVariantExtremeZinogre, true
	}
	if monsterID == 154 && questHasExtremeGuanzorumu(questID) {
		return HuntVariantExtremeGuanzorumu, true
	}
	if monsterID == 155 && questHasExtremeDeviljho(questID) {
		return HuntVariantExtremeDeviljho, true
	}
	if monsterID == 166 && questHasExtremeElzelion(questID) {
		return HuntVariantExtremeElzelion, true
	}
	return HuntVariantUnknown, false
}

func questHasGuaranteedRedRajang(questID uint16) bool {
	switch questID {
	case 54340, 54341, 54518, 54530, 54592, 54624, 54696, 54765,
		55199, 55204, 55346, 55394, 55533, 55937, 55938:
		return true
	default:
		return false
	}
}

func questHasGuaranteedPhantomDoragyurosu(questID uint16) bool {
	switch questID {
	case 54340, 54341, 54518, 54530, 55204:
		return true
	default:
		return false
	}
}

func questHasViolentRaviente(questID uint16) bool {
	switch questID {
	case 62101, 62102, 62103, 62104:
		return true
	default:
		return false
	}
}

func questHasExtremeZinogre(questID uint16) bool {
	switch questID {
	case 54926, 55192, 55345, 55398, 55535, 55919, 56131:
		return true
	default:
		return false
	}
}

func questHasExtremeGuanzorumu(questID uint16) bool {
	switch questID {
	case 55121, 55196, 55348, 55529, 56126:
		return true
	default:
		return false
	}
}

func questHasExtremeDeviljho(questID uint16) bool {
	switch questID {
	case 54849, 55194, 55343, 55396, 55530, 55917:
		return true
	default:
		return false
	}
}

func questHasExtremeElzelion(questID uint16) bool {
	switch questID {
	case 55583, 55714, 55936, 56133, 56153, 56158:
		return true
	default:
		return false
	}
}

// questIsChallenge is generated from the 65 quest IDs whose Korean-source
// retail binary title category contains "超難関クエスト". Keeping an explicit
// allowlist avoids treating unrelated quests with overlapping flag patterns
// as challenge forms.
func questIsChallenge(questID uint16) bool {
	switch questID {
	case 23648, 23649,
		54338, 54339, 54340, 54341, 54358, 54359,
		54516, 54517, 54518, 54528, 54529, 54530, 54592,
		54607, 54624, 54696, 54765, 54883,
		55026, 55080, 55081,
		55120, 55121, 55148, 55195, 55196, 55197, 55198, 55199,
		55201, 55202, 55203, 55204,
		55344, 55346, 55347, 55348, 55394,
		55529, 55531, 55532, 55533, 55582, 55583, 55714,
		55920, 55935, 55936, 55937, 55938, 55939, 55948, 55949, 55950, 55951,
		56106, 56126, 56127, 56128, 56133, 56152, 56153, 56158:
		return true
	default:
		return false
	}
}

// isConfirmedSenyuMonster prevents QuestVariant3 bit 1 (GSR-to-GR) from
// misclassifying other systems that reuse the bit, notably interception.
func isConfirmedSenyuMonster(monsterID int) bool {
	switch monsterID {
	case 146, // Zinogre
		147, // Deviljho
		148, // Brachydios
		151, // Barioth
		152, // Uragaan
		153, // Stygian Zinogre
		159, // Nargacuga
		162, // Gore Magala
		164, // Shagaru Magala
		165, // Amatsu
		169: // Seregios
		return true
	default:
		return false
	}
}

func huntMetadataFromJSONFile(path string, expectedQuestID uint16) (HuntQuestMetadata, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return invalidHuntQuestMetadata(), false
	}
	var quest struct {
		QuestID       uint16 `json:"quest_id"`
		QuestVariant1 uint8  `json:"quest_variant1"`
		QuestVariant2 uint8  `json:"quest_variant2"`
		QuestVariant3 uint8  `json:"quest_variant3"`
		QuestVariant4 uint8  `json:"quest_variant4"`
		RankBand      uint16 `json:"rank_band"`
		StatTable1    uint32 `json:"stat_table_1"`
		StatTable2    uint8  `json:"stat_table_2"`
	}
	if json.Unmarshal(data, &quest) != nil || quest.QuestID != expectedQuestID {
		return invalidHuntQuestMetadata(), false
	}
	return newHuntQuestMetadata(
		quest.QuestVariant1,
		quest.QuestVariant2,
		quest.QuestVariant3,
		quest.QuestVariant4,
		quest.RankBand,
		quest.StatTable1,
		quest.StatTable2,
	), true
}

func huntMetadataFromBinaryFile(path string, expectedQuestID uint16) (HuntQuestMetadata, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return invalidHuntQuestMetadata(), false
	}
	if len(data) < 4 {
		return invalidHuntQuestMetadata(), false
	}
	data = decryption.UnpackSimple(data)
	if len(data) < 0x62 {
		return invalidHuntQuestMetadata(), false
	}
	main := int(binary.LittleEndian.Uint32(data[:4]))
	if main < 0 || main > len(data)-(questVariant4Offset+1) {
		return invalidHuntQuestMetadata(), false
	}
	if binary.LittleEndian.Uint16(data[main+0x2e:main+0x30]) != expectedQuestID {
		return invalidHuntQuestMetadata(), false
	}
	return newHuntQuestMetadata(
		data[main+questVariant1Offset],
		data[main+questVariant2Offset],
		data[main+questVariant3Offset],
		data[main+questVariant4Offset],
		binary.LittleEndian.Uint16(data[main+0x08:main+0x0a]),
		binary.LittleEndian.Uint32(data[0x48:0x4c]),
		data[0x61],
	), true
}

func newHuntQuestMetadata(variant1, variant2, variant3, variant4 uint8, rankBand uint16, statTable1 uint32, statTable2 uint8) HuntQuestMetadata {
	rankKind := HuntRankHR
	if variant1&questVariant1GRank != 0 {
		rankKind = HuntRankG
	}
	return HuntQuestMetadata{
		RankKind:   rankKind,
		Variant1:   variant1,
		Variant2:   variant2,
		Variant3:   variant3,
		Variant4:   variant4,
		RankBand:   rankBand,
		StatTable1: statTable1,
		StatTable2: statTable2,
		Valid:      true,
	}
}

func invalidHuntQuestMetadata() HuntQuestMetadata {
	return HuntQuestMetadata{RankKind: HuntRankUnknown}
}

func sameHuntQuestMetadata(a, b HuntQuestMetadata) bool {
	return a.RankKind == b.RankKind &&
		a.Variant1 == b.Variant1 &&
		a.Variant2 == b.Variant2 &&
		a.Variant3 == b.Variant3 &&
		a.Variant4 == b.Variant4 &&
		a.RankBand == b.RankBand &&
		a.StatTable1 == b.StatTable1 &&
		a.StatTable2 == b.StatTable2
}

func classifyHuntVariant(variant1, variant3 uint8) HuntVariant {
	switch {
	case variant3&questVariant3Zenith != 0:
		return HuntVariantZenith
	case variant1&questVariant1FixedHC != 0:
		return HuntVariantHardcore
	case variant1&questVariant1HCToUL != 0:
		return HuntVariantHardcoreOptional
	case variant1&questVariant1ULFixed != 0:
		return HuntVariantULFixed
	default:
		return HuntVariantNormal
	}
}
