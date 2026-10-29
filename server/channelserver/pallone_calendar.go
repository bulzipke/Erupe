package channelserver

import (
	"time"

	"erupe-ce/common/mhfearth"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

// Only the opt-in ZZ path uses multiple records. Legacy manual selection and
// pre-ZZ replies retain their existing serialization and behavior.
func (s *Session) handleIndependentPalloneCalendar(pkt *mhfpacket.MsgMhfGetEarthStatus, now time.Time) {
	config := s.server.erupeConfig
	tower, err := s.server.towerEvent(now)
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	status := config.EarthStatus
	if config.TowerRotation.Enabled {
		status = 0
		if tower.Active(now) {
			status = 21
		}
	} else if status == 11 || status == 12 {
		// Explicit festival selection supersedes the old manual festival slot.
		// Publishing both would make the native category lookup pick the old ID.
		status = 0
	}
	primary := mhfearth.Status{Start: uint32(tower.Start.Unix()), End: uint32(tower.End.Unix()), Category: status, EventID: tower.ID}
	copy(primary.Monsters[:], config.EarthMonsters)
	rows := []mhfearth.Status{primary}
	if s.server.automaticPalloneFestival() {
		schedule, err := s.server.palloneSchedule(now)
		if err != nil {
			s.logger.Warn("Pallone calendar unavailable; retaining primary Earth schedule", zap.Error(err))
		} else if schedule.Current.ID != 0 {
			for _, e := range []PalloneEvent{schedule.Current, schedule.Next} {
				rows = append(rows,
					mhfearth.Status{Start: uint32(e.Start.Unix()), End: uint32(e.End.Unix()), Category: 11, EventID: e.ID},
					mhfearth.Status{Start: uint32(e.End.Unix()), End: uint32(e.ClaimUntil.Unix()), Category: 12, EventID: e.ID})
			}
			if previous := schedule.Previous; previous != nil {
				rows = append(rows, mhfearth.Status{Start: uint32(previous.End.Unix()), End: uint32(previous.ClaimUntil.Unix()), Category: 12, EventID: previous.ID})
			}
		}
		s.sendPalloneCalendar(pkt.AckHandle, rows)
		return
	}
	pallone, err := s.server.selectedPalloneEventAt(now)
	if err != nil {
		// A separately selected festival must not take the Tower calendar down.
		// Festival-specific reads/delivery still fail closed on the same error.
		s.logger.Warn("Pallone calendar unavailable; retaining primary Earth schedule", zap.Error(err))
	} else if pallone != nil {
		if !validPalloneWindow(*pallone) {
			s.logger.Warn("Pallone calendar window invalid; retaining primary Earth schedule")
		} else {
			// The native cache is populated at login and does its own date
			// lookup. Publish both windows, including an upcoming festival,
			// so starting/entering claim time does not require a new login.
			rows = append(rows,
				mhfearth.Status{Start: uint32(pallone.Start.Unix()), End: uint32(pallone.End.Unix()), Category: 11, EventID: pallone.ID},
				mhfearth.Status{Start: uint32(pallone.End.Unix()), End: uint32(pallone.ClaimUntil.Unix()), Category: 12, EventID: pallone.ID},
			)
		}
	}
	s.sendPalloneCalendar(pkt.AckHandle, rows)
}

func (s *Session) sendPalloneCalendar(handle uint32, rows []mhfearth.Status) {
	data, err := mhfearth.Encode(rows)
	if err != nil {
		doAckBufFail(s, handle, nil)
		return
	}
	doAckBufSucceed(s, handle, data)
}
