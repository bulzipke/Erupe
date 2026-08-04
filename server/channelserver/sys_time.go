package channelserver

import (
	"erupe-ce/common/gametime"
	"time"
)

// TimeAdjusted, TimeMidnight, TimeWeekStart, TimeWeekNext, and TimeGameAbsolute
// are package-level wrappers around the gametime utility functions, providing
// convenient access to adjusted server time, daily/weekly boundaries, and the
// absolute game timestamp used by the MHF client.

func TimeAdjusted() time.Time   { return gametime.Adjusted() }
func TimeMidnight() time.Time   { return gametime.Midnight() }
func TimeWeekStart() time.Time  { return gametime.WeekStart() }
func TimeWeekNext() time.Time   { return gametime.WeekNext() }
func TimeMonthStart() time.Time { return gametime.MonthStart() }
func TimeGameAbsolute() uint32  { return gametime.GameAbsolute() }

// TimeClientAdjusted returns the real server time unless a debug in-game hour
// override is configured. The override changes only the timestamp sent to the
// client; server schedules and persistence continue to use TimeAdjusted.
func TimeClientAdjusted(overrideHour *int) time.Time {
	now := TimeAdjusted()
	if overrideHour == nil {
		return now
	}
	return gametime.AtGameHour(now, *overrideHour)
}

// TimeGameAbsoluteAdjusted keeps server-side day/night quest selection aligned
// with the clock shown to the client while the debug override is active.
func TimeGameAbsoluteAdjusted(overrideHour *int) uint32 {
	if overrideHour == nil {
		return TimeGameAbsolute()
	}
	return gametime.GameAbsoluteAt(gametime.AtGameHour(TimeAdjusted(), *overrideHour))
}
