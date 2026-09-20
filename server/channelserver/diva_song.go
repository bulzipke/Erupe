package channelserver

import (
	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
	"time"
)

var divaLocation = time.FixedZone("UTC+9", 9*60*60)

// Beginning of the daily choice window containing t.
func divaNoon(t time.Time) time.Time {
	t = t.In(divaLocation)
	noon := time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, divaLocation)
	if t.Before(noon) {
		noon = noon.Add(-24 * time.Hour)
	}
	return noon
}

// Publication times requested for the song phase. Saves themselves are immediate.
func divaRankingCutoff(now, start time.Time) time.Time {
	now = now.In(divaLocation)
	start = start.In(divaLocation)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, divaLocation)
	cutoff := midnight.Add(-4 * time.Hour) // Yesterday 20:00.
	firstDay := now.Year() == start.Year() && now.YearDay() == start.YearDay()
	for _, hour := range []int{4, 12, 18, 20} {
		// Even a midnight/debug anchor publishes its first result at 18:00.
		// Later days use only the ordinary 04:00/12:00/20:00 schedule.
		if (firstDay && hour < 18) || (!firstDay && hour == 18) {
			continue
		}
		t := midnight.Add(time.Duration(hour) * time.Hour)
		if !t.After(now) {
			cutoff = t
		}
	}
	if cutoff.Before(start) {
		return start
	}
	return cutoff
}

func divaDisplayPoints(n int64) uint32 {
	if n < 0 {
		return 0
	}
	if n > 0x7fffffff {
		return 0x7fffffff
	} // The UI formats a signed integer.
	return uint32(n)
}

// ZZ 0x11534740: status byte + 8 days, each containing TWO (color,base,bonus)
// triplets. The second color is not a half-day flag, nor are the points duplicates.
func divaMyPointPayload(days []DivaDay, firstDay time.Time) []byte {
	var slots [8]DivaDay
	for _, d := range days {
		i := int(d.Day.Sub(firstDay) / (24 * time.Hour))
		if d.Day.Before(firstDay) || i < 0 || i >= len(slots) {
			continue
		}
		slots[i] = d
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0)
	for _, d := range slots {
		bf.WriteUint8(uint8(d.Color))
		bf.WriteUint32(divaDisplayPoints(d.Quest))
		bf.WriteUint32(divaDisplayPoints(d.Bonus))
		bf.WriteUint8(uint8(d.SecondColor))
		bf.WriteUint32(divaDisplayPoints(d.SecondQuest))
		bf.WriteUint32(divaDisplayPoints(d.SecondBonus))
	}
	return bf.Data()
}

// The active ZZ screen (0x103b4a50) requests kinds 0 and 2: personal and
// guild. Both use 0x11535ed0's 100 x 31-byte parser. Its first byte is
// reserved; byte 1 is the displayed rank (0x103b4c70), NOT a uint16.
// The unused kind 1/3 paths have a 47-byte parser; keep those lists empty
// until their meaning is established rather than inventing past rankings.
func divaRankingPayload(kind uint8, ranks []DivaRank) []byte {
	bf := byteframe.NewByteFrame()
	for i := 0; i < 100; i++ {
		for len(ranks) > 0 && (ranks[0].Rank == 0 || ranks[0].Rank > 100 || ranks[0].Points <= 0) {
			ranks = ranks[1:]
		}
		if (kind == 0 || kind == 2) && len(ranks) > 0 {
			r := ranks[0]
			ranks = ranks[1:]
			bf.WriteUint8(0)
			bf.WriteUint8(uint8(min(r.Rank, 100)))
			bf.WriteBytes(stringsupport.PaddedString(r.Name, 25, true))
			bf.WriteUint32(divaDisplayPoints(r.Points))
		} else if kind%2 == 1 {
			bf.WriteBytes(make([]byte, 47))
		} else {
			bf.WriteBytes(make([]byte, 31))
		}
	}
	return bf.Data()
}

func divaMyRankingPayload(ranks []DivaRank, charID uint32) []byte {
	return divaMyRankingWithGuildPayload(ranks, nil, charID)
}

func divaMyRankingWithGuildPayload(ranks, guildRanks []DivaRank, charID uint32) []byte {
	bf := byteframe.NewByteFrame()
	var own DivaRank
	for _, r := range ranks {
		if r.CharID == charID {
			own = r
			break
		}
	}
	bf.WriteUint32(own.Rank)
	bf.WriteUint32(0) // Native stores this field but its meaning is unconfirmed.
	bf.WriteUint32(divaDisplayPoints(own.Points))
	var guild DivaRank
	for _, r := range guildRanks {
		if r.IsOwn {
			guild = r
			break
		}
	}
	bf.WriteUint32(guild.Rank)
	bf.WriteUint32(0)
	bf.WriteUint32(divaDisplayPoints(guild.Points))
	bf.WriteBytes(stringsupport.PaddedString(guild.Name, 25, true))
	return bf.Data()
}

func (s *Session) divaEvent() (DivaEvent, error) {
	if s.server.erupeConfig.DebugOptions.DivaOverride == 1 {
		return s.server.divaRepo.EnsureDivaSongEvent(TimeAdjusted())
	}
	if repo, ok := s.server.divaRepo.(DivaEventLifecycleRepository); ok {
		return repo.EnsureDivaEvent(TimeAdjusted(), s.server.erupeConfig.DebugOptions.DivaOverride)
	}
	events, err := s.server.divaRepo.GetEvents()
	if err != nil || len(events) == 0 {
		return DivaEvent{}, err
	}
	return events[len(events)-1], nil
}

func (s *Session) divaSongStart(event DivaEvent) time.Time {
	return time.Unix(int64(event.StartTime), 0).In(divaLocation)
}

func (s *Session) divaPersonalRanks() ([]DivaRank, error) {
	event, err := s.divaEvent()
	if err != nil || event.ID == 0 {
		return nil, err
	}
	return s.server.divaRepo.GetDivaRanking(event.ID, s.charID, divaSongRankingCutoff(TimeAdjusted(), s.divaSongStart(event)))
}
