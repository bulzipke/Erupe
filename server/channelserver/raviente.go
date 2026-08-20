package channelserver

import (
	"strings"
	"sync"
	"time"

	"erupe-ce/common/byteframe"
	ps "erupe-ce/common/pascalstring"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
)

// Raviente holds shared state for the Raviente siege event.
type Raviente struct {
	sync.Mutex
	id       uint16
	register []uint32
	state    []uint32
	support  []uint32
}

func (s *Server) resetRaviente() {
	s.ravienteLifecycleMu.Lock()
	s.semaphoreLock.Lock()
	generation, completed, reset := s.resetRavienteLocked()
	s.semaphoreLock.Unlock()
	if reset {
		s.recordRavienteRunTeardown(generation, completed)
	}
	s.ravienteLifecycleMu.Unlock()
}

// resetRavienteLocked resets the event before another goroutine can publish a
// replacement Raviente semaphore. The caller must hold semaphoreLock for write.
func (s *Server) resetRavienteLocked() (generation uint16, completed bool, reset bool) {
	for _, semaphore := range s.semaphore {
		if strings.HasPrefix(semaphore.name, "hs_l0") {
			return 0, false, false
		}
	}

	s.raviente.Lock()
	defer s.raviente.Unlock()
	s.logger.Debug("All Raviente Semaphores empty, resetting")
	oldGeneration := s.raviente.id
	wasCompleted := s.raviente.register[1] != 0 && s.raviente.register[2] != 0
	s.raviente.id = s.raviente.id + 1
	s.raviente.register = make([]uint32, 30)
	s.raviente.state = make([]uint32, 30)
	s.raviente.support = make([]uint32, 30)
	return oldGeneration, wasCompleted, true
}

func (s *Server) GetRaviMultiplier() float64 {
	players, active := s.raviPlayerCount()
	s.raviente.Lock()
	defer s.raviente.Unlock()
	return s.getRaviMultiplierLocked(players, active)
}

func (s *Server) getRaviMultiplierLocked(players int, active bool) float64 {
	if active {
		var minPlayers int
		if s.raviente.register[9] > 8 {
			minPlayers = 24
		} else {
			minPlayers = 4
		}
		// Guard against a division by zero in the window between the last
		// player leaving and the semaphore being torn down.
		if players <= 0 {
			return 1
		}
		if players > minPlayers {
			return 1
		}
		// Both operands must be converted before dividing: an integer division
		// here truncates the ratio (e.g. 13 of 24 players yielded 1 instead of
		// 1.85), which silently disabled scaling for most under-populated runs.
		return float64(minPlayers) / float64(players)
	}
	return 0
}

func (s *Server) UpdateRavi(semaID uint32, index uint8, value uint32, update bool) (uint32, uint32) {
	s.ravienteLifecycleMu.Lock()
	defer s.ravienteLifecycleMu.Unlock()
	players, active := s.raviPlayerCount()
	s.raviente.Lock()
	defer s.raviente.Unlock()
	return s.updateRaviLocked(semaID, index, value, update, players, active)
}

func (s *Server) updateRaviLocked(semaID uint32, index uint8, value uint32, update bool, players int, active bool) (uint32, uint32) {
	var prev uint32
	var dest *[]uint32
	switch semaID {
	case 0x40000:
		switch index {
		case 17, 28: // Ignore res and poison
			break
		default:
			value = uint32(float64(value) * s.getRaviMultiplierLocked(players, active))
		}
		dest = &s.raviente.state
	case 0x50000:
		dest = &s.raviente.support
	case 0x60000:
		dest = &s.raviente.register
	default:
		return 0, 0
	}
	if int(index) >= len(*dest) {
		return 0, 0
	}
	prev = (*dest)[index]
	if update {
		(*dest)[index] += value
	} else {
		(*dest)[index] = value
	}
	return prev, (*dest)[index]
}

func (s *Server) BroadcastRaviente(ip uint32, port uint16, stage []byte, _type uint8) {
	bf := byteframe.NewByteFrame()
	bf.SetLE()
	bf.WriteUint16(0)    // Unk
	bf.WriteUint16(0x43) // Data len
	bf.WriteUint16(3)    // Unk len
	var text string
	switch _type {
	case 2:
		text = s.i18n.raviente.berserk
	case 3:
		text = s.i18n.raviente.extreme
	case 4:
		text = s.i18n.raviente.extremeLimited
	case 5:
		text = s.i18n.raviente.berserkSmall
	default:
		s.logger.Error("Unk raviente type", zap.Uint8("_type", _type))
	}
	ps.Uint16(bf, text, true)
	bf.WriteBytes([]byte{0x5F, 0x53, 0x00})
	bf.WriteUint32(ip)   // IP address
	bf.WriteUint16(port) // Port
	bf.WriteUint16(0)    // Unk
	bf.WriteBytes(stage)
	s.WorldcastMHF(&mhfpacket.MsgSysCastedBinary{
		BroadcastType:  BroadcastTypeServer,
		MessageType:    BinaryMessageTypeChat,
		RawDataPayload: bf.Data(),
	}, nil, s)
}

func (s *Server) getRaviSemaphore() *Semaphore {
	s.semaphoreLock.RLock()
	defer s.semaphoreLock.RUnlock()
	for _, semaphore := range s.semaphore {
		if strings.HasPrefix(semaphore.name, "hs_l0") && strings.HasSuffix(semaphore.name, "3") {
			return semaphore
		}
	}
	return nil
}

func (s *Server) raviPlayerCount() (int, bool) {
	semaphore := s.getRaviSemaphore()
	if semaphore == nil {
		return 0, false
	}
	semaphore.RLock()
	players := len(semaphore.clients)
	semaphore.RUnlock()
	return players, true
}

// executeRaviResurrectionSupport performs the same state transition as
// "!ravi sendres". It returns true only when a live Raviente room had a
// pending resurrection request to consume.
func (s *Server) executeRaviResurrectionSupport() bool {
	s.ravienteLifecycleMu.Lock()
	defer s.ravienteLifecycleMu.Unlock()

	if s.getRaviSemaphore() == nil {
		return false
	}

	s.raviente.Lock()
	defer s.raviente.Unlock()
	if s.raviente.state[28] == 0 {
		return false
	}
	s.raviente.state[28] = 0
	return true
}

// executeRaviSedationSupport performs the same state transition as
// "!ravi sendsed". The support value follows the current aggregate Raviente
// HP, which is the protocol's fulfilled-sedation marker.
func (s *Server) executeRaviSedationSupport() bool {
	s.ravienteLifecycleMu.Lock()
	defer s.ravienteLifecycleMu.Unlock()

	if s.getRaviSemaphore() == nil {
		return false
	}

	s.raviente.Lock()
	defer s.raviente.Unlock()
	hp := s.raviente.state[0] + s.raviente.state[1] + s.raviente.state[2] + s.raviente.state[3] + s.raviente.state[4]
	changed := s.raviente.support[1] != hp
	s.raviente.support[1] = hp
	return changed
}

// requestRaviSedationSupport performs the same state transition as
// "!ravi reqsed".
func (s *Server) requestRaviSedationSupport() {
	s.ravienteLifecycleMu.Lock()
	defer s.ravienteLifecycleMu.Unlock()

	if s.getRaviSemaphore() == nil {
		return
	}

	s.raviente.Lock()
	defer s.raviente.Unlock()
	hp := s.raviente.state[0] + s.raviente.state[1] + s.raviente.state[2] + s.raviente.state[3] + s.raviente.state[4]
	s.raviente.support[1] = hp + 1
}

func raviSupportInterval(seconds int) time.Duration {
	const maxDurationSeconds = int64((1<<63 - 1) / int64(time.Second))
	if seconds <= 0 || int64(seconds) > maxDurationSeconds {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// raviAutoSupport periodically executes the same support operations exposed by
// the ZZ-only Raviente chat commands. Disabled intervals use nil select
// channels, so one goroutine can service either or both options without busy
// polling.
func (s *Server) raviAutoSupport() {
	if s.erupeConfig.RealClientMode != cfg.ZZ {
		return
	}

	resurrectionInterval := raviSupportInterval(s.erupeConfig.GameplayOptions.RaviAutoResurrectionSeconds)
	sedationInterval := raviSupportInterval(s.erupeConfig.GameplayOptions.RaviAutoSedationSeconds)
	if resurrectionInterval == 0 && sedationInterval == 0 {
		return
	}

	var resurrectionTicker, sedationTicker *time.Ticker
	var resurrectionTicks, sedationTicks <-chan time.Time
	if resurrectionInterval > 0 {
		resurrectionTicker = time.NewTicker(resurrectionInterval)
		resurrectionTicks = resurrectionTicker.C
		defer resurrectionTicker.Stop()
	}
	if sedationInterval > 0 {
		sedationTicker = time.NewTicker(sedationInterval)
		sedationTicks = sedationTicker.C
		defer sedationTicker.Stop()
	}

	for {
		select {
		case <-s.done:
			return
		case <-resurrectionTicks:
			if s.executeRaviResurrectionSupport() {
				s.logger.Debug("Automatically executed Raviente resurrection support")
				s.broadcastRaviUpdate()
			}
		case <-sedationTicks:
			if s.executeRaviSedationSupport() {
				s.logger.Debug("Automatically executed Raviente sedation support")
				s.broadcastRaviUpdate()
			}
		}
	}
}

// raviAutoStart optionally auto-starts a Raviente siege when its gathering room
// would otherwise fail to gather (モジ集失敗) with too few players. It performs
// the exact same flip as "!ravi start" — general register[1] = register[3] —
// on a server-side timer, then pushes the change to every gathered client.
//
// This exists because the gathering countdown is entirely client-side and the
// server has no register to watch (there is no countdown value in any register
// array), so a self-armed timer is the only way to detect "the room is about to
// give up". There is no hard client abandon deadline: on too few players the
// client keeps the room open and re-evaluates every frame, so firing a little
// late is harmless — the only requirement is to flip register[1] while players
// are still present.
//
// Disabled (no-op) unless GameplayOptions.RaviAutoStartSeconds > 0.
func (s *Server) raviAutoStart() {
	seconds := s.erupeConfig.GameplayOptions.RaviAutoStartSeconds
	if seconds <= 0 {
		return // feature disabled
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var armed bool
	var armedID uint16
	var deadline time.Time

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
		}

		// Semaphore liveness + player count. getRaviSemaphore iterates the
		// semaphore map, so guard it with the semaphore lock.
		sema := s.getRaviSemaphore()
		players := 0
		if sema != nil {
			sema.RLock()
			players = len(sema.clients)
			sema.RUnlock()
		}

		// Register snapshot under the raviente lock. Never cache the slice:
		// resetRaviente reallocates it.
		s.raviente.Lock()
		id := s.raviente.id
		started := s.raviente.register[1] != 0
		target := s.raviente.register[3]
		s.raviente.Unlock()

		// "Gathering active, not started" = a ravi room is alive, the host has
		// populated the start value (register[3] != 0, written at gather setup),
		// and the started flag (register[1]) is still 0.
		if sema == nil || target == 0 || started {
			armed = false
			continue
		}

		// (Re)arm on a fresh gathering session (new raviente.id after reset) or
		// when not yet armed.
		if !armed || armedID != id {
			armed, armedID = true, id
			deadline = time.Now().Add(time.Duration(seconds) * time.Second)
			continue
		}
		if time.Now().Before(deadline) {
			continue
		}

		// Timer elapsed. Starting an empty room is pointless; the whole point is
		// to start with FEWER players than the client's own 4/24 gate demands.
		if players < 1 {
			armed = false
			continue
		}

		// Fire: replicate the command exactly, under the lock, re-verifying the
		// preconditions so we never race a natural or manual start, and never
		// touch a session that reset() has since torn down (id changed).
		s.ravienteLifecycleMu.Lock()
		currentSemaphore := s.getRaviSemaphore()
		currentPlayers := 0
		if currentSemaphore != nil {
			currentSemaphore.RLock()
			currentPlayers = len(currentSemaphore.clients)
			currentSemaphore.RUnlock()
		}
		s.raviente.Lock()
		fired := currentSemaphore != nil && currentPlayers > 0 &&
			s.raviente.id == id && s.raviente.register[1] == 0 && s.raviente.register[3] != 0
		var startVal uint32
		if fired {
			s.raviente.register[1] = s.raviente.register[3]
			startVal = s.raviente.register[1]
		}
		s.raviente.Unlock()
		if fired {
			s.recordRavienteRunStart(id)
		}
		s.ravienteLifecycleMu.Unlock()
		if fired {
			s.logger.Info("Raviente auto-start fired",
				zap.Int("players", currentPlayers), zap.Uint32("startValue", startVal))
			s.broadcastRaviUpdate()
		}
		armed = false
	}
}

// broadcastRaviUpdate pushes an automatic register/support change to all
// gathered clients, working regardless of LowLatencyRaviente. notifyRavi is a
// *Session method whose non-low-latency path only self-notifies the calling
// session, so we snapshot the sessions and call notifyRavi per session. Even
// without this push clients eventually observe the change through their next
// LOAD_REGISTER poll.
func (s *Server) broadcastRaviUpdate() {
	sema := s.getRaviSemaphore()
	var clients []*Session
	if sema != nil {
		sema.RLock()
		clients = make([]*Session, 0, len(sema.clients))
		for session := range sema.clients {
			clients = append(clients, session)
		}
		sema.RUnlock()
	}

	lowLatency := s.erupeConfig.GameplayOptions.LowLatencyRaviente
	for _, session := range clients {
		session.notifyRavi()
		if lowLatency {
			break // one low-latency notify already reaches everyone
		}
	}
}
