package gametime

import "time"

var jst = time.FixedZone("UTC+9", 9*60*60)

// MezFesWindow is the MezFes event identity and open window sent at login.
// The client keeps per-event ticket usage keyed by ID: a new ID makes it
// reset the used solo/group counts (refilling tickets) while keeping points.
type MezFesWindow struct {
	ID    uint32
	Start uint32
	End   uint32
}

// mezFesDailyOpenSpan keeps a daily event open well past the next reset so a
// player who stays logged in across the reset is never cut off mid-game; the
// refill simply applies on their next login.
const mezFesDailyOpenSpan = 7 * 24 * time.Hour

// MezFesWindowAt returns the MezFes window active at now.
//
// With dailyReset and a valid resetHour (0-23, JST), a new event starts every
// day at resetHour:00, so tickets refill once per day. Otherwise the legacy
// weekly event is used: ID is Monday 00:00 and the window ends at the next
// Monday, starting durationSeconds before it.
func MezFesWindowAt(now time.Time, durationSeconds int, dailyReset bool, resetHour int) MezFesWindow {
	local := now.In(jst)
	if dailyReset && resetHour >= 0 && resetHour < 24 {
		start := time.Date(local.Year(), local.Month(), local.Day(), resetHour, 0, 0, 0, jst)
		if local.Before(start) {
			start = start.AddDate(0, 0, -1)
		}
		return MezFesWindow{
			ID:    uint32(start.Unix()),
			Start: uint32(start.Unix()),
			End:   uint32(start.Add(mezFesDailyOpenSpan).Unix()),
		}
	}
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, jst)
	offset := int(midnight.Weekday()) - int(time.Monday)
	if offset < 0 {
		offset += 7
	}
	weekStart := midnight.AddDate(0, 0, -offset)
	weekNext := weekStart.AddDate(0, 0, 7)
	return MezFesWindow{
		ID:    uint32(weekStart.Unix()),
		Start: uint32(weekNext.Add(-time.Duration(durationSeconds) * time.Second).Unix()),
		End:   uint32(weekNext.Unix()),
	}
}
