package gametime

import (
	"time"
)

const (
	gameCycleSeconds       int64 = 5760
	gameCycleOffsetSeconds int64 = 2160
	gameHoursPerCycle      int64 = 24
)

// Adjusted returns the current time in JST (UTC+9), the timezone used by MHF.
func Adjusted() time.Time {
	baseTime := time.Now().In(time.FixedZone("UTC+9", 9*60*60))
	return time.Date(baseTime.Year(), baseTime.Month(), baseTime.Day(), baseTime.Hour(), baseTime.Minute(), baseTime.Second(), baseTime.Nanosecond(), baseTime.Location())
}

// Midnight returns today's midnight (00:00) in JST.
func Midnight() time.Time {
	baseTime := time.Now().In(time.FixedZone("UTC+9", 9*60*60))
	return time.Date(baseTime.Year(), baseTime.Month(), baseTime.Day(), 0, 0, 0, 0, baseTime.Location())
}

// WeekStart returns the most recent Monday at midnight in JST.
func WeekStart() time.Time {
	midnight := Midnight()
	offset := int(midnight.Weekday()) - int(time.Monday)
	if offset < 0 {
		offset += 7
	}
	return midnight.Add(-time.Duration(offset) * 24 * time.Hour)
}

// WeekNext returns the next Monday at midnight in JST.
func WeekNext() time.Time {
	return WeekStart().Add(time.Hour * 24 * 7)
}

// MonthStart returns the first day of the current month at midnight in JST.
func MonthStart() time.Time {
	midnight := Midnight()
	return time.Date(midnight.Year(), midnight.Month(), 1, 0, 0, 0, 0, midnight.Location())
}

// GameAbsoluteAt returns t's position within the 5760-second (96-minute)
// in-game day/night cycle. One in-game hour lasts 240 real seconds.
func GameAbsoluteAt(t time.Time) uint32 {
	position := (t.Unix() - gameCycleOffsetSeconds) % gameCycleSeconds
	if position < 0 {
		position += gameCycleSeconds
	}
	return uint32(position)
}

// GameAbsolute returns the current position within the 5760-second (96-minute)
// in-game day/night cycle, offset by 2160 seconds.
func GameAbsolute() uint32 {
	return GameAbsoluteAt(Adjusted())
}

// AtGameHour shifts t within its current 96-minute cycle so the client sees the
// requested in-game hour. Invalid hours leave t unchanged.
func AtGameHour(t time.Time, hour int) time.Time {
	if hour < 0 || hour >= int(gameHoursPerCycle) {
		return t
	}

	targetPosition := int64(hour) * gameCycleSeconds / gameHoursPerCycle
	currentPosition := int64(GameAbsoluteAt(t))
	return t.Add(time.Duration(targetPosition-currentPosition) * time.Second)
}
