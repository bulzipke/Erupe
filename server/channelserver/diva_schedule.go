package channelserver

import "time"

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

// Keep the first new prayer phase whole when upgrading during an old idle
// period. Never invent a backdated interception that would expire old rewards.
// Subsequent rounds remain anchored even after downtime or delayed requests.
func divaNextNormalStart(event DivaEvent, now time.Time) time.Time {
	if event.ID == 0 || !event.Continuous {
		if event.ID != 0 && now.Equal(event.phaseTime(5)) {
			return event.phaseTime(5)
		}
		return now.Truncate(time.Second)
	}
	end := event.phaseTime(5)
	cycle := time.Duration(3*secsPerWeek) * time.Second
	return end.Add(now.Sub(end) / cycle * cycle)
}
