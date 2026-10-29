package config

import (
	"fmt"
	"time"
)

// PalloneFestivalOptions runs a continuous, independently numbered festival.
// EventID is retained only for compatibility with explicit maintenance plans;
// omit it (zero) for automatic rounds. Disabled preserves manual Earth settings.
type PalloneFestivalOptions struct {
	Enabled    bool
	StartAt    string
	ActiveDays int
	ClaimDays  int // Additional claim time AFTER a round ends; at most one round.
	EventID    int32
}

func (o PalloneFestivalOptions) Validate(mode Mode) error {
	if !o.Enabled {
		return nil
	}
	if mode != ZZ {
		return fmt.Errorf("PalloneFestival currently requires the ZZ client")
	}
	if o.EventID < 0 {
		return fmt.Errorf("PalloneFestival.EventID cannot be negative")
	}
	if o.EventID > 0 {
		return nil
	}
	if o.ActiveDays <= 0 || o.ActiveDays > 364 || o.ActiveDays%7 != 0 || o.ClaimDays <= 0 || o.ClaimDays > o.ActiveDays {
		return fmt.Errorf("PalloneFestival requires ActiveDays in 7-day multiples (7..364) and 0 < ClaimDays <= ActiveDays")
	}
	if o.StartAt != "" {
		t, err := time.Parse(time.RFC3339, o.StartAt)
		if err != nil {
			return fmt.Errorf("PalloneFestival.StartAt: %w", err)
		}
		t = t.In(time.FixedZone("KST", 9*60*60))
		if t.Weekday() != time.Monday || t.Hour() != 12 || t.Minute() != 0 || t.Second() != 0 || t.Nanosecond() != 0 {
			return fmt.Errorf("PalloneFestival.StartAt must be Monday 12:00 in UTC+9")
		}
	}
	return nil
}
