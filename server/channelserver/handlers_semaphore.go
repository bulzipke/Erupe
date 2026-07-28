package channelserver

import (
	"erupe-ce/common/byteframe"
	"go.uber.org/zap"
	"strconv"
	"strings"

	"erupe-ce/network/mhfpacket"
)

const (
	maxSemaphoreIDLength    = 64
	maxSemaphoresPerSession = 32
	maxSemaphoresPerChannel = 4096
)

func validSemaphoreID(id string) bool {
	return id != "" && len(id) <= maxSemaphoreIDLength
}

func removeSessionFromSemaphore(s *Session) {
	resetRavi := false
	s.server.semaphoreLock.Lock()
	for id, semaphore := range s.server.semaphore {
		semaphore.Lock()
		delete(semaphore.clients, s)
		empty := len(semaphore.clients) == 0
		semaphore.Unlock()
		if empty {
			delete(s.server.semaphore, id)
			resetRavi = resetRavi || strings.HasPrefix(id, "hs_l0")
		}
	}
	if resetRavi {
		s.server.resetRavienteLocked()
	}
	s.server.semaphoreLock.Unlock()
	s.Lock()
	s.semaphore = nil
	s.Unlock()
}

func handleMsgSysCreateSemaphore(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysCreateSemaphore)
	doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00, 0x03, 0x00, 0x0d})
}

func destructEmptySemaphores(s *Session) {
	resetRavi := false
	s.server.semaphoreLock.Lock()
	for id, sema := range s.server.semaphore {
		sema.RLock()
		empty := len(sema.clients) == 0
		sema.RUnlock()
		if empty {
			delete(s.server.semaphore, id)
			resetRavi = resetRavi || strings.HasPrefix(id, "hs_l0")
			s.logger.Debug("Destructed semaphore", zap.String("sema.name", id))
		}
	}
	if resetRavi {
		s.server.resetRavienteLocked()
	}
	s.server.semaphoreLock.Unlock()
}

func handleMsgSysDeleteSemaphore(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysDeleteSemaphore)
	destructEmptySemaphores(s)
	resetRavi := false
	s.server.semaphoreLock.Lock()
	for id, sema := range s.server.semaphore {
		if sema.id == pkt.SemaphoreID {
			sema.Lock()
			delete(sema.clients, s)
			empty := len(sema.clients) == 0
			sema.Unlock()
			if empty {
				delete(s.server.semaphore, id)
				resetRavi = resetRavi || strings.HasPrefix(id, "hs_l0")
				s.logger.Debug("Destructed semaphore", zap.String("sema.name", id))
			}
			break
		}
	}
	if resetRavi {
		s.server.resetRavienteLocked()
	}
	s.server.semaphoreLock.Unlock()
}

func handleMsgSysCreateAcquireSemaphore(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysCreateAcquireSemaphore)
	SemaphoreID := pkt.SemaphoreID
	if !validSemaphoreID(SemaphoreID) {
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	s.lifecycleMu.Lock()
	lifecycleHeld := true
	defer func() {
		if lifecycleHeld {
			s.lifecycleMu.Unlock()
		}
	}()
	if s.closed.Load() {
		s.lifecycleMu.Unlock()
		lifecycleHeld = false
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	if s.server.HasSemaphore(s) {
		s.semaphoreMode = !s.semaphoreMode
	}
	if s.semaphoreMode {
		s.semaphoreID[1]++
	} else {
		s.semaphoreID[0]++
	}

	s.server.semaphoreLock.Lock()
	memberships := s.server.semaphoreMembershipsLocked(s)
	newSemaphore, exists := s.server.semaphore[SemaphoreID]
	if !exists {
		if len(s.server.semaphore) >= maxSemaphoresPerChannel ||
			memberships >= maxSemaphoresPerSession {
			s.server.semaphoreLock.Unlock()
			s.lifecycleMu.Unlock()
			lifecycleHeld = false
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if strings.HasPrefix(SemaphoreID, "hs_l0") {
			suffix, err := strconv.Atoi(pkt.SemaphoreID[len(pkt.SemaphoreID)-1:])
			if err != nil {
				s.server.semaphoreLock.Unlock()
				s.lifecycleMu.Unlock()
				lifecycleHeld = false
				doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
				return
			}
			s.server.semaphore[SemaphoreID] = &Semaphore{
				name:       pkt.SemaphoreID,
				id:         uint32((suffix + 1) * raviSemaphoreStride),
				clients:    make(map[*Session]uint32),
				maxPlayers: raviSemaphoreMax,
			}
		} else {
			s.server.semaphore[SemaphoreID] = NewSemaphore(s, SemaphoreID, 1)
		}
		newSemaphore = s.server.semaphore[SemaphoreID]
	}

	newSemaphore.Lock()
	bf := byteframe.NewByteFrame()
	if _, exists := newSemaphore.clients[s]; exists {
		bf.WriteUint32(newSemaphore.id)
	} else if memberships < maxSemaphoresPerSession &&
		uint16(len(newSemaphore.clients)) < newSemaphore.maxPlayers {
		newSemaphore.clients[s] = s.charID
		s.Lock()
		s.semaphore = newSemaphore
		s.Unlock()
		bf.WriteUint32(newSemaphore.id)
	} else {
		bf.WriteUint32(0)
	}
	newSemaphore.Unlock()
	s.server.semaphoreLock.Unlock()
	s.lifecycleMu.Unlock()
	lifecycleHeld = false
	doAckSimpleSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgSysAcquireSemaphore(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysAcquireSemaphore)
	s.lifecycleMu.Lock()
	lifecycleHeld := true
	defer func() {
		if lifecycleHeld {
			s.lifecycleMu.Unlock()
		}
	}()
	if s.closed.Load() {
		s.lifecycleMu.Unlock()
		lifecycleHeld = false
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	s.server.semaphoreLock.RLock()
	if sema, exists := s.server.semaphore[pkt.SemaphoreID]; exists {
		sema.Lock()
		sema.host = s
		semaphoreID := sema.id
		sema.Unlock()
		s.server.semaphoreLock.RUnlock()
		s.lifecycleMu.Unlock()
		lifecycleHeld = false
		bf := byteframe.NewByteFrame()
		bf.WriteUint32(semaphoreID)
		doAckSimpleSucceed(s, pkt.AckHandle, bf.Data())
	} else {
		s.server.semaphoreLock.RUnlock()
		s.lifecycleMu.Unlock()
		lifecycleHeld = false
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
	}
}

func handleMsgSysReleaseSemaphore(s *Session, p mhfpacket.MHFPacket) {
	//pkt := p.(*mhfpacket.MsgSysReleaseSemaphore)
}

func handleMsgSysCheckSemaphore(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysCheckSemaphore)
	resp := []byte{0x00, 0x00, 0x00, 0x00}
	s.server.semaphoreLock.RLock()
	if _, exists := s.server.semaphore[pkt.SemaphoreID]; exists {
		resp = []byte{0x00, 0x00, 0x00, 0x01}
	}
	s.server.semaphoreLock.RUnlock()
	doAckSimpleSucceed(s, pkt.AckHandle, resp)
}
