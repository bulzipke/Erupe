// Package mhfpallone describes Pallone Festival client data.
package mhfpallone

import (
	"encoding/binary"
	"errors"
	"fmt"

	"erupe-ce/common/decryption"
)

const (
	// The ZZ mhfdat table at 0x878 retains the original festival routes.
	// The separate 0x91c table also contains later G Caravan data; its
	// replacement HR/GR routes must not be substituted for these routes.
	CatalogueHeaderOffset  = 0x878
	RouteBanksHeaderOffset = 0x91c
	RouteBankCount         = 11
	RouteRecordSize        = 0x10c
	MaxRoutes              = 200
	stageOffset            = 0x4c
	stageStride            = 0x10
	stageSlots             = 12
	clientDataMagic        = 0x1a66686d // "mhf\x1a"
	jpkMagic               = 0x1a524b4a
	maxClientDataSize      = 128 << 20
)

// Route is a client catalogue record, not a server quest descriptor.
// Index is the ordinal within its source table (within Star for route banks).
// NativeCode is separate from the displayed number and stage quest IDs.
type Route struct {
	Index         int      `json:"index"`
	Star          uint16   `json:"star"`
	Number        uint16   `json:"number"`
	NativeCode    uint16   `json:"native_code"`
	StageQuestIDs []uint16 `json:"stage_quest_ids"`
}

// ParseRouteBanks reads all eleven banks used by ZZ FUN_1041fb30 and
// FUN_108923a0. A bank ends when the next record's encoded star changes;
// neighboring banks are not necessarily separated by a zero record.
// These include later G Caravan replacements and must be audited separately
// from the retained festival catalogue at 0x878.
func ParseRouteBanks(data []byte) ([]Route, error) {
	if len(data) < RouteBanksHeaderOffset+4 || binary.LittleEndian.Uint32(data) != clientDataMagic {
		return nil, errors.New("pallone: expected decoded ZZ mhfdat route banks")
	}
	pointers := uint64(binary.LittleEndian.Uint32(data[RouteBanksHeaderOffset:]))
	if pointers < RouteBanksHeaderOffset+4 || pointers+RouteBankCount*4 > uint64(len(data)) {
		return nil, errors.New("pallone: route bank pointers outside client data")
	}
	var routes []Route
	for star := 0; star < RouteBankCount; star++ {
		table := uint64(binary.LittleEndian.Uint32(data[pointers+uint64(star)*4:]))
		if table < RouteBanksHeaderOffset+4 || table+16 > uint64(len(data)) {
			return nil, fmt.Errorf("pallone: invalid pointer for route bank %d", star)
		}
		count := 0
		for i := 0; i <= MaxRoutes; i++ {
			pos := table + uint64(i)*RouteRecordSize
			if pos+16 > uint64(len(data)) {
				return nil, fmt.Errorf("pallone: unterminated route bank %d", star)
			}
			if binary.LittleEndian.Uint32(data[pos+12:])>>16 != uint32(star+1) {
				break
			}
			if i == MaxRoutes || pos+RouteRecordSize > uint64(len(data)) {
				return nil, fmt.Errorf("pallone: route bank %d exceeds client capacity or is truncated", star)
			}
			route, err := parseRouteRecord(data, pos, i)
			if err != nil {
				return nil, err
			}
			// Active banks are addressed by (star, ordinal), not NativeCode.
			// A historical route appended beside a later G Caravan version
			// can legitimately retain the same displayed/native code.
			routes = append(routes, route)
			count++
		}
		if count == 0 {
			return nil, fmt.Errorf("pallone: empty route bank %d", star)
		}
	}
	return routes, nil
}

// Bounds are checked before calling the existing game's decompressor.
func decodeContainer(data []byte) (decoded []byte, err error) {
	if len(data) > maxClientDataSize {
		return nil, errors.New("pallone: client data exceeds 128 MiB")
	}
	if len(data) >= 4 && binary.LittleEndian.Uint32(data) == decryption.ECDMagic {
		data, err = decryption.DecodeECD(data)
		if err != nil {
			return nil, fmt.Errorf("pallone: decrypt client data: %w", err)
		}
	}
	if len(data) >= 4 && binary.LittleEndian.Uint32(data) == jpkMagic {
		if len(data) < 16 {
			return nil, errors.New("pallone: truncated JPK header")
		}
		kind := binary.LittleEndian.Uint16(data[6:])
		start := binary.LittleEndian.Uint32(data[8:])
		size := binary.LittleEndian.Uint32(data[12:])
		if (kind != 3 && kind != 4) || start < 16 || uint64(start) > uint64(len(data)) || size == 0 || size > maxClientDataSize {
			return nil, errors.New("pallone: invalid or unsupported JPK header")
		}
		defer func() {
			if recover() != nil {
				decoded = nil
				err = errors.New("pallone: malformed JPK client data")
			}
		}()
		data = decryption.UnpackSimple(data)
	}
	return data, nil
}

// DecodeClientData accepts a raw, ECD-encrypted, or JPK-compressed mhfdat file.
func DecodeClientData(data []byte) ([]byte, error) {
	data, err := decodeContainer(data)
	if err != nil {
		return nil, err
	}
	if len(data) < CatalogueHeaderOffset+4 || binary.LittleEndian.Uint32(data) != clientDataMagic {
		return nil, errors.New("pallone: expected a supported ZZ mhfdat file")
	}
	return data, nil
}

// ParseCatalogue reads the retained festival table from decoded ZZ mhfdat.
// It deliberately does not infer a server delivery schedule or score keys
// from this catalogue: those are separate parts of the client protocol.
func ParseCatalogue(data []byte) ([]Route, error) {
	if len(data) < CatalogueHeaderOffset+4 || binary.LittleEndian.Uint32(data) != clientDataMagic {
		return nil, errors.New("pallone: expected decoded ZZ mhfdat")
	}
	table := uint64(binary.LittleEndian.Uint32(data[CatalogueHeaderOffset:]))
	if table < CatalogueHeaderOffset+4 || table+16 > uint64(len(data)) {
		return nil, errors.New("pallone: catalogue pointer outside client data")
	}
	var routes []Route
	seen := make(map[uint16]bool)
	for i := 0; i <= MaxRoutes; i++ {
		pos := table + uint64(i)*RouteRecordSize
		if pos+16 > uint64(len(data)) {
			return nil, errors.New("pallone: catalogue has no terminator")
		}
		header := data[pos : pos+16]
		if binary.LittleEndian.Uint64(header) == 0 && binary.LittleEndian.Uint64(header[8:]) == 0 {
			if len(routes) == 0 {
				return nil, errors.New("pallone: empty retained catalogue")
			}
			return routes, nil
		}
		if i == MaxRoutes || pos+RouteRecordSize > uint64(len(data)) {
			return nil, errors.New("pallone: catalogue exceeds client capacity or is truncated")
		}
		route, err := parseRouteRecord(data, pos, i)
		if err != nil {
			return nil, err
		}
		if seen[route.NativeCode] {
			return nil, fmt.Errorf("pallone: duplicate route code %d", route.NativeCode)
		}
		seen[route.NativeCode] = true
		routes = append(routes, route)
	}
	return nil, errors.New("pallone: catalogue exceeds client capacity")
}

// Callers check the full record bounds before calling this helper.
func parseRouteRecord(data []byte, pos uint64, index int) (Route, error) {
	record := data[pos : pos+RouteRecordSize]
	title := uint64(binary.LittleEndian.Uint32(record))
	code := binary.LittleEndian.Uint32(record[0xc:])
	star := int(code>>16) - 1
	nativeCode := uint16(code)
	number := int(nativeCode) - 10000 - star*1000
	if title == 0 || title >= uint64(len(data)) || star < 0 || star > 10 || number < 1 || number > 99 {
		return Route{}, fmt.Errorf("pallone: invalid route record %d", index)
	}
	route := Route{Index: index, Star: uint16(star), Number: uint16(star*100 + number), NativeCode: nativeCode}
	ended := false
	for j := 0; j < stageSlots; j++ {
		id := binary.LittleEndian.Uint32(record[stageOffset+j*stageStride:])
		if id == 0 {
			ended = true
			continue
		}
		if ended || id > 65535 {
			return Route{}, fmt.Errorf("pallone: invalid stage %d in route %d", j, index)
		}
		route.StageQuestIDs = append(route.StageQuestIDs, uint16(id))
	}
	if !ended || len(route.StageQuestIDs) == 0 {
		return Route{}, fmt.Errorf("pallone: invalid stage list in route %d", index)
	}
	return route, nil
}
