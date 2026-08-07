package channelserver

import (
	"encoding/hex"
	"strings"
	"time"

	"erupe-ce/common/byteframe"
	ps "erupe-ce/common/pascalstring"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
)

const (
	maxStageIDLength          = 255
	maxHostedStagesPerSession = 32
)

func validStageID(id string) bool {
	return len(id) >= 5 && len(id) <= maxStageIDLength
}

func stageKind(id string) string {
	if len(id) < 5 {
		return ""
	}
	return id[3:5]
}

func hostedStageCount(s *Session) int {
	count := 0
	s.server.stages.Range(func(_ string, stage *Stage) bool {
		stage.RLock()
		hosted := stage.host == s
		stage.RUnlock()
		if hosted {
			count++
		}
		return count < maxHostedStagesPerSession
	})
	return count
}

func handleMsgSysCreateStage(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysCreateStage)
	if !validStageID(pkt.StageID) {
		s.logger.Warn("Rejected invalid stage ID", zap.Int("length", len(pkt.StageID)))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	if hostedStageCount(s) >= maxHostedStagesPerSession {
		s.logger.Warn("Rejected stage creation limit",
			zap.Int("limit", maxHostedStagesPerSession))
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
	stage := NewStage(pkt.StageID)
	stage.host = s
	stage.maxPlayers = uint16(pkt.PlayerCount)
	created := s.server.stages.StoreIfAbsent(pkt.StageID, stage)
	s.lifecycleMu.Unlock()
	lifecycleHeld = false
	if created {
		doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x00})
	} else {
		doAckSimpleFail(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x00})
	}
}

func handleMsgSysStageDestruct(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func doStageTransfer(s *Session, ackHandle uint32, stageID string) bool {
	if !validStageID(stageID) {
		s.logger.Warn("Rejected invalid stage transfer ID", zap.Int("length", len(stageID)))
		doAckSimpleFail(s, ackHandle, make([]byte, 4))
		return false
	}
	if _, exists := s.server.stages.Get(stageID); !exists &&
		hostedStageCount(s) >= maxHostedStagesPerSession {
		s.logger.Warn("Rejected stage transfer creation limit",
			zap.Int("limit", maxHostedStagesPerSession))
		doAckSimpleFail(s, ackHandle, make([]byte, 4))
		return false
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
		doAckSimpleFail(s, ackHandle, make([]byte, 4))
		return false
	}
	var stage *Stage
	var created bool
	for {
		stage, created = s.server.stages.GetOrCreate(stageID)
		stage.Lock()
		// Empty-stage cleanup removes the map entry while holding this same
		// stage lock. If it won the race after our lookup, retry against the
		// current map entry instead of joining an orphaned stage.
		if s.server.stages.IsCurrent(stageID, stage) {
			break
		}
		stage.Unlock()
	}
	if created {
		stage.host = s
	}
	_, alreadyClient := stage.clients[s]
	_, hasReservation := stage.reservedClientSlots[s.charID]
	if !alreadyClient && !hasReservation &&
		len(stage.clients)+len(stage.reservedClientSlots) >= int(stage.maxPlayers) {
		stage.Unlock()
		if created {
			s.server.stages.CompareAndDelete(stageID, stage)
		}
		s.lifecycleMu.Unlock()
		lifecycleHeld = false
		doAckSimpleFail(s, ackHandle, []byte{0x00, 0x00, 0x00, 0x01})
		return false
	}
	stage.clients[s] = s.charID
	delete(stage.reservedClientSlots, s.charID)
	updateQuestPartyTrackingLocked(stage, s, alreadyClient)
	stage.Unlock()

	// Ensure this session no longer belongs to reservations.
	s.Lock()
	oldStage := s.stage
	s.Unlock()
	if oldStage != nil && oldStage != stage {
		removeSessionFromStage(s)
	}

	// Save our new stage pointer.
	s.Lock()
	s.stage = stage
	s.Unlock()
	if stageKind(stageID) == "Qs" {
		s.beginQuestRun()
	} else {
		// Failed or abandoned quests may not send a record log. Returning to any
		// non-quest stage is therefore the reliable boundary for stale run state.
		s.clearQuestRunMode(0)
		s.endQuestRun()
	}
	s.lifecycleMu.Unlock()
	lifecycleHeld = false

	// Tell the client to cleanup its current stage objects.
	// Use blocking send to ensure this critical cleanup packet is not dropped.
	s.QueueSendMHF(&mhfpacket.MsgSysCleanupObject{})

	// Confirm the stage entry.
	doAckSimpleSucceed(s, ackHandle, []byte{0x00, 0x00, 0x00, 0x00})

	newNotif := byteframe.NewByteFrame()

	// Cast existing user data to new user
	if !s.loaded {
		s.loaded = true

		// Lock server to safely iterate over sessions map
		// We need to copy the session list first to avoid holding the lock during packet building
		s.server.Lock()
		var sessionList []*Session
		for _, session := range s.server.sessions {
			if s == session || !session.loaded {
				continue
			}
			sessionList = append(sessionList, session)
		}
		s.server.Unlock()

		// Build packets for each session without holding the lock
		var temp mhfpacket.MHFPacket
		for _, session := range sessionList {
			temp = &mhfpacket.MsgSysInsertUser{CharID: session.charID}
			newNotif.WriteUint16(uint16(temp.Opcode()))
			_ = temp.Build(newNotif, s.clientContext)
			for i := 0; i < 3; i++ {
				temp = &mhfpacket.MsgSysNotifyUserBinary{
					CharID:     session.charID,
					BinaryType: uint8(i + 1),
				}
				newNotif.WriteUint16(uint16(temp.Opcode()))
				_ = temp.Build(newNotif, s.clientContext)
			}
		}
	}

	if s.stage != nil { // avoids lock up when using bed for dream quests
		// Notify the client to duplicate the existing objects.
		s.logger.Info("Sending existing stage objects", zap.String("session", s.Name))

		// Lock stage to safely iterate over objects map
		// We need to copy the objects list first to avoid holding the lock during packet building
		s.stage.RLock()
		var objectList []*Object
		for _, obj := range s.stage.objects {
			if obj.ownerCharID == s.charID {
				continue
			}
			objectList = append(objectList, obj)
		}
		s.stage.RUnlock()

		// Build packets for each object without holding the lock
		var temp mhfpacket.MHFPacket
		for _, obj := range objectList {
			temp = &mhfpacket.MsgSysDuplicateObject{
				ObjID:       obj.id,
				X:           obj.x,
				Y:           obj.y,
				Z:           obj.z,
				Unk0:        0,
				OwnerCharID: obj.ownerCharID,
			}
			newNotif.WriteUint16(uint16(temp.Opcode()))
			_ = temp.Build(newNotif, s.clientContext)
		}
	}

	// FIX: Always send stage transfer packet, even if empty.
	// The client expects this packet to complete the zone change, regardless of content.
	// Previously, if newNotif was empty (no users, no objects), no packet was sent,
	// causing the client to timeout after 60 seconds.
	s.QueueSend(newNotif.Data())
	return true
}

// updateQuestPartyTrackingLocked marks every participant once a quest stage
// has held more than one client. The flag remains set even if a party member
// leaves early, so the result cannot later be mistaken for a solo hunt.
// stage must be write-locked by the caller.
func updateQuestPartyTrackingLocked(stage *Stage, joining *Session, alreadyClient bool) {
	if stageKind(stage.id) != "Qs" {
		return
	}
	if !alreadyClient {
		joining.questHadParty.Store(false)
	}
	if len(stage.clients) <= 1 {
		return
	}
	for session := range stage.clients {
		session.questHadParty.Store(true)
	}
}

func destructEmptyStages(s *Session) {
	s.server.stages.Range(func(id string, stage *Stage) bool {
		kind := stageKind(id)
		stage.Lock()
		transient := kind == "Qs" || kind == "Ms" || kind == "Gs" || kind == "Ls"
		isEmpty := len(stage.reservedClientSlots) == 0 && len(stage.clients) == 0
		deleted := isEmpty && transient && s.server.stages.CompareAndDelete(id, stage)
		stage.Unlock()

		if deleted {
			s.logger.Debug("Destructed stage", zap.String("stage.id", id))
		}
		return true
	})
}

func destructEmptyHostedStages(s *Session) {
	s.server.stages.Range(func(id string, stage *Stage) bool {
		stage.Lock()
		emptyHostedStage := stage.host == s &&
			len(stage.reservedClientSlots) == 0 &&
			len(stage.clients) == 0
		deleted := emptyHostedStage && s.server.stages.CompareAndDelete(id, stage)
		stage.Unlock()
		if deleted {
			s.logger.Debug("Destructed hosted stage", zap.String("stage.id", id))
		}
		return true
	})
}

func removeSessionFromStage(s *Session) {
	s.Lock()
	stage := s.stage
	s.Unlock()
	if stage == nil {
		return
	}

	// Acquire stage lock to protect concurrent access to clients and objects maps
	// This prevents race conditions when multiple goroutines access these maps
	stage.Lock()

	// Remove client from old stage.
	delete(stage.clients, s)

	// Delete old stage objects owned by the client.
	// We must copy the objects to delete to avoid modifying the map while iterating
	var objectsToDelete []*Object
	replacementInStage := false
	for session, charID := range stage.clients {
		if session != s && charID == s.charID {
			replacementInStage = true
			break
		}
	}
	if !replacementInStage {
		for _, object := range stage.objects {
			if object.ownerCharID == s.charID {
				objectsToDelete = append(objectsToDelete, object)
			}
		}
	}

	// Delete from map while still holding lock
	for _, object := range objectsToDelete {
		delete(stage.objects, object.ownerCharID)
	}

	// CRITICAL FIX: Unlock BEFORE broadcasting to avoid deadlock
	// BroadcastMHF also tries to lock the stage, so we must release our lock first
	stage.Unlock()

	// Now broadcast the deletions (without holding the lock)
	for _, object := range objectsToDelete {
		stage.BroadcastMHF(&mhfpacket.MsgSysDeleteObject{ObjID: object.id}, s)
	}

	destructEmptyStages(s)
	destructEmptyHostedStages(s)
	destructEmptySemaphores(s)
}

func isStageFull(s *Session, StageID string) bool {
	stage, exists := s.server.stages.Get(StageID)

	if exists {
		// Lock stage to safely check client counts
		// Read the values we need while holding RLock, then release immediately
		// to avoid deadlock with other functions that might hold server lock
		stage.RLock()
		reserved := len(stage.reservedClientSlots)
		clients := len(stage.clients)
		_, hasReservation := stage.reservedClientSlots[s.charID]
		maxPlayers := stage.maxPlayers
		stage.RUnlock()

		if hasReservation {
			return false
		}
		return reserved+clients >= int(maxPlayers)
	}
	return false
}

func handleMsgSysEnterStage(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysEnterStage)

	if isStageFull(s, pkt.StageID) {
		doAckSimpleFail(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x01})
		return
	}

	s.Lock()
	oldStage := s.stage
	s.Unlock()

	// Reserve the old stage before transferring so transient stages cannot be
	// destroyed between leaving and a later back-stage request.
	if oldStage != nil {
		oldStage.Lock()
		oldStage.reservedClientSlots[s.charID] = false
		oldStage.Unlock()
		s.stageMoveStack.Push(oldStage.id)
	}

	if !doStageTransfer(s, pkt.AckHandle, pkt.StageID) {
		if oldStage != nil {
			oldStage.Lock()
			delete(oldStage.reservedClientSlots, s.charID)
			oldStage.Unlock()
			_, _ = s.stageMoveStack.Pop()
		}
		return
	}

	s.Lock()
	if s.reservationStage != nil {
		s.reservationStage = nil
	}
	s.Unlock()
}

func handleMsgSysBackStage(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysBackStage)

	// Transfer back to the saved stage ID before the previous move or enter.
	backStage, err := s.stageMoveStack.Pop()
	if backStage == "" || err != nil {
		backStage = "sl1Ns200p0a0u0"
	}

	if isStageFull(s, backStage) {
		s.stageMoveStack.Push(backStage)
		doAckSimpleFail(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x01})
		return
	}

	s.Lock()
	oldStage := s.stage
	s.Unlock()
	if !doStageTransfer(s, pkt.AckHandle, backStage) {
		s.stageMoveStack.Push(backStage)
		return
	}

	if oldStage != nil {
		oldStage.Lock()
		delete(oldStage.reservedClientSlots, s.charID)
		oldStage.Unlock()
		destructEmptyStages(s)
	}
}

func handleMsgSysMoveStage(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysMoveStage)

	if isStageFull(s, pkt.StageID) {
		doAckSimpleFail(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x01})
		return
	}

	doStageTransfer(s, pkt.AckHandle, pkt.StageID)
}

func handleMsgSysLeaveStage(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysLockStage(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysLockStage)
	stage, exists := s.server.stages.Get(pkt.StageID)
	if exists {
		stage.Lock()
		stage.locked = true
		stage.Unlock()
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgSysUnlockStage(s *Session, p mhfpacket.MHFPacket) {
	s.Lock()
	reservationStage := s.reservationStage
	s.Unlock()
	if reservationStage != nil {
		reservationStage.Lock()
		charIDs := make([]uint32, 0, len(reservationStage.reservedClientSlots))
		for charID := range reservationStage.reservedClientSlots {
			charIDs = append(charIDs, charID)
			delete(reservationStage.reservedClientSlots, charID)
		}
		if len(reservationStage.clients) == 0 {
			s.server.stages.CompareAndDelete(reservationStage.id, reservationStage)
		}
		reservationStage.Unlock()

		s.Lock()
		if s.reservationStage == reservationStage {
			s.reservationStage = nil
		}
		s.Unlock()

		for _, charID := range charIDs {
			session := s.server.FindSessionByCharID(charID)
			if session != nil {
				session.Lock()
				if session.reservationStage == reservationStage {
					session.reservationStage = nil
				}
				session.Unlock()
				session.QueueSendMHFNonBlocking(&mhfpacket.MsgSysStageDestruct{})
			}
		}
	}

	destructEmptyStages(s)
}

func handleMsgSysReserveStage(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysReserveStage)
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
	stage, exists := s.server.stages.Get(pkt.StageID)
	if !exists {
		s.lifecycleMu.Unlock()
		lifecycleHeld = false
		s.logger.Error("Failed to get stage", zap.String("StageID", pkt.StageID))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	s.Lock()
	stagePass := s.stagePass
	s.Unlock()

	success := false
	newReservation := false
	stage.Lock()
	// Empty-stage cleanup can remove the map entry after Get but before this
	// lock is acquired. Never attach a reservation to that orphaned stage.
	if !s.server.stages.IsCurrent(pkt.StageID, stage) {
		stage.Unlock()
		s.lifecycleMu.Unlock()
		lifecycleHeld = false
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	if _, alreadyReserved := stage.reservedClientSlots[s.charID]; alreadyReserved {
		switch pkt.Ready {
		case 1: // 0x01
			stage.reservedClientSlots[s.charID] = false
		case 17: // 0x11
			stage.reservedClientSlots[s.charID] = true
		}
		success = true
	} else if uint16(len(stage.reservedClientSlots)) < stage.maxPlayers &&
		!stage.locked &&
		(len(stage.password) == 0 || stage.password == stagePass) {
		stage.reservedClientSlots[s.charID] = false
		success = true
		newReservation = true
	}
	stage.Unlock()

	if newReservation {
		// Save the reservation stage in the session for later use in
		// MsgSysUnreserveStage.
		s.Lock()
		s.reservationStage = stage
		s.Unlock()
	}
	s.lifecycleMu.Unlock()
	lifecycleHeld = false
	if success {
		doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
	} else {
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
	}
}

func handleMsgSysUnreserveStage(s *Session, p mhfpacket.MHFPacket) {
	s.Lock()
	stage := s.reservationStage
	s.reservationStage = nil
	s.Unlock()
	if stage != nil {
		stage.Lock()
		delete(stage.reservedClientSlots, s.charID)
		stage.Unlock()
	}
}

func handleMsgSysSetStagePass(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysSetStagePass)
	s.Lock()
	stage := s.reservationStage
	s.Unlock()
	if stage != nil {
		stage.Lock()
		// Will only exist if host.
		if _, exists := stage.reservedClientSlots[s.charID]; exists {
			stage.password = pkt.Password
		}
		stage.Unlock()
	} else {
		// Store for use on next ReserveStage.
		s.Lock()
		s.stagePass = pkt.Password
		s.Unlock()
	}
}

func handleMsgSysSetStageBinary(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysSetStageBinary)
	questID, runMode, runModeDecoded := decodeQuestRunModeFromStageBinary(
		pkt.StageID,
		pkt.BinaryType0,
		pkt.BinaryType1,
		pkt.RawDataPayload,
	)
	if s.server.erupeConfig.RealClientMode != cfg.ZZ {
		runModeDecoded = false
	}
	if s.server.erupeConfig.DebugOptions.QuestTools {
		fields := []zap.Field{
			zap.Uint32("charID", uint32(s.charID)),
			zap.String("name", s.Name),
			zap.String("stageID", pkt.StageID),
			zap.Uint8("binaryType0", pkt.BinaryType0),
			zap.Uint8("binaryType1", pkt.BinaryType1),
			zap.Int("payloadBytes", len(pkt.RawDataPayload)),
			zap.String("payloadHex", hex.EncodeToString(pkt.RawDataPayload)),
		}
		if runModeDecoded {
			fields = append(fields,
				zap.Uint16("questID", questID),
				zap.String("runMode", runMode.String()),
			)
		}
		s.logger.Debug("QuestStageBinaryDiagnostic", fields...)
	}
	stage, exists := s.server.stages.Get(pkt.StageID)
	if exists {
		if runModeDecoded {
			s.storeQuestRunMode(questID, runMode)
		}
		const maxStageBinaryEntries = 256
		key := stageBinaryKey{pkt.BinaryType0, pkt.BinaryType1}
		stage.Lock()
		_, replacing := stage.rawBinaryData[key]
		stored := replacing || len(stage.rawBinaryData) < maxStageBinaryEntries
		if stored {
			stage.rawBinaryData[key] = append([]byte(nil), pkt.RawDataPayload...)
		}
		stage.Unlock()
		if !stored {
			s.logger.Warn("Stage binary entry limit reached", zap.String("StageID", pkt.StageID))
		}
	} else {
		s.logger.Warn("Failed to get stage", zap.String("StageID", pkt.StageID))
	}
}

func handleMsgSysGetStageBinary(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysGetStageBinary)
	stage, exists := s.server.stages.Get(pkt.StageID)
	if exists {
		stage.RLock()
		binaryData, gotBinary := stage.rawBinaryData[stageBinaryKey{pkt.BinaryType0, pkt.BinaryType1}]
		binaryData = append([]byte(nil), binaryData...)
		stage.RUnlock()
		if gotBinary {
			doAckBufSucceed(s, pkt.AckHandle, binaryData)
		} else if pkt.BinaryType1 == 4 {
			// Server-generated binary used for guild room checks and lobby state.
			// Earlier clients (G1) crash on a completely empty response when parsing
			// this during lobby initialization, so return a minimal valid structure
			// with a zero entry count.
			doAckBufSucceed(s, pkt.AckHandle, make([]byte, 4))
		} else {
			s.logger.Warn("Failed to get stage binary", zap.Uint8("BinaryType0", pkt.BinaryType0), zap.Uint8("pkt.BinaryType1", pkt.BinaryType1))
			doAckBufSucceed(s, pkt.AckHandle, make([]byte, 4))
		}
	} else {
		s.logger.Warn("Failed to get stage", zap.String("StageID", pkt.StageID))
		doAckBufSucceed(s, pkt.AckHandle, make([]byte, 4))
	}
	s.logger.Debug("MsgSysGetStageBinary Done!")
}

func handleMsgSysWaitStageBinary(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysWaitStageBinary)
	stage, exists := s.server.stages.Get(pkt.StageID)
	if exists {
		if pkt.BinaryType0 == 1 && pkt.BinaryType1 == 12 {
			// This might contain the hunter count, or max player count?
			doAckBufSucceed(s, pkt.AckHandle, []byte{0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
			return
		}
		for i := 0; i < 10; i++ {
			s.logger.Debug("MsgSysWaitStageBinary before lock and get stage")
			stage.Lock()
			stageBinary, gotBinary := stage.rawBinaryData[stageBinaryKey{pkt.BinaryType0, pkt.BinaryType1}]
			stage.Unlock()
			s.logger.Debug("MsgSysWaitStageBinary after lock and get stage")
			if gotBinary {
				doAckBufSucceed(s, pkt.AckHandle, stageBinary)
				return
			} else {
				s.logger.Debug("Waiting stage binary", zap.Uint8("BinaryType0", pkt.BinaryType0), zap.Uint8("pkt.BinaryType1", pkt.BinaryType1))
				time.Sleep(1 * time.Second)
				continue
			}
		}
		s.logger.Warn("MsgSysWaitStageBinary stage binary timeout")
		doAckBufSucceed(s, pkt.AckHandle, []byte{})
	} else {
		s.logger.Warn("Failed to get stage", zap.String("StageID", pkt.StageID))
		doAckBufSucceed(s, pkt.AckHandle, make([]byte, 4))
	}
	s.logger.Debug("MsgSysWaitStageBinary Done!")
}

func handleMsgSysEnumerateStage(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysEnumerateStage)

	// Build the response
	bf := byteframe.NewByteFrame()
	var joinable uint16
	bf.WriteUint16(0)
	s.server.stages.Range(func(sid string, stage *Stage) bool {
		stage.RLock()

		if len(stage.reservedClientSlots) == 0 && len(stage.clients) == 0 {
			stage.RUnlock()
			return true
		}
		if !strings.Contains(stage.id, pkt.StagePrefix) {
			stage.RUnlock()
			return true
		}
		joinable++

		bf.WriteUint16(uint16(len(stage.reservedClientSlots)))
		bf.WriteUint16(uint16(len(stage.clients)))
		if strings.HasPrefix(stage.id, "sl2Ls") {
			bf.WriteUint16(uint16(len(stage.clients) + len(stage.reservedClientSlots)))
		} else {
			bf.WriteUint16(uint16(len(stage.clients)))
		}
		bf.WriteUint16(stage.maxPlayers)
		var flags uint8
		if stage.locked {
			flags |= 1
		}
		if len(stage.password) > 0 {
			flags |= 2
		}
		bf.WriteUint8(flags)
		ps.Uint8(bf, sid, false)
		stage.RUnlock()
		return true
	})
	_, _ = bf.Seek(0, 0)
	bf.WriteUint16(joinable)

	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}
