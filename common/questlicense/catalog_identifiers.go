package questlicense

import "sort"

// CampaignIDs includes retired/disabled offers so their namespace cannot
// accidentally shadow an unrelated legacy campaign. The result is detached.
func (c *Catalog) CampaignIDs() []uint32 {
	if c == nil {
		return nil
	}
	ids := make([]uint32, 0, len(c.products))
	for id := range c.products {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
