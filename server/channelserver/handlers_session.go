package channelserver

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"erupe-ce/common/byteframe"
	"erupe-ce/common/mhfcourse"
	"erupe-ce/common/mhfmon"
	"erupe-ce/common/mhfquest"
	ps "erupe-ce/common/pascalstring"
	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"time"

	"go.uber.org/zap"
)

func handleMsgHead(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysExtendThreshold(s *Session, p mhfpacket.MHFPacket) {
	// No data aside from header, no resp required.
}

func handleMsgSysEnd(s *Session, p mhfpacket.MHFPacket) {
	// No data aside from header, no resp required.
}

func handleMsgSysNop(s *Session, p mhfpacket.MHFPacket) {
	// No data aside from header, no resp required.
}

func handleMsgSysAck(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

const maxTerminalLogEntriesLogged = 16

func handleMsgSysTerminalLog(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysTerminalLog)
	if s.server.erupeConfig.DebugOptions.QuestTools {
		s.logger.Debug("QuestTerminalLogDiagnostic",
			zap.Uint32("charID", uint32(s.charID)),
			zap.String("name", s.Name),
			zap.Uint32("ackHandle", pkt.AckHandle),
			zap.Uint32("logID", pkt.LogID),
			zap.Int("entryCount", len(pkt.Entries)),
			zap.Any("entries", pkt.Entries),
		)
	}
	loggedEntries := len(pkt.Entries)
	if loggedEntries > maxTerminalLogEntriesLogged {
		loggedEntries = maxTerminalLogEntriesLogged
	}
	for i := 0; i < loggedEntries; i++ {
		s.server.logger.Info("SysTerminalLog",
			zap.Uint8("Type1", pkt.Entries[i].Type1),
			zap.Uint8("Type2", pkt.Entries[i].Type2),
			zap.Int16("Unk0", pkt.Entries[i].Unk0),
			zap.Int32("Unk1", pkt.Entries[i].Unk1),
			zap.Int32("Unk2", pkt.Entries[i].Unk2),
			zap.Int32("Unk3", pkt.Entries[i].Unk3),
			zap.Int32s("Unk4", pkt.Entries[i].Unk4),
		)
	}
	if len(pkt.Entries) > loggedEntries {
		s.server.logger.Info("SysTerminalLog entries omitted",
			zap.Int("total", len(pkt.Entries)),
			zap.Int("logged", loggedEntries))
	}
	resp := byteframe.NewByteFrame()
	resp.WriteUint32(pkt.LogID + 1) // LogID to use for requests after this.
	doAckSimpleSucceed(s, pkt.AckHandle, resp.Data())
}

func handleMsgSysLogin(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysLogin)

	if !s.server.erupeConfig.DebugOptions.DisableTokenCheck {
		if err := s.server.sessionRepo.ValidateLoginToken(pkt.LoginTokenString, pkt.LoginTokenNumber, pkt.CharID0); err != nil {
			_ = s.rawConn.Close()
			s.logger.Warn("Invalid login token", zap.Uint32("charID", pkt.CharID0))
			return
		}
	}

	s.Lock()
	s.charID = pkt.CharID0
	s.token = pkt.LoginTokenString
	s.Unlock()

	userID, err := s.server.charRepo.GetUserID(s.charID)
	if err != nil {
		s.logger.Error("Failed to resolve user ID for character", zap.Error(err), zap.Uint32("charID", s.charID))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	s.userID = userID

	// Load per-user language preference. A DB error or an empty value both
	// mean "use the server default", which is what Session.Lang() returns
	// when clientLang is empty — so we don't need to fail the login here.
	if lang, langErr := s.server.userRepo.GetLanguage(userID); langErr == nil && lang != "" {
		s.SetLang(lang)
	} else if langErr != nil {
		s.logger.Warn("Failed to load user language preference", zap.Error(langErr), zap.Uint32("userID", userID))
	}

	if s.captureConn != nil {
		s.captureConn.SetSessionInfo(s.charID, s.userID)
	}

	bf := byteframe.NewByteFrame()
	bf.WriteUint32(uint32(TimeAdjusted().Unix())) // Unix timestamp

	err = s.server.sessionRepo.UpdatePlayerCount(s.server.ID, s.server.sessionCount())
	if err != nil {
		s.logger.Error("Failed to update current players", zap.Error(err))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	err = s.server.sessionRepo.BindSession(s.token, s.server.ID, s.charID)
	if err != nil {
		s.logger.Error("Failed to update sign session", zap.Error(err))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	if err = s.server.charRepo.UpdateLastLogin(s.charID, TimeAdjusted().Unix()); err != nil {
		s.logger.Error("Failed to update last login", zap.Error(err))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	err = s.server.userRepo.SetLastCharacter(s.userID, s.charID)
	if err != nil {
		s.logger.Error("Failed to update last character", zap.Error(err))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	// A random collaboration is selected only on the world's first successful
	// login and remains fixed until its final authenticated session leaves.
	s.acquireCollabEvent()

	doAckSimpleSucceed(s, pkt.AckHandle, bf.Data())

	updateRights(s)

	s.server.BroadcastMHF(&mhfpacket.MsgSysInsertUser{CharID: s.charID}, s)
}

func handleMsgSysLogout(s *Session, p mhfpacket.MHFPacket) {
	logoutPlayer(s)
}

// saveAllCharacterData saves all character data to the database with proper error handling.
// This function ensures data persistence even if the client disconnects unexpectedly.
// It handles:
// - Main savedata blob (compressed)
// - User binary data (house, gallery, etc.)
// - Plate data (transmog appearance, storage, equipment sets)
// - Playtime updates
// - RP updates
// - Name corruption prevention
func saveAllCharacterData(s *Session, rpToAdd int) error {
	unlock := s.server.charSaveLocks.Lock(s.charID)
	defer unlock()

	saveStart := time.Now()

	// Get current savedata from database
	characterSaveData, err := GetCharacterSaveData(s, s.charID)
	if err != nil {
		s.logger.Error("Failed to retrieve character save data",
			zap.Error(err),
			zap.Uint32("charID", s.charID),
			zap.String("name", s.Name),
		)
		return err
	}

	if characterSaveData == nil {
		s.logger.Warn("Character save data is nil, skipping save",
			zap.Uint32("charID", s.charID),
			zap.String("name", s.Name),
		)
		return nil
	}

	// Same guard as handleMsgMhfSavedata: if the blob loaded for this character
	// is structurally damaged, do not write it back on logout. Without this the
	// disconnect path re-persists the same garbage that the in-session save
	// already rejected.
	if !characterSaveData.IsNewCharacter && hasCorruptName(characterSaveData.Name) {
		s.logger.Error("Refusing to save corrupted savedata at logout",
			zap.String("savedata_name", characterSaveData.Name),
			zap.String("session_name", s.Name),
			zap.Uint32("charID", s.charID),
		)
		return fmt.Errorf("corrupted savedata for charID %d, save skipped", s.charID)
	}

	// Force name to match to prevent corruption detection issues
	// This handles SJIS/UTF-8 encoding differences across game versions
	if characterSaveData.Name != s.Name {
		s.logger.Debug("Correcting name mismatch before save",
			zap.String("savedata_name", characterSaveData.Name),
			zap.String("session_name", s.Name),
			zap.Uint32("charID", s.charID),
		)
		characterSaveData.Name = s.Name
		characterSaveData.updateSaveDataWithStruct()
	}

	// Update playtime from session
	if !s.playtimeTime.IsZero() {
		sessionPlaytime := uint32(time.Since(s.playtimeTime).Seconds())
		s.playtime += sessionPlaytime
		s.logger.Debug("Updated playtime",
			zap.Uint32("session_playtime_seconds", sessionPlaytime),
			zap.Uint32("total_playtime", s.playtime),
			zap.Uint32("charID", s.charID),
		)
	}
	characterSaveData.Playtime = s.playtime

	// Update RP if any gained during session
	if rpToAdd > 0 {
		characterSaveData.RP += uint16(rpToAdd)
		if characterSaveData.RP >= s.server.erupeConfig.GameplayOptions.MaximumRP {
			characterSaveData.RP = s.server.erupeConfig.GameplayOptions.MaximumRP
			s.logger.Debug("RP capped at maximum",
				zap.Uint16("max_rp", s.server.erupeConfig.GameplayOptions.MaximumRP),
				zap.Uint32("charID", s.charID),
			)
		}
		s.logger.Debug("Added RP",
			zap.Int("rp_gained", rpToAdd),
			zap.Uint16("new_rp", characterSaveData.RP),
			zap.Uint32("charID", s.charID),
		)
	}

	// Save to database (main savedata + user_binary)
	if err := characterSaveData.Save(s); err != nil {
		s.logger.Error("Failed to save character data",
			zap.Error(err),
			zap.Uint32("charID", s.charID),
			zap.String("name", s.Name),
		)
		return err
	}

	// Save auxiliary data types
	// Note: Plate data saves immediately when client sends save packets,
	// so this is primarily a safety net for monitoring and consistency
	if err := savePlateDataToDatabase(s); err != nil {
		s.logger.Error("Failed to save plate data during logout",
			zap.Error(err),
			zap.Uint32("charID", s.charID),
		)
		// Don't return error - continue with logout even if plate save fails
	}

	saveDuration := time.Since(saveStart)
	s.logger.Info("Saved character data successfully",
		zap.Uint32("charID", s.charID),
		zap.String("name", s.Name),
		zap.Duration("duration", saveDuration),
		zap.Int("rp_added", rpToAdd),
		zap.Uint32("playtime", s.playtime),
	)

	return nil
}

func logoutPlayer(s *Session) {
	s.logoutOnce.Do(func() {
		s.markClosed()
		s.closeConnection()
		defer func() {
			panicValue := recover()
			finalizeRuntimeLogout(s)
			if panicValue != nil {
				s.logger.Error("Recovered panic during player logout",
					zap.Uint32("charID", s.charID),
					zap.String("name", s.Name),
					zap.Any("panic", panicValue),
					zap.ByteString("stack", debug.Stack()))
			}
		}()
		logoutPlayerOnce(s)
	})
}

// finalizeRuntimeLogout is an idempotent safety net. It runs after both normal
// and panicking logout paths so a recovered panic cannot leave a closed session
// referenced by the live session, stage, or semaphore registries.
func finalizeRuntimeLogout(s *Session) {
	if s.server == nil {
		return
	}

	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	runLogoutCleanupPhase(s, "collaboration rotation", func() {
		s.releaseCollabEvent()
	})

	var remainingSessions []*Session
	runLogoutCleanupPhase(s, "session registry", func() {
		s.server.Lock()
		delete(s.server.sessions, s.rawConn)
		for _, session := range s.server.sessions {
			remainingSessions = append(remainingSessions, session)
		}
		s.server.Unlock()
	})
	replacementReservations := reservationStagesForCharacter(remainingSessions, s, s.charID)

	runLogoutCleanupPhase(s, "stage memberships", func() {
		s.server.stages.Range(func(id string, stage *Stage) bool {
			stage.Lock()
			delete(stage.clients, s)

			replacementInStage := false
			for session, charID := range stage.clients {
				if session != s && charID == s.charID {
					replacementInStage = true
					break
				}
			}
			if !replacementInStage {
				delete(stage.objects, s.charID)
			}
			_, replacementReserved := replacementReservations[stage]
			if !replacementInStage && !replacementReserved {
				delete(stage.reservedClientSlots, s.charID)
			}

			wasHost := stage.host == s
			if wasHost {
				stage.host = nil
				for session := range stage.clients {
					stage.host = session
					break
				}
			}
			empty := len(stage.clients) == 0 && len(stage.reservedClientSlots) == 0
			if empty && (wasHost || stageKind(id) == "Qs" || stageKind(id) == "Ms" ||
				stageKind(id) == "Gs" || stageKind(id) == "Ls") {
				s.server.stages.CompareAndDelete(id, stage)
			}
			stage.Unlock()
			return true
		})

		s.Lock()
		s.stage = nil
		s.reservationStage = nil
		s.Unlock()
	})

	runLogoutCleanupPhase(s, "semaphore memberships", func() {
		removeSessionFromSemaphore(s)
	})
}

func reservationStagesForCharacter(sessions []*Session, excluded *Session, charID uint32) map[*Stage]struct{} {
	stages := make(map[*Stage]struct{})
	for _, session := range sessions {
		if session == excluded {
			continue
		}
		session.Lock()
		sameCharacter := session.charID == charID
		reservationStage := session.reservationStage
		session.Unlock()
		if sameCharacter && reservationStage != nil {
			stages[reservationStage] = struct{}{}
		}
	}
	return stages
}

func runLogoutCleanupPhase(s *Session, phase string, cleanup func()) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("Recovered panic during logout cleanup",
				zap.String("phase", phase),
				zap.Any("panic", r),
				zap.ByteString("stack", debug.Stack()))
		}
	}()
	cleanup()
}

func logoutPlayerOnce(s *Session) {
	logoutStart := time.Now()

	// Log logout initiation with session details
	sessionDuration := time.Duration(0)
	if s.sessionStart > 0 {
		sessionDuration = time.Since(time.Unix(s.sessionStart, 0))
	}

	s.logger.Info("Player logout initiated",
		zap.Uint32("charID", s.charID),
		zap.String("name", s.Name),
		zap.Duration("session_duration", sessionDuration),
	)

	// Calculate session metrics FIRST (before cleanup)
	var timePlayed int
	var sessionTime int
	var rpGained int

	if s.charID != 0 {
		if val, err := s.server.charRepo.ReadInt(s.charID, "time_played"); err != nil {
			s.logger.Error("Failed to read time_played, RP accrual may be inaccurate", zap.Error(err))
		} else {
			timePlayed = val
		}
		sessionTime = int(TimeAdjusted().Unix()) - int(s.sessionStart)
		timePlayed += sessionTime

		if mhfcourse.CourseExists(30, s.courses) {
			rpGained = timePlayed / rpAccrualCafe
			timePlayed = timePlayed % rpAccrualCafe
			if _, err := s.server.charRepo.AdjustInt(s.charID, "cafe_time", sessionTime); err != nil {
				s.logger.Error("Failed to update cafe time", zap.Error(err))
			}
		} else {
			rpGained = timePlayed / rpAccrualNormal
			timePlayed = timePlayed % rpAccrualNormal
		}

		s.logger.Debug("Session metrics calculated",
			zap.Uint32("charID", s.charID),
			zap.Int("session_time_seconds", sessionTime),
			zap.Int("rp_gained", rpGained),
			zap.Int("time_played_remainder", timePlayed),
		)

		// Save all character data ONCE with all updates
		// This is the safety net that ensures data persistence even if client
		// didn't send save packets before disconnecting
		if err := saveAllCharacterData(s, rpGained); err != nil {
			s.logger.Error("Failed to save character data during logout",
				zap.Error(err),
				zap.Uint32("charID", s.charID),
				zap.String("name", s.Name),
			)
			// Continue with logout even if save fails
		}

		// Update time_played and guild treasure hunt
		if err := s.server.charRepo.UpdateTimePlayed(s.charID, timePlayed); err != nil {
			s.logger.Error("Failed to update time played", zap.Error(err))
		}
		if err := s.server.guildRepo.ClearTreasureHunt(s.charID); err != nil {
			s.logger.Error("Failed to clear treasure hunt", zap.Error(err))
		}
	}

	// Flush and close capture file before closing the connection.
	if s.captureCleanup != nil {
		s.captureCleanup()
	}

	// NOW do cleanup (after save is complete)
	s.server.Lock()
	delete(s.server.sessions, s.rawConn)
	s.server.Unlock()

	// Stage cleanup — snapshot sessions first under server mutex, then iterate stages
	s.server.Lock()
	sessionSnapshot := make([]*Session, 0, len(s.server.sessions))
	for _, sess := range s.server.sessions {
		sessionSnapshot = append(sessionSnapshot, sess)
	}
	s.server.Unlock()
	replacementReservations := reservationStagesForCharacter(sessionSnapshot, s, s.charID)

	s.server.stages.Range(func(_ string, stage *Stage) bool {
		stage.Lock()
		hostedBySession := stage.host == s
		reserved := make(map[uint32]struct{}, len(stage.reservedClientSlots))
		if hostedBySession {
			for charID := range stage.reservedClientSlots {
				reserved[charID] = struct{}{}
			}
		}
		delete(stage.clients, s)
		stage.Unlock()

		// Packet building and enqueueing can fail or block; never do either
		// while holding a shared stage lock.
		if hostedBySession {
			for _, sess := range sessionSnapshot {
				if _, exists := reserved[sess.charID]; exists &&
					sess.stage != nil && stageKind(sess.stage.id) != "Qs" {
					sess.QueueSendMHFNonBlocking(&mhfpacket.MsgSysStageDestruct{})
				}
			}
		}
		return true
	})

	// Update sign sessions and server player count
	if s.server.db != nil {
		if err := s.server.sessionRepo.ClearSession(s.token); err != nil {
			s.logger.Error("Failed to clear sign session", zap.Error(err))
		}

		if err := s.server.sessionRepo.UpdatePlayerCount(s.server.ID, s.server.sessionCount()); err != nil {
			s.logger.Error("Failed to update player count", zap.Error(err))
		}
	}

	s.server.stages.Range(func(_ string, stage *Stage) bool {
		stage.Lock()
		replacementInStage := false
		for session, charID := range stage.clients {
			if session != s && charID == s.charID {
				replacementInStage = true
				break
			}
		}
		_, replacementReserved := replacementReservations[stage]
		if !replacementInStage && !replacementReserved {
			delete(stage.reservedClientSlots, s.charID)
		}
		stage.Unlock()
		return true
	})
	removeSessionFromSemaphore(s)

	if s.stage == nil {
		destructEmptyHostedStages(s)
		logoutDuration := time.Since(logoutStart)
		s.logger.Info("Player logout completed",
			zap.Uint32("charID", s.charID),
			zap.String("name", s.Name),
			zap.Duration("logout_duration", logoutDuration),
		)
		return
	}

	// Broadcast user deletion and final cleanup
	s.server.BroadcastMHF(&mhfpacket.MsgSysDeleteUser{
		CharID: s.charID,
	}, s)

	removeSessionFromStage(s)
	destructEmptyHostedStages(s)

	logoutDuration := time.Since(logoutStart)
	s.logger.Info("Player logout completed",
		zap.Uint32("charID", s.charID),
		zap.String("name", s.Name),
		zap.Duration("logout_duration", logoutDuration),
		zap.Int("rp_gained", rpGained),
	)
}

func handleMsgSysSetStatus(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysPing(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysPing)
	doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x00})
}

func handleMsgSysTime(s *Session, p mhfpacket.MHFPacket) {
	resp := &mhfpacket.MsgSysTime{
		GetRemoteTime: false,
		Timestamp:     uint32(TimeAdjusted().Unix()), // JP timezone
	}
	s.QueueSendMHF(resp)
	s.notifyRavi()
}

func handleMsgSysIssueLogkey(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysIssueLogkey)

	// Make a random log key for this session.
	logKey := make([]byte, 16)
	_, err := rand.Read(logKey)
	if err != nil {
		s.logger.Error("Failed to generate log key", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	// Client log key off-by-one (RE'd from mhfo-hd.dll ZZ):
	// putIssue_logkey (0x1D) requests and stores all 16 bytes correctly.
	// putRecord_log (0x1E) and putTerminal_log (0x13) do NOT embed the log key
	// in their packets — they pass size 0 to the packet builder for the key field.
	// The original off-by-one note (Andoryuuta) may apply to pre-ZZ clients where
	// these functions did use the key. In ZZ the key is stored but never sent back,
	// so the server value is effectively unused beyond issuance.
	s.Lock()
	s.logKey = logKey
	s.Unlock()

	// Issue it.
	resp := byteframe.NewByteFrame()
	resp.WriteBytes(logKey)
	doAckBufSucceed(s, pkt.AckHandle, resp.Data())
}

const localhostAddrLE = uint32(0x0100007F) // 127.0.0.1 in little-endian

// Kill log binary layout constants
const (
	killLogHeaderSize            = 32  // bytes before monster kill count array
	killLogMonsterCount          = 176 // monster table entries
	questIDOffset                = 0   // uint16 quest ID in the ZZ record-log header
	questElapsedFramesOffset     = 12  // uint32, 30 Hz, reverse-engineered from ZZ mhfo-hd.dll
	questTimerFramesPerSecond    = 30
	maxRankedQuestDurationFrames = 4 * 60 * 60 * questTimerFramesPerSecond
)

// RP accrual rate constants (seconds per RP point)
const (
	rpAccrualNormal = 1800 // 30 min per RP without cafe
	rpAccrualCafe   = 900  // 15 min per RP with cafe course
)

func handleMsgSysRecordLog(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysRecordLog)
	if s.server.erupeConfig.DebugOptions.QuestTools {
		fields := []zap.Field{
			zap.Uint32("charID", uint32(s.charID)),
			zap.String("name", s.Name),
			zap.Uint32("ackHandle", pkt.AckHandle),
			zap.Uint32("unk0", pkt.Unk0),
			zap.Uint32("unk1", pkt.Unk1),
			zap.Int("dataBytes", len(pkt.Data)),
			zap.String("dataHex", hex.EncodeToString(pkt.Data)),
		}
		if len(pkt.Data) >= questIDOffset+2 {
			questID := binary.LittleEndian.Uint16(pkt.Data[questIDOffset : questIDOffset+2])
			fields = append(fields, zap.Uint16("questID", questID))
			if s.server.erupeConfig.RealClientMode == cfg.ZZ {
				fields = append(fields,
					zap.String("stageRunMode", s.peekQuestRunMode(questID).String()),
					zap.String("recordRunMode", decodeQuestRunModeFromRecordLog(pkt.Data).String()),
				)
			}
		}
		if len(pkt.Data) >= questElapsedFramesOffset+4 {
			fields = append(fields, zap.Uint32("elapsedFrames", binary.LittleEndian.Uint32(pkt.Data[questElapsedFramesOffset:questElapsedFramesOffset+4])))
		}
		s.logger.Debug("QuestRecordLogDiagnostic", fields...)
	}
	if s.server.erupeConfig.RealClientMode == cfg.ZZ && len(pkt.Data) >= killLogHeaderSize+killLogMonsterCount {
		bf := byteframe.NewByteFrameFromBytes(pkt.Data)
		_, _ = bf.Seek(killLogHeaderSize, 0)
		questID := binary.LittleEndian.Uint16(pkt.Data[questIDOffset : questIDOffset+2])
		elapsedFrames := binary.LittleEndian.Uint32(pkt.Data[questElapsedFramesOffset : questElapsedFramesOffset+4])
		stageRunMode := s.peekQuestRunMode(questID)
		recordRunMode := decodeQuestRunModeFromRecordLog(pkt.Data)
		recordTime := elapsedFrames > 0 && elapsedFrames <= maxRankedQuestDurationFrames && !s.questHadParty.Load()
		recordedAt := TimeAdjusted()
		questName := ""
		questMetadata := mhfquest.HuntQuestMetadata{RankKind: mhfquest.HuntRankUnknown}
		if recordTime && s.server.huntRecordRepo != nil {
			questName = questTitleForRecord(s, questID)
			questMetadata = mhfquest.ResolveHuntQuestMetadata(s.server.erupeConfig.BinPath, questID)
		}
		var val uint8
		runModeSkipLogged := false
		for i := 0; i < killLogMonsterCount; i++ {
			val = bf.ReadUint8()
			if val > 0 && mhfmon.Monsters[i].Large {
				if err := s.server.guildRepo.InsertKillLog(s.charID, i, val, recordedAt); err != nil {
					s.logger.Error("Failed to insert kill log", zap.Error(err))
				}
				if recordTime && s.server.huntRecordRepo != nil {
					baseVariant := mhfquest.MonsterHuntVariantForMetadata(questID, i, questMetadata)
					monsterVariant, resolved := resolveQuestRunVariant(baseVariant, stageRunMode, recordRunMode)
					if !resolved {
						if !runModeSkipLogged {
							fields := []zap.Field{
								zap.Uint16("questID", questID),
								zap.String("stageRunMode", stageRunMode.String()),
								zap.String("recordRunMode", recordRunMode.String()),
							}
							if stageRunMode != questRunModeUnknown && recordRunMode != questRunModeUnknown && stageRunMode != recordRunMode {
								s.logger.Warn("Skipped optional-HC hunt record because runtime mode signals disagree", fields...)
							} else if s.server.erupeConfig.DebugOptions.QuestTools {
								s.logger.Debug("Skipped optional-HC hunt record because runtime mode is unknown", fields...)
							}
							runModeSkipLogged = true
						}
						continue
					}
					record := HuntRecordUpsert{
						CharacterID:    s.charID,
						MonsterID:      i,
						QuestID:        questID,
						QuestName:      questName,
						RankKind:       string(questMetadata.RankKind),
						VariantKind:    string(monsterVariant),
						QuestVariant1:  questMetadata.Variant1,
						QuestVariant2:  questMetadata.Variant2,
						QuestVariant3:  questMetadata.Variant3,
						QuestVariant4:  questMetadata.Variant4,
						RankBand:       questMetadata.RankBand,
						StatTable1:     questMetadata.StatTable1,
						StatTable2:     questMetadata.StatTable2,
						BestTimeFrames: elapsedFrames,
						RecordedAt:     recordedAt,
					}
					if err := s.server.huntRecordRepo.UpsertPersonalBest(record); err != nil {
						s.logger.Error("Failed to update monster hunt personal best",
							zap.Error(err),
							zap.Int("monsterID", i),
							zap.String("variantKind", string(monsterVariant)),
							zap.String("rankKind", string(questMetadata.RankKind)),
							zap.Uint16("questID", questID),
							zap.Uint32("elapsedFrames", elapsedFrames),
						)
					}
				}
			}
		}
		// A result packet ends this quest run. Clear only matching setup state so
		// a delayed result from another quest cannot erase a newer selection.
		s.clearQuestRunMode(questID)
	}
	// Remove a client returning to town from reserved slots to make sure the
	// stage is hidden from the board.
	if s.stage != nil {
		s.stage.Lock()
		delete(s.stage.reservedClientSlots, s.charID)
		s.stage.Unlock()
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgSysEcho(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysLockGlobalSema(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysLockGlobalSema)
	sgid := s.server.Registry.FindChannelForStage(pkt.UserIDString)
	bf := byteframe.NewByteFrame()
	if len(sgid) > 0 && sgid != s.server.GlobalID {
		bf.WriteUint8(0)
		bf.WriteUint8(0)
		ps.Uint16(bf, sgid, false)
	} else {
		bf.WriteUint8(2)
		bf.WriteUint8(0)
		ps.Uint16(bf, pkt.ServerChannelIDString, false)
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgSysUnlockGlobalSema(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysUnlockGlobalSema)
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgSysUpdateRight(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysAuthQuery(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysAuthTerminal(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysRightsReload(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysRightsReload)
	updateRights(s)
	doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x00})
}

func handleMsgMhfTransitMessage(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfTransitMessage)

	local := strings.Split(s.rawConn.RemoteAddr().String(), ":")[0] == "127.0.0.1"

	var maxResults, port, count uint16
	var cid uint32
	var term, ip string
	bf := byteframe.NewByteFrameFromBytes(pkt.MessageData)
	switch pkt.SearchType {
	case 1:
		maxResults = 1
		cid = bf.ReadUint32()
	case 2:
		bf.ReadUint16() // term length
		maxResults = bf.ReadUint16()
		bf.ReadUint8() // Unk
		term = stringsupport.SJISToUTF8Lossy(bf.ReadNullTerminatedBytes())
	case 3:
		_ip := bf.ReadBytes(4)
		ip = fmt.Sprintf("%d.%d.%d.%d", _ip[3], _ip[2], _ip[1], _ip[0])
		port = bf.ReadUint16()
		bf.ReadUint16() // term length
		maxResults = bf.ReadUint16()
		bf.ReadUint8()
		term = string(bf.ReadNullTerminatedBytes())
	}

	resp := byteframe.NewByteFrame()
	resp.WriteUint16(0)
	switch pkt.SearchType {
	case 1, 2, 3: // usersearchidx, usersearchname, lobbysearchname
		predicate := func(snap SessionSnapshot) bool {
			switch pkt.SearchType {
			case 1:
				return snap.CharID == cid
			case 2:
				return strings.Contains(snap.Name, term)
			case 3:
				return snap.ServerIP.String() == ip && snap.ServerPort == port && snap.StageID == term
			}
			return false
		}
		snapshots := s.server.Registry.SearchSessions(predicate, int(maxResults))
		count = uint16(len(snapshots))

		for _, snap := range snapshots {
			if !local {
				resp.WriteUint32(binary.LittleEndian.Uint32(snap.ServerIP))
			} else {
				resp.WriteUint32(localhostAddrLE)
			}
			resp.WriteUint16(snap.ServerPort)
			resp.WriteUint32(snap.CharID)
			sjisStageID := stringsupport.UTF8ToSJIS(snap.StageID)
			sjisName := stringsupport.UTF8ToSJIS(snap.Name)
			resp.WriteUint8(uint8(len(sjisStageID) + 1))
			resp.WriteUint8(uint8(len(sjisName) + 1))
			resp.WriteUint16(uint16(len(snap.UserBinary3)))

			// User search response padding block (RE'd from mhfo-hd.dll ZZ):
			// ZZ per-entry parser (FUN_115868a0) reads 0x28 (40) bytes at offset +8
			// via memcpy into the result struct. G1 and earlier use 8 bytes.
			// G2 DLL analysis was inconclusive (stripped binary, no shared struct
			// sizes with ZZ) — the boundary may be <=G2 rather than <=G1.
			if s.server.erupeConfig.RealClientMode <= cfg.G1 {
				resp.WriteBytes(make([]byte, 8))
			} else {
				resp.WriteBytes(make([]byte, 40))
			}
			resp.WriteBytes(make([]byte, 8))

			resp.WriteNullTerminatedBytes(sjisStageID)
			resp.WriteNullTerminatedBytes(sjisName)
			resp.WriteBytes(snap.UserBinary3)
		}
	case 4: // lobbysearch
		type FindPartyParams struct {
			StagePrefix     string
			RankRestriction int16
			Targets         []int16
			Unk0            []int16
			Unk1            []int16
			QuestID         []int16
		}
		findPartyParams := FindPartyParams{
			StagePrefix: "sl2Ls210",
		}
		numParams := bf.ReadUint8()
		maxResults = bf.ReadUint16()
		for i := uint8(0); i < numParams; i++ {
			switch bf.ReadUint8() {
			case 0:
				values := bf.ReadUint8()
				for i := uint8(0); i < values; i++ {
					if s.server.erupeConfig.RealClientMode >= cfg.Z1 {
						findPartyParams.RankRestriction = bf.ReadInt16()
					} else {
						findPartyParams.RankRestriction = int16(bf.ReadInt8())
					}
				}
			case 1:
				values := bf.ReadUint8()
				for i := uint8(0); i < values; i++ {
					if s.server.erupeConfig.RealClientMode >= cfg.Z1 {
						findPartyParams.Targets = append(findPartyParams.Targets, bf.ReadInt16())
					} else {
						findPartyParams.Targets = append(findPartyParams.Targets, int16(bf.ReadInt8()))
					}
				}
			case 2:
				values := bf.ReadUint8()
				for i := uint8(0); i < values; i++ {
					var value int16
					if s.server.erupeConfig.RealClientMode >= cfg.Z1 {
						value = bf.ReadInt16()
					} else {
						value = int16(bf.ReadInt8())
					}
					switch value {
					case 0: // Public Bar
						findPartyParams.StagePrefix = "sl2Ls210"
					case 1: // Tokotoko Partnya
						findPartyParams.StagePrefix = "sl2Ls463"
					case 2: // Hunting Prowess Match
						findPartyParams.StagePrefix = "sl2Ls286"
					case 3: // Volpakkun Together
						findPartyParams.StagePrefix = "sl2Ls465"
					case 5: // Quick Party
						// Unk
					}
				}
			case 3: // Unknown
				values := bf.ReadUint8()
				for i := uint8(0); i < values; i++ {
					if s.server.erupeConfig.RealClientMode >= cfg.Z1 {
						findPartyParams.Unk0 = append(findPartyParams.Unk0, bf.ReadInt16())
					} else {
						findPartyParams.Unk0 = append(findPartyParams.Unk0, int16(bf.ReadInt8()))
					}
				}
			case 4: // Looking for n or already have n
				values := bf.ReadUint8()
				for i := uint8(0); i < values; i++ {
					if s.server.erupeConfig.RealClientMode >= cfg.Z1 {
						findPartyParams.Unk1 = append(findPartyParams.Unk1, bf.ReadInt16())
					} else {
						findPartyParams.Unk1 = append(findPartyParams.Unk1, int16(bf.ReadInt8()))
					}
				}
			case 5:
				values := bf.ReadUint8()
				for i := uint8(0); i < values; i++ {
					if s.server.erupeConfig.RealClientMode >= cfg.Z1 {
						findPartyParams.QuestID = append(findPartyParams.QuestID, bf.ReadInt16())
					} else {
						findPartyParams.QuestID = append(findPartyParams.QuestID, int16(bf.ReadInt8()))
					}
				}
			}
		}
		allStages := s.server.Registry.SearchStages(findPartyParams.StagePrefix, int(maxResults))

		// Post-fetch filtering on snapshots (rank restriction, targets)
		type filteredStage struct {
			StageSnapshot
			stageData []int16
		}
		var stageResults []filteredStage
		for _, snap := range allStages {
			sb3 := byteframe.NewByteFrameFromBytes(snap.RawBinData3)
			_, _ = sb3.Seek(4, 0)

			stageDataParams := 7
			if s.server.erupeConfig.RealClientMode <= cfg.G10 {
				stageDataParams = 4
			} else if s.server.erupeConfig.RealClientMode <= cfg.Z1 {
				stageDataParams = 6
			}

			var stageData []int16
			for i := 0; i < stageDataParams; i++ {
				if s.server.erupeConfig.RealClientMode >= cfg.Z1 {
					stageData = append(stageData, sb3.ReadInt16())
				} else {
					stageData = append(stageData, int16(sb3.ReadInt8()))
				}
			}

			if findPartyParams.RankRestriction >= 0 {
				if stageData[0] > findPartyParams.RankRestriction {
					continue
				}
			}

			if len(findPartyParams.Targets) > 0 {
				var hasTarget bool
				for _, target := range findPartyParams.Targets {
					if target == stageData[1] {
						hasTarget = true
						break
					}
				}
				if !hasTarget {
					continue
				}
			}

			stageResults = append(stageResults, filteredStage{
				StageSnapshot: snap,
				stageData:     stageData,
			})
		}
		count = uint16(len(stageResults))

		for _, sr := range stageResults {
			if !local {
				resp.WriteUint32(binary.LittleEndian.Uint32(sr.ServerIP))
			} else {
				resp.WriteUint32(localhostAddrLE)
			}
			resp.WriteUint16(sr.ServerPort)

			resp.WriteUint16(0) // Static?
			resp.WriteUint16(0) // Unk, [0 1 2]
			resp.WriteUint16(uint16(sr.ClientCount))
			resp.WriteUint16(sr.MaxPlayers)
			// Retail returned only clients in quest stages ("Qs" prefix),
			// not workshop/my series. RE'd from FUN_11586690 in mhfo-hd.dll ZZ:
			// field at entry offset 0x08-0x09 → struct offset 0x1C (param_1[0xe]).
			resp.WriteUint16(uint16(sr.QuestReserved))

			resp.WriteUint8(0) // Static?
			resp.WriteUint8(uint8(sr.MaxPlayers))
			resp.WriteUint8(1) // Static?
			resp.WriteUint8(uint8(len(sr.StageID) + 1))
			resp.WriteUint8(uint8(len(sr.RawBinData0)))
			resp.WriteUint8(uint8(len(sr.RawBinData1)))

			for i := range sr.stageData {
				if s.server.erupeConfig.RealClientMode >= cfg.Z1 {
					resp.WriteInt16(sr.stageData[i])
				} else {
					resp.WriteInt8(int8(sr.stageData[i]))
				}
			}
			resp.WriteUint8(0) // Unk
			resp.WriteUint8(0) // Unk

			resp.WriteNullTerminatedBytes([]byte(sr.StageID))
			resp.WriteBytes(sr.RawBinData0)
			resp.WriteBytes(sr.RawBinData1)
		}
	}
	_, _ = resp.Seek(0, io.SeekStart)
	resp.WriteUint16(count)
	doAckBufSucceed(s, pkt.AckHandle, resp.Data())
}

func handleMsgCaExchangeItem(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgCaExchangeItem)
	// TODO: full response format is not yet reverse-engineered.
	// doAckBufFail sends a well-formed buf-type ACK with error code 1.
	// The client's fail branch exits cleanly without reading response fields.
	doAckBufFail(s, pkt.AckHandle, nil)
}

func handleMsgMhfServerCommand(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgMhfAnnounce(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfAnnounce)
	s.server.BroadcastRaviente(pkt.IPAddress, pkt.Port, pkt.StageID, pkt.Data.ReadUint8())
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgMhfSetLoginwindow(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysTransBinary(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysCollectBinary(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysGetState(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysSerialize(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysEnumlobby(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysEnumuser(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgSysInfokyserver(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgMhfGetCaUniqueID(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented
