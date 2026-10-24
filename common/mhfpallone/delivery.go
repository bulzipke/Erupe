package mhfpallone

import "fmt"

// RouteDelivery names a bank-local ordinal, not a catalogue index or score key.
// ZZ FUN_10349bc0 reads only words 3..5 of each RESPONSE_REWARD row: the
// group code (3400 + star), ordinal and group flag. It keeps a signed-short
// cache of at most 12 ordinals plus a negative sentinel per star.
type RouteDelivery struct {
	Star      uint16 `json:"star"`
	Ordinal   uint16 `json:"bank_ordinal"`
	GroupFlag uint16 `json:"group_flag"`
}

func EncodeRouteDelivery(eventID int32, routes []RouteDelivery) ([]byte, error) {
	bf, err := responseHeader(eventID, len(routes), MaxRoutes)
	if err != nil {
		return nil, err
	}
	var groups [RouteBankCount]int
	seen := make(map[[2]uint16]bool)
	for _, r := range routes {
		key := [2]uint16{r.Star, r.Ordinal}
		if r.Star >= RouteBankCount || r.Ordinal >= MaxRoutes || seen[key] || (r.GroupFlag != 0 && r.GroupFlag != 1 && r.GroupFlag != 3) {
			return nil, fmt.Errorf("pallone: invalid/duplicate native route delivery")
		}
		groups[r.Star]++
		if groups[r.Star] > 12 {
			return nil, fmt.Errorf("pallone: native star-group delivery exceeds 12 entries")
		}
		seen[key] = true
		// The menu consumer does not use the first three reward words. Keep
		// them zero; do not invent reward or currency semantics for them.
		bf.WriteUint32(0)
		bf.WriteUint32(0)
		bf.WriteUint32(0)
		bf.WriteUint32(3400 + uint32(r.Star))
		bf.WriteUint32(uint32(r.Ordinal))
		bf.WriteUint32(uint32(r.GroupFlag))
	}
	return bf.Data(), bf.Err()
}
