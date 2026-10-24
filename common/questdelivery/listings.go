package questdelivery

// MissingListingQuestIDs returns the enabled managed IDs absent from a supplied
// inventory of intended listing sources. It is an offline structural check;
// an inventory entry does not prove native menu visibility or eligibility.
// Include automatic providers and any verified native sources in the inventory.
// Disabled IDs require no listing source. The returned slice is independent.
func (c *Catalog) MissingListingQuestIDs(availableIDs []int) []int {
	available := make(map[int]struct{}, len(availableIDs))
	for _, id := range availableIDs {
		available[id] = struct{}{}
	}
	missing := make([]int, 0)
	for _, id := range c.questIDs {
		if _, found := available[id]; !c.quests[id].disabled && !found {
			missing = append(missing, id)
		}
	}
	return missing
}
