package config

// Disabled servers retain manual EarthStatus/ID. StartAt is an optional RFC3339
// anchor used on first activation; the persisted schedule survives restarts.
// Defaults: Enabled=true, ActiveDays=21, CycleDays=21 (continuous exploration).
// Each round's unclaimed rewards remain available throughout the next round.
// With an empty StartAt, first activation starts at the most recent Monday noon
// in UTC+9. Set e.g. "2026-10-26T12:00:00+09:00" to select an explicit anchor.
// Existing manual progress is adopted on activation. Only subsequent rounds
// archive/reset floors, passed guardians, antique collections, daily UI data,
// guild Tower mission pages/scores and Tower-donated RP. TR/TRP/TSP, skills,
// items and historical reward receipts remain. Do not mix automatic/manual
// channel processes sharing one DB, or silently change a persisted anchor.
type TowerRotationOptions struct {
	Enabled    bool
	StartAt    string
	ActiveDays int
	CycleDays  int
}
