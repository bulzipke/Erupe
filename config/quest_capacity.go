package config

import (
	"fmt"
	"math"
)

const (
	DefaultEventQuestCapacity         = 16384
	MinimumExpandedEventQuestCapacity = 513
	// MaximumEventQuestCapacity is the server wire-count limit. It does not
	// certify that a client has matching storage, menu arrays or lookup bounds.
	MaximumEventQuestCapacity = math.MaxUint16
)

// EffectiveEventQuestCapacity assumes the matching expanded client is installed.
// It provides no stock-512 compatibility, admission check or negotiation.
func (c *Config) EffectiveEventQuestCapacity() (int, error) {
	capacity := c.EventQuestCapacity
	if capacity == 0 {
		capacity = DefaultEventQuestCapacity
	}
	if capacity < MinimumExpandedEventQuestCapacity || capacity > MaximumEventQuestCapacity {
		return 0, fmt.Errorf("EventQuestCapacity must be 0 (expanded default) or %d..%d", MinimumExpandedEventQuestCapacity, MaximumEventQuestCapacity)
	}
	return capacity, nil
}
