package mhfpallone

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"golang.org/x/text/encoding/japanese"
)

const (
	questInfoMagic    = 0x1a666e69 // "inf\x1a"
	questInfoG10Head  = 0x78
	questInfoPrefix   = 0xa0
	maxQuestTextBytes = 64 << 10
)

// QuestInfoObjective preserves the native type and last word. For a part-break
// objective the last word is a part ID, rather than a required hunt count.
type QuestInfoObjective struct {
	Type        uint32 `json:"type"`
	Target      uint16 `json:"target"`
	CountOrPart uint16 `json:"count_or_part"`
}

// QuestInfo is recovery evidence, NOT a loadable quest binary/QuestJSON.
// The G10.1 list record retains basic objectives and text, but has no stage
// geometry, monster spawns, stat tables, supply box or full reward tables.
// Raw bytes preserve unknown fields and the original Shift-JIS text exactly.
type QuestInfo struct {
	QuestID      uint16                `json:"quest_id"`
	SourceOffset uint32                `json:"source_offset"`
	PrefixSHA256 string                `json:"prefix_sha256"`
	PrefixHex    string                `json:"prefix_0xa0_hex"`
	Fee          uint32                `json:"fee"`
	ZennyMain    uint32                `json:"zenny_main"`
	ZennyKO      uint32                `json:"zenny_ko"`
	ZennySubA    uint32                `json:"zenny_sub_a"`
	ZennySubB    uint32                `json:"zenny_sub_b"`
	TimeFrames   uint32                `json:"time_frames_30hz"`
	MapID        uint32                `json:"map_id"`
	Restrictions uint16                `json:"restrictions"`
	Objectives   [3]QuestInfoObjective `json:"objectives"`
	Text         [8]string             `json:"text"`
	TextRawHex   [8]string             `json:"text_shift_jis_hex"`
}

type QuestInfoRecovery struct {
	Kind           string      `json:"kind"`
	Required       int         `json:"required_count"`
	Recovered      int         `json:"metadata_recovered_count"`
	MissingIDs     []uint16    `json:"missing_metadata_ids"`
	AbsentSections []string    `json:"absent_quest_body_sections"`
	Quests         []QuestInfo `json:"quests"`
}

// DecodeQuestInfoG10 accepts the same bounded containers as DecodeClientData,
// then checks the G10.1 JP mhfinf header. ZZ has a different header/table layout
// and must not be interpreted with this profile.
func DecodeQuestInfoG10(data []byte) ([]byte, error) {
	data, err := decodeContainer(data)
	if err != nil {
		return nil, err
	}
	if len(data) < questInfoG10Head || binary.LittleEndian.Uint32(data) != questInfoMagic ||
		binary.LittleEndian.Uint32(data[4:]) != 6 || binary.LittleEndian.Uint32(data[12:]) != questInfoG10Head {
		return nil, errors.New("pallone: expected G10.1 JP mhfinf (version 6, header 0x78)")
	}
	return data, nil
}

// RecoverQuestInfoG10 extracts only requested IDs and reports missing metadata
// independently of the stage-file inventory. A recovered list record never
// means that its actual stage quest file is available.
func RecoverQuestInfoG10(data []byte, ids []uint16) (QuestInfoRecovery, error) {
	var err error
	data, err = DecodeQuestInfoG10(data)
	if err != nil {
		return QuestInfoRecovery{}, err
	}
	u16 := func(p uint64) uint16 { return binary.LittleEndian.Uint16(data[p:]) }
	u32 := func(p uint64) uint32 { return binary.LittleEndian.Uint32(data[p:]) }
	valid := func(p, n uint64) bool { return p >= questInfoG10Head && p+n <= uint64(len(data)) }
	countPtr, table := uint64(u32(0x10)), uint64(u32(0x14))
	if !valid(countPtr, 2) {
		return QuestInfoRecovery{}, errors.New("pallone: quest category count outside mhfinf")
	}
	count := uint64(u16(countPtr))
	if count == 0 || count > 1024 || !valid(table, count*8) {
		return QuestInfoRecovery{}, errors.New("pallone: invalid quest category table")
	}
	pointers := make(map[uint16]uint64)
	total := uint64(0)
	for i := uint64(0); i < count; i++ {
		p := table + i*8
		n, array := uint64(u16(p+2)), uint64(u32(p+4))
		total += n
		if total > 65536 || n > 0 && !valid(array, n*4) {
			return QuestInfoRecovery{}, errors.New("pallone: quest pointer array outside mhfinf or too large")
		}
		for j := uint64(0); j < n; j++ {
			q := uint64(u32(array + j*4))
			if q == 0 {
				continue
			}
			if !valid(q, questInfoPrefix) {
				return QuestInfoRecovery{}, errors.New("pallone: quest list record outside mhfinf")
			}
			id := u16(q + 0x2e)
			if _, exists := pointers[id]; exists {
				return QuestInfoRecovery{}, fmt.Errorf("pallone: duplicate mhfinf quest ID %d", id)
			}
			pointers[id] = q
		}
	}
	wanted := make(map[uint16]bool)
	for _, id := range ids {
		if id == 0 {
			return QuestInfoRecovery{}, errors.New("pallone: quest ID zero cannot be recovered")
		}
		wanted[id] = true
	}
	ordered := make([]int, 0, len(wanted))
	for id := range wanted {
		ordered = append(ordered, int(id))
	}
	sort.Ints(ordered)
	result := QuestInfoRecovery{
		Kind: "source_metadata_not_loadable_quest", Required: len(ordered),
		MissingIDs: []uint16{}, Quests: []QuestInfo{},
		AbsentSections: []string{"loaded stages and map geometry", "large and small monster spawns", "monster stat tables and size variation", "supply box", "full reward tables", "gathering and area facilities"},
	}
	for _, id := range ordered {
		q, exists := pointers[uint16(id)]
		if !exists {
			result.MissingIDs = append(result.MissingIDs, uint16(id))
			continue
		}
		prefix := data[q : q+questInfoPrefix]
		v := QuestInfo{
			QuestID: uint16(id), SourceOffset: uint32(q), PrefixSHA256: fmt.Sprintf("%x", sha256.Sum256(prefix)), PrefixHex: hex.EncodeToString(prefix),
			Fee: u32(q + 0x0c), ZennyMain: u32(q + 0x10), ZennyKO: u32(q + 0x14),
			ZennySubA: u32(q + 0x18), ZennySubB: u32(q + 0x1c), TimeFrames: u32(q + 0x20), MapID: u32(q + 0x24), Restrictions: u16(q + 0x2c),
		}
		for j := range v.Objectives {
			o := q + 0x30 + uint64(j)*8
			v.Objectives[j] = QuestInfoObjective{Type: u32(o), Target: u16(o + 4), CountOrPart: u16(o + 6)}
		}
		text := uint64(u32(q + 0x28))
		if !valid(text, 8*4) {
			return QuestInfoRecovery{}, fmt.Errorf("pallone: quest %d text table outside mhfinf", id)
		}
		for j := range v.Text {
			p := uint64(u32(text + uint64(j)*4))
			if p == 0 {
				continue
			}
			if !valid(p, 1) {
				return QuestInfoRecovery{}, fmt.Errorf("pallone: quest %d text pointer outside mhfinf", id)
			}
			end := p + maxQuestTextBytes
			if end > uint64(len(data)) {
				end = uint64(len(data))
			}
			n := bytes.IndexByte(data[p:end], 0)
			if n < 0 {
				return QuestInfoRecovery{}, fmt.Errorf("pallone: quest %d text has no bounded terminator", id)
			}
			raw := data[p : p+uint64(n)]
			v.TextRawHex[j] = hex.EncodeToString(raw)
			v.Text[j], err = japanese.ShiftJIS.NewDecoder().String(string(raw))
			if err != nil {
				return QuestInfoRecovery{}, fmt.Errorf("pallone: quest %d text: %w", id, err)
			}
		}
		result.Quests = append(result.Quests, v)
	}
	result.Recovered = len(result.Quests)
	return result, nil
}
