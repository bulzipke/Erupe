package channelserver

import (
	"encoding/binary"
	"strings"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/binpacket"
	"erupe-ce/network/mhfpacket"
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

// The client announces its seat via a stage cast and answers via a targeted cast.
// Both use subtype 0x1d. The contract messages use 0x1e, with command, size,
// and status bytes at offsets 12..14.
func mercenaryExchangeType(data []byte) byte {
	if len(data) >= 12 && binary.BigEndian.Uint16(data[:2]) == 2 &&
		(data[2] == 0x1d || data[2] == 0x1e) &&
		int(binary.LittleEndian.Uint16(data[8:10])) == len(data)-12 {
		return data[2]
	}
	return 0
}

type mercenaryCast struct {
	message  *mhfpacket.MsgSysCastedBinary
	contract bool
}

type mercenaryCastGroup struct {
	receiving bool
	bar       *Stage
	peers     map[*Session][]mercenaryCast
}

func (s *Session) routeMercenaryCast(resp *mhfpacket.MsgSysCastedBinary, targeted *binpacket.MsgBinTargeted) bool {
	var exchange byte
	if resp.MessageType == BinaryMessageTypeData {
		exchange = mercenaryExchangeType(resp.RawDataPayload)
	}
	seatAnnouncement := exchange == 0x1d && resp.BroadcastType == BroadcastTypeStage
	reply := exchange != 0 && targeted != nil
	state := resp.MessageType == BinaryMessageTypeState &&
		(resp.BroadcastType == BroadcastTypeServer || resp.BroadcastType == BroadcastTypeStage)
	if !seatAnnouncement && !reply && !state {
		return false
	}
	bar := s.mercenaryBar()
	if bar == nil {
		return reply
	}
	cast := mercenaryCast{message: resp, contract: exchange == 0x1e}
	if reply {
		if len(targeted.TargetCharIDs) == 1 && targeted.TargetCharIDs[0] != s.charID {
			peer := s.server.FindSessionByCharID(targeted.TargetCharIDs[0])
			if peer != nil && peer.mercenaryBar() == bar {
				s.addMercenaryCast(bar, peer, cast)
			}
		}
		return true
	}

	if resp.BroadcastType == BroadcastTypeStage {
		for _, peer := range bar.sessionsExcept(s) {
			s.addMercenaryCast(bar, peer, cast)
		}
	} else {
		for _, peer := range s.server.sessionSnapshot() {
			if peer == s {
				continue
			}
			if peer.mercenaryBar() == bar {
				s.addMercenaryCast(bar, peer, cast)
			} else {
				peer.QueueSendMHFNonBlocking(resp)
			}
		}
	}
	return true
}

func (s *Session) addMercenaryCast(bar *Stage, peer *Session, cast mercenaryCast) {
	group := &s.mercenaryCasts
	if !group.receiving {
		s.queueMercenaryCasts(bar, peer, []mercenaryCast{cast})
		return
	}
	if group.peers == nil {
		group.bar = bar
		group.peers = make(map[*Session][]mercenaryCast)
	}
	group.peers[peer] = append(group.peers[peer], cast)
}

func (s *Session) flushMercenaryCasts() {
	group := &s.mercenaryCasts
	for peer, casts := range group.peers {
		if !s.closed.Load() {
			s.queueMercenaryCasts(group.bar, peer, casts)
		}
	}
	group.bar, group.peers = nil, nil
}

func (s *Session) queueMercenaryCasts(bar *Stage, peer *Session, casts []mercenaryCast) {
	peer.Lock()
	defer peer.Unlock()
	if peer.stage != bar || peer.closed.Load() {
		return
	}
	// Keep a sender's negotiation and the state updates from that same inbound
	// frame together. Build each recipient's frame before it reaches sendLoop.
	const (
		maxGroupedPayload = 4094 // Leave room for MSG_SYS_END.
		castedHeaderSize  = 10   // Opcode, character ID, cast types, data size.
	)
	for len(casts) != 0 {
		bf := byteframe.NewByteFrame()
		count := 0
		for _, cast := range casts {
			if count > 0 && len(bf.Data())+castedHeaderSize+len(cast.message.RawDataPayload) > maxGroupedPayload {
				break
			}
			bf.WriteUint16(uint16(network.MSG_SYS_CASTED_BINARY))
			_ = cast.message.Build(bf, peer.clientContext)
			count++
		}
		select {
		case peer.sendPackets <- packet{data: bf.Data(), nonBlocking: true}:
			for _, cast := range casts[:count] {
				if cast.contract {
					s.noteMercenaryReply(peer, cast.message.RawDataPayload, TimeAdjusted())
				}
			}
			peer.logMessage(uint16(network.MSG_SYS_CASTED_BINARY), bf.Data(), "Server", peer.Name)
		default:
			s.logger.Warn("Mercenary exchange queue full")
			return
		}
		casts = casts[count:]
	}
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
