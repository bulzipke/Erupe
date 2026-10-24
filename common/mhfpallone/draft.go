package mhfpallone

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// DraftSupply and DraftRewardItem contain evidenced numeric item data only.
// They do not infer native table identities from a translated heading.
type DraftSupply struct {
	Item     uint16 `json:"item"`
	Quantity uint16 `json:"quantity"`
}

type DraftRewardItem struct {
	Rate     uint16 `json:"rate"`
	Item     uint16 `json:"item"`
	Quantity uint16 `json:"quantity"`
}

// DraftRewardPool reuses an existing native table's complete four-byte header,
// including unknown flags. Only its item list and absolute pointer change.
type DraftRewardPool struct {
	TemplateTableID uint8             `json:"template_table_id"`
	Items           []DraftRewardItem `json:"items"`
}

// DraftSpawn borrows all 60 bytes of a same-map monster's native spawn slot.
// This includes unparsed timing/condition bytes; it is not original provenance.
type DraftSpawn struct {
	MonsterID uint8
	Source    []byte
}

type DraftOptions struct {
	Reference ReferenceStats
	Night     bool
	Supplies  [3][]DraftSupply
	Rewards   []DraftRewardPool
	AddSpawns []DraftSpawn
}

type DraftReport struct {
	Kind                  string   `json:"kind"`
	QuestID               uint16   `json:"quest_id"`
	ListMapID             uint32   `json:"original_list_map_id"`
	VariantMapID          uint32   `json:"variant_map_id"`
	TemplateSHA256        string   `json:"template_sha256"`
	DecodedTemplateSHA256 string   `json:"decoded_template_sha256"`
	OutputSHA256          string   `json:"output_sha256"`
	LargeMonsters         []uint16 `json:"large_monster_ids"`
	AddedSpawnSHA256      []string `json:"added_spawn_source_sha256"`
	Playable              bool     `json:"live_client_verified"`
	Limits                []string `json:"limits"`
}

// BuildQuestDraft creates an offline ZZ reconstruction, never a verified retail
// quest. Append-only item/text sections preserve geometry, gathering, facilities,
// transitions and all other unknown template bytes. G10 list metadata is mapped
// explicitly: its +0x8c rank points are NOT ZZ's +0x8c variant fields.
func BuildQuestDraft(source []byte, q QuestInfo, opts DraftOptions) ([]byte, DraftReport, error) {
	var report DraftReport
	template, err := InspectQuestTemplate(source)
	if err != nil {
		return nil, report, err
	}
	expectedMap, mapOK := draftVariantMap(q.MapID, opts.Night)
	if q.QuestID < 27001 || q.QuestID > 27523 || !mapOK || expectedMap != template.MapID || !template.HasGeometry {
		return nil, report, fmt.Errorf("pallone: draft requires original festival ID and same-map geometry")
	}
	ref := opts.Reference
	if ref.QuestID != q.QuestID || !ref.CoreAgrees || len(ref.Conflicts) != 0 || len(ref.UnknownRoster) != 0 {
		return nil, report, fmt.Errorf("pallone: draft requires agreeing, fully mapped historical evidence")
	}
	prefix, err := validatedQuestPrefix(q)
	if err != nil {
		return nil, report, err
	}
	decoded, err := decodeContainer(source)
	if err != nil {
		return nil, report, err
	}
	b := bytes.Clone(decoded)
	main := int(binary.LittleEndian.Uint32(b))
	// The common prefix ends before G10's compact rank-point/focus-item tail.
	copy(b[main:main+0x8c], prefix[:0x8c])
	// List metadata names the daytime map. Retail n* bodies use the paired
	// night map, including their own map sections and transitions.
	binary.LittleEndian.PutUint32(b[main+0x24:], expectedMap)
	for i, off := range []int{0x4c, 0x54, 0x58} {
		v := binary.LittleEndian.Uint32(prefix[0x8c+i*4:])
		binary.LittleEndian.PutUint32(b[off:], v)
		binary.LittleEndian.PutUint32(b[main+0xa4+i*4:], v)
	}
	copy(b[main+0xb0:main+0xb6], prefix[0x98:0x9e])
	binary.LittleEndian.PutUint16(b[0x44:], ref.Size)
	binary.LittleEndian.PutUint16(b[0x46:], ref.SizeRange)
	binary.LittleEndian.PutUint32(b[0x48:], ref.StatLarge)
	b[0x61] = ref.StatSmall

	// Keep original Shift-JIS bytes. Empty fields still get a valid NUL pointer,
	// as Erupe's quest-list reader expects eight readable strings.
	alignDraft(&b)
	textTable := len(b)
	b = append(b, make([]byte, 32)...)
	for i, encoded := range q.TextRawHex {
		raw, err := hex.DecodeString(encoded)
		if err != nil || len(raw) > maxQuestTextBytes || bytes.IndexByte(raw, 0) >= 0 {
			return nil, report, fmt.Errorf("pallone: invalid source text slot %d", i)
		}
		binary.LittleEndian.PutUint32(b[textTable+i*4:], uint32(len(b)))
		b = append(b, raw...)
		b = append(b, 0)
	}
	binary.LittleEndian.PutUint32(b[main+0x28:], uint32(textTable))

	alignDraft(&b)
	binary.LittleEndian.PutUint32(b[8:], uint32(len(b)))
	for i, capacity := range []int{24, 8, 8} {
		if len(opts.Supplies[i]) > capacity {
			return nil, report, fmt.Errorf("pallone: supply section %d exceeds %d slots", i, capacity)
		}
		for j := 0; j < capacity; j++ {
			var entry DraftSupply
			if j < len(opts.Supplies[i]) {
				entry = opts.Supplies[i][j]
				if entry.Item == 0 || entry.Item == 0xffff || entry.Quantity == 0 {
					return nil, report, fmt.Errorf("pallone: invalid supply item")
				}
			}
			b = binary.LittleEndian.AppendUint16(b, entry.Item)
			b = binary.LittleEndian.AppendUint16(b, entry.Quantity)
		}
	}
	if err := appendDraftRewards(&b, decoded, opts.Rewards); err != nil {
		return nil, report, err
	}
	report.AddedSpawnSHA256 = []string{}
	for _, spawn := range opts.AddSpawns {
		if err := addDraftSpawn(b, expectedMap, spawn); err != nil {
			return nil, report, err
		}
		report.AddedSpawnSHA256 = append(report.AddedSpawnSHA256, fmt.Sprintf("%x", sha256.Sum256(spawn.Source)))
	}
	result, err := InspectQuestTemplate(b)
	if err != nil {
		return nil, report, err
	}
	for _, id := range ref.LargeMonsters {
		found := false
		for _, present := range result.LargeMonsters {
			found = found || id == present
		}
		if !found {
			return nil, report, fmt.Errorf("pallone: missing historical monster %d", id)
		}
	}
	mainObjective := canonicalObjective(q.Objectives[0])
	if mainObjective.Type == 1 || mainObjective.Type == 0x101 || mainObjective.Type == 0x201 {
		found := false
		for _, id := range result.LargeMonsters {
			found = found || id == mainObjective.Target
		}
		if !found {
			return nil, report, fmt.Errorf("pallone: main hunt target absent")
		}
	}
	report.Kind, report.QuestID = "reconstructed_quest_draft_not_original", q.QuestID
	report.ListMapID, report.VariantMapID = q.MapID, expectedMap
	report.TemplateSHA256, report.DecodedTemplateSHA256 = template.SourceSHA256, template.DecodedSHA256
	report.OutputSHA256, report.LargeMonsters = fmt.Sprintf("%x", sha256.Sum256(b)), result.LargeMonsters
	report.Limits = []string{
		"Original route, list prefix, objectives and text retained; G10 rank points/focus items mapped to ZZ fields.",
		"Geometry, gathering, facilities, transitions and unknown body fields are borrowed from the named variant.",
		"Added spawn records retain their donor conditions; original conditional appearances are not recovered.",
		"Supply lists and reward weights are secondary historical evidence; native table header flags are borrowed.",
		"Unknown stat-variation and later ZZ-only fields remain from the donor; original equivalence is unverified.",
		"Live departure, delivery, transitions, scoring and rewards require client testing; no event is enabled.",
	}
	return b, report, nil
}

func draftVariantMap(listMap uint32, night bool) (uint32, bool) {
	if !night {
		return listMap, true
	}
	// Only the four explicitly reviewed route-001 map pairs are supported.
	// Numbers agree with retail d*/n* bodies and misc/MapIDs.hexpat; names in
	// that external pattern are not used to translate quest location text.
	pairs := map[uint32]uint32{6: 20, 11: 21, 45: 46, 57: 58}
	v, ok := pairs[listMap]
	return v, ok
}

func validatedQuestPrefix(q QuestInfo) ([]byte, error) {
	p, err := hex.DecodeString(q.PrefixHex)
	if err != nil || len(p) != questInfoPrefix || fmt.Sprintf("%x", sha256.Sum256(p)) != q.PrefixSHA256 {
		return nil, fmt.Errorf("pallone: original prefix hash/length mismatch")
	}
	if binary.LittleEndian.Uint16(p[0x2e:]) != q.QuestID || binary.LittleEndian.Uint32(p[0x24:]) != q.MapID ||
		binary.LittleEndian.Uint32(p[0xc:]) != q.Fee || binary.LittleEndian.Uint32(p[0x10:]) != q.ZennyMain ||
		binary.LittleEndian.Uint32(p[0x14:]) != q.ZennyKO || binary.LittleEndian.Uint32(p[0x18:]) != q.ZennySubA ||
		binary.LittleEndian.Uint32(p[0x1c:]) != q.ZennySubB || binary.LittleEndian.Uint32(p[0x20:]) != q.TimeFrames ||
		binary.LittleEndian.Uint16(p[0x2c:]) != q.Restrictions {
		return nil, fmt.Errorf("pallone: original prefix differs from metadata")
	}
	for i, obj := range q.Objectives {
		o := p[0x30+i*8:]
		if binary.LittleEndian.Uint32(o) != obj.Type || binary.LittleEndian.Uint16(o[4:]) != obj.Target || binary.LittleEndian.Uint16(o[6:]) != obj.CountOrPart {
			return nil, fmt.Errorf("pallone: original objective differs from metadata")
		}
	}
	return p, nil
}

func alignDraft(b *[]byte) {
	for len(*b)%4 != 0 {
		*b = append(*b, 0)
	}
}

func appendDraftRewards(out *[]byte, original []byte, pools []DraftRewardPool) error {
	if len(pools) == 0 || len(pools) > 16 {
		return fmt.Errorf("pallone: expected 1..16 evidenced reward pools")
	}
	headers := make(map[uint8][4]byte)
	p := uint64(binary.LittleEndian.Uint32(original[12:]))
	for count := 0; ; count++ {
		if p < 0x86 || p+2 > uint64(len(original)) || count > 64 {
			return fmt.Errorf("pallone: invalid template reward header array")
		}
		if binary.LittleEndian.Uint16(original[p:]) == 0xffff {
			break
		}
		if p+8 > uint64(len(original)) {
			return fmt.Errorf("pallone: truncated template reward header")
		}
		id := original[p]
		if _, exists := headers[id]; exists {
			return fmt.Errorf("pallone: duplicate template reward table %d", id)
		}
		headers[id] = [4]byte(original[p : p+4])
		p += 8
	}
	alignDraft(out)
	start := len(*out)
	*out = append(*out, make([]byte, len(pools)*8+2)...)
	binary.LittleEndian.PutUint32((*out)[12:], uint32(start))
	binary.LittleEndian.PutUint16((*out)[start+len(pools)*8:], 0xffff)
	seen := make(map[uint8]bool)
	for i, pool := range pools {
		header, ok := headers[pool.TemplateTableID]
		if !ok || seen[pool.TemplateTableID] || len(pool.Items) == 0 || len(pool.Items) > 256 {
			return fmt.Errorf("pallone: invalid/unavailable template reward table %d", pool.TemplateTableID)
		}
		seen[pool.TemplateTableID] = true
		entry := start + i*8
		copy((*out)[entry:entry+4], header[:])
		binary.LittleEndian.PutUint32((*out)[entry+4:], uint32(len(*out)))
		total := 0
		for _, item := range pool.Items {
			if item.Rate == 0 || item.Rate > 100 || item.Item == 0 || item.Item == 0xffff || item.Quantity == 0 {
				return fmt.Errorf("pallone: invalid historical reward item")
			}
			total += int(item.Rate)
			*out = binary.LittleEndian.AppendUint16(*out, item.Rate)
			*out = binary.LittleEndian.AppendUint16(*out, item.Item)
			*out = binary.LittleEndian.AppendUint16(*out, item.Quantity)
		}
		if total != 100 {
			return fmt.Errorf("pallone: historical reward rates sum to %d", total)
		}
		*out = binary.LittleEndian.AppendUint16(*out, 0xffff)
	}
	return nil
}

func draftSpawnArrays(b []byte) (ids, spawns int, err error) {
	if len(b) < 0x86 {
		return 0, 0, fmt.Errorf("pallone: truncated monster source")
	}
	p := uint64(binary.LittleEndian.Uint32(b[0x18:]))
	if p < 0x86 || p+16 > uint64(len(b)) {
		return 0, 0, fmt.Errorf("pallone: missing monster pointer block")
	}
	i, s := uint64(binary.LittleEndian.Uint32(b[p+8:])), uint64(binary.LittleEndian.Uint32(b[p+12:]))
	if i < 0x86 || i+20 > uint64(len(b)) || s < 0x86 || s+300 > uint64(len(b)) || i < s+300 && s < i+20 {
		return 0, 0, fmt.Errorf("pallone: invalid/overlapping monster arrays")
	}
	return int(i), int(s), nil
}

func addDraftSpawn(out []byte, mapID uint32, spawn DraftSpawn) error {
	info, err := InspectQuestTemplate(spawn.Source)
	if err != nil || info.MapID != mapID || spawn.MonsterID == 0 || spawn.MonsterID == 0xff {
		return fmt.Errorf("pallone: additional monster requires same-map source")
	}
	donor, err := decodeContainer(spawn.Source)
	if err != nil {
		return err
	}
	ids, target, err := draftSpawnArrays(out)
	if err != nil {
		return err
	}
	_, source, err := draftSpawnArrays(donor)
	if err != nil {
		return err
	}
	free, found := -1, -1
	for i := 0; i < 5; i++ {
		if out[target+i*60] == spawn.MonsterID {
			return fmt.Errorf("pallone: monster %d already present", spawn.MonsterID)
		}
		if free < 0 && out[target+i*60] == 0xff {
			free = i
		}
		if found < 0 && donor[source+i*60] == spawn.MonsterID {
			found = i
		}
	}
	if free < 0 || found < 0 {
		return fmt.Errorf("pallone: no free slot or missing source monster %d", spawn.MonsterID)
	}
	// Monster stage IDs are area IDs, not the four player-camp spawn IDs at
	// header +4. Some retail quests leave their minion/map-section arrays null
	// and use the map defaults. Require an area already used by an existing
	// native large-monster slot instead of interpreting a null array as no map.
	stage := binary.LittleEndian.Uint32(donor[source+found*60+8:])
	stageFound := false
	for i := 0; i < 5; i++ {
		stageFound = stageFound || out[target+i*60] != 0xff && binary.LittleEndian.Uint32(out[target+i*60+8:]) == stage
	}
	if !stageFound {
		return fmt.Errorf("pallone: source monster stage %d is not used by a destination monster", stage)
	}
	copy(out[target+free*60:target+(free+1)*60], donor[source+found*60:source+(found+1)*60])
	binary.LittleEndian.PutUint32(out[ids+free*4:], uint32(spawn.MonsterID))
	return nil
}
