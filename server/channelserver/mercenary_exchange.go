package channelserver

import (
	"encoding/binary"
	"strings"
	"time"
)

const mercenaryConsentDuration = 45 * time.Second

type mercenaryConsent struct {
	peer    *Session
	expires time.Time
}

func (s *Session) mercenaryBar() *Stage {
	s.Lock()
	defer s.Unlock()
	if s.stage != nil && len(s.stage.id) >= 3 && !s.closed.Load() && strings.HasPrefix(s.stage.id[3:], "Ns211p") {
		return s.stage
	}
	return nil
}

// The client routes its Rasta Bar seat exchange through a targeted data cast. The
// request/response handshake starts with 0x1d. The contract conversation
// uses 0x1e and a native command, size, and status byte at offsets 12..14.
func mercenaryExchangeType(data []byte) byte {
	if len(data) >= 12 && binary.BigEndian.Uint16(data[:2]) == 2 &&
		(data[2] == 0x1d || data[2] == 0x1e) &&
		int(binary.LittleEndian.Uint16(data[8:10])) == len(data)-12 {
		return data[2]
	}
	return 0
}

// Remember the borrower's affirmative reply on the lending session. Only the
// authenticated sender of the reply can authorize this specific peer.
// The caller holds peer's session lock while queuing the reply.
func (s *Session) noteMercenaryReply(peer *Session, data []byte, now time.Time) {
	if len(data) != 20 {
		return
	}
	command, size, status := data[12], data[13], data[14]
	if size != byte(len(data)-12) {
		return
	}
	switch {
	case command == 3 && status == 0:
		peer.mercenaryConsent = mercenaryConsent{peer: s, expires: now.Add(mercenaryConsentDuration)}
	case command == 1 || command == 3:
		if peer.mercenaryConsent.peer == s {
			peer.mercenaryConsent = mercenaryConsent{}
		}
	}
}

func (s *Session) hasMercenaryConsent(charID uint32, now time.Time) bool {
	bar := s.mercenaryBar()
	if bar == nil {
		return false
	}
	s.Lock()
	consent := s.mercenaryConsent
	s.Unlock()
	return consent.peer != nil && consent.peer.charID == charID && now.Before(consent.expires) &&
		consent.peer.mercenaryBar() == bar
}
