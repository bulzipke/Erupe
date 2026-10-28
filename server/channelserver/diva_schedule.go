package channelserver

import "time"

// ScheduleTimestamps exposes the same phase boundaries sent to the game client.
func (e DivaEvent) ScheduleTimestamps() []uint32 { return divaEventTimestamps(e) }

// Schedule policy belongs to the immutable event, not the current server
// configuration: replaying an old receipt must use that round's old deadlines.
func (e DivaEvent) phaseOffsets() [6]int64 {
	if e.Continuous {
		return [6]int64{0, secsPerWeek, secsPerWeek, 2 * secsPerWeek, 2 * secsPerWeek, 3 * secsPerWeek}
	}
	return [6]int64{0, divaPhaseDuration, divaPhaseDuration + divaInterlude,
		divaPhaseDuration + divaWeekDuration, divaPhaseDuration + divaWeekDuration + divaInterlude,
		divaPhaseDuration + 2*divaWeekDuration}
}

func (e DivaEvent) phaseTime(index int) time.Time {
	return time.Unix(int64(e.StartTime)+e.phaseOffsets()[index], 0)
}

func (e DivaEvent) prayerDuration() int64 { return e.phaseOffsets()[1] }

func (e DivaEvent) lifespan() int64 {
	if e.Continuous {
		return 3 * secsPerWeek
	}
	return divaTotalLifespan
}

func divaEventTimestamps(e DivaEvent) []uint32 {
	result := make([]uint32, 6)
	for i, offset := range e.phaseOffsets() {
		result[i] = uint32(int64(e.StartTime) + offset)
	}
	return result
}

func (e DivaEvent) songRankingCutoff(now time.Time) time.Time {
	if !e.Continuous {
		return divaSongRankingCutoff(now, e.phaseTime(0))
	}
	if !now.Before(e.phaseTime(1)) {
		return e.phaseTime(1)
	}
	return divaRankingCutoff(now, e.phaseTime(0))
}

// Monday noon in UTC+9, including the previous week before Monday noon.
func rotationMondayNoon(now time.Time) time.Time {
	t := now.In(divaLocation)
	start := time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, divaLocation)
	start = start.AddDate(0, 0, -(int(t.Weekday())+6)%7)
	if now.Before(start) {
		start = start.AddDate(0, 0, -7)
	}
	return start
}

// Bootstrap at Monday noon; subsequent rounds retain the persisted three-week
// anchor after downtime. Historical legacy rounds retain their own deadlines.
func divaNextNormalStart(event DivaEvent, now time.Time) time.Time {
	if event.ID == 0 || !event.Continuous {
		if event.ID != 0 && now.Equal(event.phaseTime(5)) {
			return event.phaseTime(5)
		}
		return rotationMondayNoon(now)
	}
	end := event.phaseTime(5)
	cycle := time.Duration(3*secsPerWeek) * time.Second
	return end.Add(now.Sub(end) / cycle * cycle)
}
