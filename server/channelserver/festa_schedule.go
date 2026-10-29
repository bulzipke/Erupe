package channelserver

import "time"

const (
	festaCycleSeconds = 3 * secsPerWeek
	festaTallySeconds = 60 * 60 // Operator policy for automatic rounds.
)

// ScheduleTimestamps exposes the persisted round's native phase boundaries.
func (e FestaEvent) ScheduleTimestamps() []uint32 { return e.timestamps() }

func (e FestaEvent) timestamps() []uint32 {
	end := e.StartTime + 2*secsPerWeek + festaVotingDuration + festaRewardDuration
	tally := uint32(festaVotingDuration)
	if e.Continuous {
		end = e.StartTime + festaCycleSeconds
		tally = festaTallySeconds
	}
	return []uint32{e.StartTime, e.StartTime + secsPerWeek, e.StartTime + 2*secsPerWeek,
		e.StartTime + 2*secsPerWeek + tally, end}
}

func (e FestaEvent) phase(now time.Time) uint {
	t := now.Unix()
	ts := e.timestamps()
	if e.ID == 0 || t < int64(ts[0]) || t >= int64(ts[4]) {
		return 0
	}
	if t >= int64(ts[3]) {
		return 4
	}
	if t >= int64(ts[2]) {
		return 3
	}
	if t >= int64(ts[1]) {
		return 2
	}
	return 1
}
