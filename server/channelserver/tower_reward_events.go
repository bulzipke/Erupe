package channelserver

import "time"

type towerRewardEventsRepository interface {
	ClaimableTowerEvents(time.Time) ([]TowerEvent, error)
}

// Previous rounds are read from their immutable event progress and receipts.
// Current-round writes still use ForTowerRound and its lifecycle lock.
func (r *TowerRepository) ClaimableTowerEvents(now time.Time) ([]TowerEvent, error) {
	var events []TowerEvent
	err := r.db.Select(&events, `SELECT e.earth_id,e.starts_at,e.ends_at,e.claim_until
		FROM tower_events e JOIN tower_event_lifecycle l ON l.singleton
		WHERE e.earth_id<=l.earth_id AND e.starts_at<=$1 AND e.claim_until>$1
		ORDER BY e.starts_at,e.earth_id`, now.UTC())
	return events, err
}

func (s *Server) towerRewardEvents(current TowerEvent, now time.Time) ([]TowerEvent, error) {
	if s.erupeConfig.TowerRotation.Enabled {
		if repo, ok := s.towerRepo.(towerRewardEventsRepository); ok {
			return repo.ClaimableTowerEvents(now)
		}
		if !current.Claimable(now) {
			return nil, nil
		}
	}
	return []TowerEvent{current}, nil
}
