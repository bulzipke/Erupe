package channelserver

import "time"

// Optional interface keeps unrelated test/in-memory repositories compatible.
type DivaGuildRankingRepository interface {
	GetDivaGuildRanking(eventID, charID uint32, cutoff time.Time) ([]DivaRank, error)
}

// Keep the final unpublished interval hidden until tallying has finished.
// The first-day 18:00 and ordinary 04:00/12:00/20:00 publications are retained.
func divaSongRankingCutoff(now, start time.Time) time.Time {
	end := start.Add(time.Duration(divaPhaseDuration) * time.Second)
	if !now.Before(end.Add(time.Duration(divaInterlude) * time.Second)) {
		return end
	}
	if !now.Before(end) {
		now = end.Add(-time.Nanosecond)
	}
	return divaRankingCutoff(now, start)
}

func (s *Session) divaGuildRanks() ([]DivaRank, error) {
	repo, ok := s.server.divaRepo.(DivaGuildRankingRepository)
	if !ok {
		return nil, nil
	}
	event, err := s.divaEvent()
	if err != nil || event.ID == 0 {
		return nil, err
	}
	return repo.GetDivaGuildRanking(event.ID, s.charID, divaSongRankingCutoff(TimeAdjusted(), s.divaSongStart(event)))
}

// Resolve the round and cutoff once so a midnight/publication boundary cannot
// combine the personal ranking of one snapshot with another guild snapshot.
func (s *Session) divaMyRanks() ([]DivaRank, []DivaRank, error) {
	event, err := s.divaEvent()
	if err != nil || event.ID == 0 {
		return nil, nil, err
	}
	cutoff := divaSongRankingCutoff(TimeAdjusted(), s.divaSongStart(event))
	personal, err := s.server.divaRepo.GetDivaRanking(event.ID, s.charID, cutoff)
	if err != nil {
		return nil, nil, err
	}
	if repo, ok := s.server.divaRepo.(DivaGuildRankingRepository); ok {
		guild, err := repo.GetDivaGuildRanking(event.ID, s.charID, cutoff)
		return personal, guild, err
	}
	return personal, nil, nil
}

// ZZ 0x11536d30 reads rank, cleared areas, name[32], count and count x
// (rank, cleared areas, name[32]). 0x107a80b0 scans all 100 cached rows,
// and the parser does not clear unused rows. Send all 100 zero rows to
// remove stale/fabricated rankings. This fits its 0xFCA allocation exactly
// (4041 payload bytes + one byte reserved by the transport).
// An interception point total or completed-quest count is NOT an area count.
func divaEmptyTacticsRankingPayload() []byte {
	data := make([]byte, 41+100*40)
	data[40] = 100
	return data
}
