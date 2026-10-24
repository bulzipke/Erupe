package mhfpallone

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

const questInfoZZHead = 0x88

// ClientDraft is an offline client-data candidate. The original source bytes
// stay in place except the two mhfinf table pointers, mhfdat bank pointer and
// the selected bank's native relocation count.
// It does not publish an event or establish a route's score/receipt binding.
type ClientDraft struct {
	Data        []byte
	Info        []byte
	Route       Route
	BankOrdinal uint16
}

// BuildClientDraft binds one retained historical route into its active ZZ bank
// and supplies its missing list records from reconstructed daytime bodies.
// Every pre-existing bank record keeps its bytes and ordinal. The historical
// route is appended to a cloned bank; its display code need not be unique.
// Text is copied verbatim; this function does not translate or transcode it.
func BuildClientDraft(dat, inf []byte, number uint16, quests map[uint16][]byte) (ClientDraft, error) {
	var out ClientDraft
	dat, err := DecodeClientData(dat)
	if err != nil {
		return out, err
	}
	routes, err := ParseCatalogue(dat)
	if err != nil {
		return out, err
	}
	active, err := ParseRouteBanks(dat)
	if err != nil {
		return out, err
	}
	// FUN_10af5140 relocates route strings using these explicit counts,
	// independently of the menu's terminators. Appending a record without
	// updating the count leaves that record's pointers unrelocated at load.
	quantities := uint64(binary.LittleEndian.Uint32(dat[0x10:]))
	if quantities < 0x20 || quantities+0x372+RouteBankCount*2 > uint64(len(dat)) ||
		binary.LittleEndian.Uint16(dat[quantities+0x370:]) != RouteBankCount {
		return out, fmt.Errorf("pallone: unsupported ZZ route relocation quantities")
	}
	var bankCounts [RouteBankCount]int
	for _, r := range active {
		bankCounts[r.Star]++
	}
	for star, count := range bankCounts {
		if int(binary.LittleEndian.Uint16(dat[quantities+0x372+uint64(star)*2:])) != count {
			return out, fmt.Errorf("pallone: route bank %d relocation count disagrees with records", star)
		}
	}
	var selected *Route
	for i := range routes {
		if routes[i].Number == number {
			selected = &routes[i]
		}
	}
	if selected == nil || len(quests) != len(selected.StageQuestIDs) {
		return out, fmt.Errorf("pallone: expected exactly the selected historical route's stage bodies")
	}
	// This first profile requires distinct stages. Repeated-stage routes need
	// an explicit list of unique assets rather than silently changing this check.
	seen := make(map[uint16]bool)
	for _, id := range selected.StageQuestIDs {
		if id < 27001 || id > 27523 || seen[id] || len(quests[id]) == 0 {
			return out, fmt.Errorf("pallone: unsupported or missing historical stage %d", id)
		}
		seen[id] = true
	}
	info, err := appendClientQuestInfo(inf, quests)
	if err != nil {
		return out, err
	}
	b := bytes.Clone(dat)
	alignDraft(&b)
	bank := len(b)
	catalogue := int(binary.LittleEndian.Uint32(dat[CatalogueHeaderOffset:]))
	pointers := int(binary.LittleEndian.Uint32(dat[RouteBanksHeaderOffset:]))
	oldBank := int(binary.LittleEndian.Uint32(dat[pointers+int(selected.Star)*4:]))
	ordinal := 0
	for _, route := range active {
		if route.Star == selected.Star {
			ordinal++
		}
	}
	if ordinal == 0 || ordinal >= MaxRoutes {
		return out, fmt.Errorf("pallone: active bank cannot accommodate a positive historical route ordinal")
	}
	// The native submission key is the bank ordinal. Overall ranking also
	// looks up key zero. Appending gives a positive key without displacing
	// ANY later G Caravan route or modifying the historical record itself.
	b = append(b, dat[oldBank:oldBank+ordinal*RouteRecordSize]...)
	record := catalogue + selected.Index*RouteRecordSize
	b = append(b, dat[record:record+RouteRecordSize]...)
	b = append(b, make([]byte, RouteRecordSize)...)
	// Clone the pointer table, since another header could share the old one.
	newPointers := len(b)
	b = append(b, dat[pointers:pointers+RouteBankCount*4]...)
	binary.LittleEndian.PutUint32(b[newPointers+int(selected.Star)*4:], uint32(bank))
	binary.LittleEndian.PutUint32(b[RouteBanksHeaderOffset:], uint32(newPointers))
	binary.LittleEndian.PutUint16(b[quantities+0x372+uint64(selected.Star)*2:], uint16(ordinal+1))
	if len(b) > maxClientDataSize {
		return out, fmt.Errorf("pallone: client draft exceeds size limit")
	}
	if _, err := ParseRouteBanks(b); err != nil {
		return out, err
	}
	route := *selected
	route.Index = ordinal
	out = ClientDraft{Data: b, Info: info, Route: route, BankOrdinal: uint16(ordinal)}
	return out, nil
}

type clientQuestCategory struct {
	upper uint16
	count uint16
	ptr   uint32
}

// DecodeQuestInfoZZ decodes and validates the supported ZZ list container.
func DecodeQuestInfoZZ(source []byte) ([]byte, error) {
	data, err := decodeContainer(source)
	if err != nil {
		return nil, err
	}
	_, err = clientQuestCategories(data)
	return data, err
}

func clientQuestCategories(data []byte) ([]clientQuestCategory, error) {
	if len(data) < questInfoZZHead || len(data) > maxClientDataSize || binary.LittleEndian.Uint32(data) != questInfoMagic ||
		binary.LittleEndian.Uint32(data[4:]) != 6 || binary.LittleEndian.Uint32(data[12:]) != questInfoZZHead {
		return nil, fmt.Errorf("pallone: expected ZZ mhfinf (version 6, header 0x88)")
	}
	valid := func(p, n uint64) bool { return p >= questInfoZZHead && p+n <= uint64(len(data)) }
	cp, table := uint64(binary.LittleEndian.Uint32(data[16:])), uint64(binary.LittleEndian.Uint32(data[20:]))
	if !valid(cp, 2) {
		return nil, fmt.Errorf("pallone: invalid ZZ category count pointer")
	}
	n := uint64(binary.LittleEndian.Uint16(data[cp:]))
	if n == 0 || n > 1024 || !valid(table, (n+1)*8) || !bytes.Equal(data[table+n*8:table+(n+1)*8], make([]byte, 8)) {
		return nil, fmt.Errorf("pallone: invalid ZZ category table or sentinel")
	}
	rows := make([]clientQuestCategory, 0, n)
	for i := uint64(0); i < n; i++ {
		p := table + i*8
		row := clientQuestCategory{binary.LittleEndian.Uint16(data[p:]), binary.LittleEndian.Uint16(data[p+2:]), binary.LittleEndian.Uint32(data[p+4:])}
		if row.upper == 0 || i > 0 && row.upper <= rows[len(rows)-1].upper || row.count > 0 && !valid(uint64(row.ptr), uint64(row.count)*4) {
			return nil, fmt.Errorf("pallone: invalid or unordered ZZ quest category")
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ClientQuestRecordZZ mirrors FUN_1047a820: the first upper bound >= ID,
// followed by slot ID %% 100. Category 27400 therefore holds 273xx, not 274xx.
// Returned records remain backed by data and must be treated as read-only.
func ClientQuestRecordZZ(data []byte, id uint16) ([]byte, error) {
	rows, err := clientQuestCategories(data)
	if err != nil {
		return nil, err
	}
	if id == 0 || id >= 40000 {
		return nil, fmt.Errorf("pallone: invalid ZZ quest ID %d", id)
	}
	for _, row := range rows {
		if row.upper < id {
			continue
		}
		index := id % 100
		if index >= row.count {
			return nil, fmt.Errorf("pallone: quest %d has no ZZ category slot", id)
		}
		p := uint64(binary.LittleEndian.Uint32(data[uint64(row.ptr)+uint64(index)*4:]))
		if p < questInfoZZHead || p+0x140 > uint64(len(data)) || binary.LittleEndian.Uint16(data[p+0x2e:]) != id {
			return nil, fmt.Errorf("pallone: quest %d has no matching ZZ record", id)
		}
		return data[p : p+0x140], nil
	}
	return nil, fmt.Errorf("pallone: quest %d outside ZZ category table", id)
}

func appendClientQuestInfo(source []byte, quests map[uint16][]byte) ([]byte, error) {
	data, err := decodeContainer(source)
	if err != nil {
		return nil, err
	}
	rows, err := clientQuestCategories(data)
	if err != nil {
		return nil, err
	}
	b := bytes.Clone(data)
	newRows := make(map[uint16]int)
	ids := make([]int, 0, len(quests))
	for id := range quests {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	for _, n := range ids {
		id := uint16(n)
		upper := id/100*100 + 100
		// Reusing any existing category is deliberately unsupported in this
		// missing-record restoration, including populated partial categories.
		for _, row := range rows {
			if row.upper == upper {
				return nil, fmt.Errorf("pallone: category %d already exists; refusing replacement", upper)
			}
		}
		array, exists := newRows[upper]
		if !exists {
			alignDraft(&b)
			array = len(b)
			b = append(b, make([]byte, 100*4)...)
			newRows[upper] = array
		}
		body, err := decodeContainer(quests[id])
		if err != nil {
			return nil, err
		}
		q, err := InspectQuestTemplate(body)
		if err != nil || q.EmbeddedID != id || !q.HasGeometry {
			return nil, fmt.Errorf("pallone: invalid reconstructed stage %d", id)
		}
		main := int(binary.LittleEndian.Uint32(body))
		texts, err := clientQuestTexts(body, body[main:main+0x140])
		if err != nil {
			return nil, err
		}
		alignDraft(&b)
		record := len(b)
		b = append(b, body[main:main+0x140]...)
		table := len(b)
		b = append(b, make([]byte, 32)...)
		binary.LittleEndian.PutUint32(b[record+0x28:], uint32(table))
		binary.LittleEndian.PutUint32(b[array+int(id%100)*4:], uint32(record))
		for i, text := range texts {
			binary.LittleEndian.PutUint32(b[table+i*4:], uint32(len(b)))
			b = append(b, text...)
			b = append(b, 0)
		}
	}
	for upper, array := range newRows {
		rows = append(rows, clientQuestCategory{upper, 100, uint32(array)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].upper < rows[j].upper })
	alignDraft(&b)
	count := len(b)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(rows)))
	alignDraft(&b)
	table := len(b)
	for _, row := range rows {
		b = binary.LittleEndian.AppendUint16(b, row.upper)
		b = binary.LittleEndian.AppendUint16(b, row.count)
		b = binary.LittleEndian.AppendUint32(b, row.ptr)
	}
	b = append(b, make([]byte, 8)...)
	binary.LittleEndian.PutUint32(b[16:], uint32(count))
	binary.LittleEndian.PutUint32(b[20:], uint32(table))
	if len(b) > maxClientDataSize {
		return nil, fmt.Errorf("pallone: client quest-list draft exceeds size limit")
	}
	for _, n := range ids {
		if _, err := ClientQuestRecordZZ(b, uint16(n)); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func clientQuestTexts(data, record []byte) ([8][]byte, error) {
	var texts [8][]byte
	p := uint64(binary.LittleEndian.Uint32(record[0x28:]))
	if p < 0x44 || p+32 > uint64(len(data)) {
		return texts, fmt.Errorf("pallone: quest text pointer table outside file")
	}
	for i := range texts {
		start := uint64(binary.LittleEndian.Uint32(data[p+uint64(i)*4:]))
		if start < 0x44 || start >= uint64(len(data)) {
			return texts, fmt.Errorf("pallone: quest text slot %d outside file", i)
		}
		end := start + maxQuestTextBytes
		if end > uint64(len(data)) {
			end = uint64(len(data))
		}
		n := bytes.IndexByte(data[start:end], 0)
		if n < 0 {
			return texts, fmt.Errorf("pallone: quest text slot %d unterminated", i)
		}
		texts[i] = data[start : start+uint64(n)]
	}
	return texts, nil
}
