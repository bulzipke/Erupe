package channelserver

import (
	"bytes"
	"encoding/hex"
	"erupe-ce/common/bfutil"
	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"erupe-ce/network/mhfpacket"
	"erupe-ce/server/channelserver/compression/deltacomp"
	"erupe-ce/server/channelserver/compression/nullcomp"

	"go.uber.org/zap"
)

// nullcompHeader is the 16-byte magic that marks a genuinely null-compressed
// savedata payload. nullcomp.Decompress returns the input verbatim (no error)
// when this header is absent, so a payload lacking it was NOT produced by the
// normal client save path — a strong signal for hypothesis #2 (the client sent
// a non-standard/garbage blob) versus #3 (server-side framing desync).
var nullcompHeader = []byte("cmp\x2020110113\x20\x20\x20\x00")

// traceSaveBlob logs the structurally load-bearing regions of an incoming save
// so a single abandoned-quest reproduction reveals where the corruption is.
// Gated behind DebugOptions.TraceSaveCorruption so it is silent in production.
//
// Read the log for one save:
//   - had_nullcomp_header=false  -> the client sent an un-nullcomp'd payload
//     (points at hypothesis #2 / client, not a server framing desync).
//   - name_region_hex already showing garbage (e.g. f7fc597878...0b) here, at
//     the very first point the server sees the decompressed blob, means the
//     damage arrived from the wire — this handler only persists it.
//   - decoded_name control chars / U+FFFD -> corrupt blob confirmed at ingest.
func traceSaveBlob(s *Session, stage string, rawPayload []byte, blob []byte) {
	if !s.server.erupeConfig.DebugOptions.TraceSaveCorruption {
		return
	}
	region := func(b []byte, from, to int) string {
		if from < 0 || from >= len(b) {
			return "<oob>"
		}
		if to > len(b) {
			to = len(b)
		}
		return hex.EncodeToString(b[from:to])
	}
	name := ""
	if len(blob) >= saveFieldNameOffset+saveFieldNameLen {
		name = stringsupport.SJISToUTF8Lossy(
			bfutil.UpToNull(blob[saveFieldNameOffset : saveFieldNameOffset+saveFieldNameLen]))
	}
	s.logger.Info("TRACE savedata blob",
		zap.String("stage", stage),
		zap.Uint32("charID", s.charID),
		zap.String("session_name", s.Name),
		zap.Int("raw_payload_len", len(rawPayload)),
		zap.Int("decompressed_len", len(blob)),
		zap.Bool("had_nullcomp_header", bytes.HasPrefix(rawPayload, nullcompHeader)),
		zap.String("head_0x00_0x20_hex", region(blob, 0, 32)),
		zap.String("name_0x50_0x68_hex", region(blob, 0x50, 0x68)), // offset 80..104, name is at 88
		zap.String("decoded_name", name),
	)
}

// Save data size limits.
// The largest known decompressed savedata is ZZ at ~147KB. We use generous
// ceilings to accommodate unknown versions while still catching runaway data.
const (
	saveDataMaxCompressedPayload   = 524288  // 512KB max compressed payload from client
	saveDataMaxDecompressedPayload = 1048576 // 1MB max decompressed savedata
)

func handleMsgMhfSavedata(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfSavedata)

	// Serialize saves for the same character to prevent concurrent operations
	// from racing and defeating corruption detection.
	unlock := s.server.charSaveLocks.Lock(s.charID)
	defer unlock()

	if len(pkt.RawDataPayload) > saveDataMaxCompressedPayload {
		s.logger.Warn("Savedata payload exceeds size limit",
			zap.Int("len", len(pkt.RawDataPayload)),
			zap.Int("max", saveDataMaxCompressedPayload),
			zap.Uint32("charID", s.charID),
		)
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	characterSaveData, err := GetCharacterSaveData(s, s.charID)
	if err != nil {
		s.logger.Error("failed to retrieve character save data from db", zap.Error(err), zap.Uint32("charID", s.charID))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	// Snapshot current house tier before applying the update so we can
	// restore it if the incoming data is corrupted (issue #92).
	prevHouseTier := make([]byte, len(characterSaveData.HouseTier))
	copy(prevHouseTier, characterSaveData.HouseTier)

	// Var to hold the decompressed savedata for updating the launcher response fields.
	if pkt.SaveType == 1 {
		// Diff-based update.
		// diffs themselves are also potentially compressed
		diff, err := nullcomp.DecompressWithLimit(pkt.RawDataPayload, saveDataMaxDecompressedPayload)
		if err != nil {
			s.logger.Error("Failed to decompress diff", zap.Error(err))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		// Perform diff with bounds checking.
		s.logger.Info("Diffing...")
		patched, err := deltacomp.ApplyDataDiffWithLimit(diff, characterSaveData.decompSave, saveDataMaxDecompressedPayload)
		if err != nil {
			s.logger.Error("Failed to apply save diff", zap.Error(err), zap.Uint32("charID", s.charID))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		characterSaveData.decompSave = patched
		traceSaveBlob(s, "diff-patched", pkt.RawDataPayload, characterSaveData.decompSave)
	} else {
		dumpSaveData(s, pkt.RawDataPayload, "savedata")
		// Regular blob update.
		saveData, err := nullcomp.DecompressWithLimit(pkt.RawDataPayload, saveDataMaxDecompressedPayload)
		if err != nil {
			s.logger.Error("Failed to decompress savedata from packet", zap.Error(err))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		if s.server.erupeConfig.SaveDumps.RawEnabled {
			dumpSaveData(s, saveData, "raw-savedata")
		}
		s.logger.Info("Updating save with blob")
		characterSaveData.decompSave = saveData
		traceSaveBlob(s, "full-blob", pkt.RawDataPayload, characterSaveData.decompSave)
	}
	if err := characterSaveData.validateLayout(); err != nil {
		s.logger.Warn("Rejected savedata with invalid layout",
			zap.Error(err), zap.Uint32("charID", s.charID))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	characterSaveData.updateStructWithSaveData()

	// A name containing control characters or U+FFFD is not an encoding
	// difference — the blob itself is damaged (observed after abandoning an
	// event quest: the name field held f7 fc 59 78 0b -> "販Yx\v"). Check the
	// raw incoming name before the authoritative-name repair below can mask it.
	if !characterSaveData.IsNewCharacter && hasCorruptName(characterSaveData.Name) {
		s.logger.Error("Refusing to save corrupted savedata",
			zap.String("savedata_name", characterSaveData.Name),
			zap.String("session_name", s.Name),
			zap.Uint32("charID", s.charID),
			zap.Int("decompressed_len", len(characterSaveData.decompSave)),
		)
		dumpSaveData(s, characterSaveData.decompSave, "corrupt-savedata")
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	if characterSaveData.IsNewCharacter && !s.validateNameInput("character", characterSaveData.Name) {
		// The client ignores a failed SAVEDATA acknowledgement during initial
		// character creation and continues into the game with its local copy.
		// The database write is already prevented above; terminate this
		// provisional session as well so the rejected character cannot appear to
		// have been created or issue follow-up packets in the same connection.
		s.logger.Warn("Disconnecting session after rejected character name",
			zap.Uint32("charID", s.charID))
		s.markClosed()
		s.closeConnection()
		return
	}
	if !characterSaveData.IsNewCharacter {
		if !s.validateNameInput("character", characterSaveData.StoredName) {
			s.logger.Warn("Disconnecting session with invalid stored character name",
				zap.Uint32("charID", s.charID))
			s.markClosed()
			s.closeConnection()
			return
		}
		if characterSaveData.Name != characterSaveData.StoredName {
			s.logger.Warn("Restoring client-modified name in savedata",
				zap.String("savedata_name", characterSaveData.Name),
				zap.String("stored_name", characterSaveData.StoredName),
				zap.Uint32("charID", s.charID))
			if !characterSaveData.writeStoredName() {
				doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
				return
			}
		}
		s.Name = characterSaveData.StoredName
	}

	// Mitigate house theme corruption (issue #92): the game client
	// sometimes sends house_tier as -1 (all 0xFF bytes), which causes
	// the house theme to vanish on next login. If the new value looks
	// corrupted, restore the previous value in both the struct and the
	// decompressed blob so Save() persists consistent data.
	if len(prevHouseTier) > 0 && characterSaveData.isHouseTierCorrupted() {
		s.logger.Warn("Detected corrupted house_tier in save data, restoring previous value",
			zap.Binary("corrupted", characterSaveData.HouseTier),
			zap.Binary("restored", prevHouseTier),
			zap.Uint32("charID", s.charID),
		)
		characterSaveData.restoreHouseTier(prevHouseTier)
	}

	s.playtime = characterSaveData.Playtime
	s.playtimeTime = time.Now()

	// The structural checks above and the client's configurable NG-word table
	// have both passed, so the first save may establish the session name.
	if characterSaveData.IsNewCharacter {
		s.Name = characterSaveData.Name
	}

	if characterSaveData.Name == s.Name || s.server.erupeConfig.RealClientMode <= cfg.S10 {
		if err := characterSaveData.Save(s); err != nil {
			s.logger.Error("Failed to save character data", zap.Error(err))
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		s.logger.Info("Wrote recompressed savedata back to DB.")
	} else {
		_ = s.rawConn.Close()
		s.logger.Warn("Save cancelled due to corruption.")
		if s.server.erupeConfig.DeleteOnSaveCorruption {
			if err := s.server.charRepo.SetDeleted(s.charID); err != nil {
				s.logger.Error("Failed to mark character as deleted", zap.Error(err))
			}
		}
		return
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func grpToGR(n int) uint16 {
	var gr int
	a := []int{208750, 593400, 993400, 1400900, 2315900, 3340900, 4505900, 5850900, 7415900, 9230900, 11345900, 100000000}
	b := []int{7850, 8000, 8150, 9150, 10250, 11650, 13450, 15650, 18150, 21150, 23950}
	c := []int{51, 100, 150, 200, 300, 400, 500, 600, 700, 800, 900}

	for i := 0; i < len(a); i++ {
		if n < a[i] {
			if i == 0 {
				for {
					n -= 500
					if n <= 500 {
						if n < 0 {
							i--
						}
						break
					} else {
						i++
						for j := 0; j < i; j++ {
							n -= 150
						}
					}
				}
				gr = i + 2
			} else {
				n -= a[i-1]
				gr = c[i-1]
				gr += n / b[i-1]
			}
			break
		}
	}
	return uint16(gr)
}

func dumpSaveData(s *Session, data []byte, suffix string) {
	if !s.server.erupeConfig.SaveDumps.Enabled {
		return
	} else {
		dir := filepath.Join(s.server.erupeConfig.SaveDumps.OutputDir, fmt.Sprintf("%d", s.charID))
		path := filepath.Join(s.server.erupeConfig.SaveDumps.OutputDir, fmt.Sprintf("%d", s.charID), fmt.Sprintf("%d_%s.bin", s.charID, suffix))
		_, err := os.Stat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				err = os.MkdirAll(dir, os.ModePerm)
				if err != nil {
					s.logger.Error("Error dumping savedata, could not create folder")
					return
				}
			} else {
				s.logger.Error("Error dumping savedata")
				return
			}
		}
		err = os.WriteFile(path, data, 0644)
		if err != nil {
			s.logger.Error("Error dumping savedata, could not write file", zap.Error(err))
		}
	}
}

func handleMsgMhfLoaddata(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfLoaddata)
	unlock := s.server.charSaveLocks.Lock(s.charID)
	defer unlock()

	usingOverride := false
	var overrideData []byte
	overridePath := filepath.Join(s.server.erupeConfig.BinPath, "save_override.bin")
	if _, statErr := os.Stat(overridePath); statErr == nil {
		var readErr error
		overrideData, readErr = os.ReadFile(overridePath)
		if readErr != nil {
			s.logger.Error("Failed to read save_override.bin", zap.Error(readErr))
		} else {
			usingOverride = true
		}
	}

	var characterSaveData *CharacterSaveData
	var err error
	if usingOverride {
		isNewCharacter, stateErr := s.server.charRepo.ReadBool(s.charID, "is_new_character")
		storedName, nameErr := s.server.charRepo.GetName(s.charID)
		if stateErr != nil || nameErr != nil {
			s.logger.Warn("Failed to load character identity for save override",
				zap.Uint32("charID", s.charID),
				zap.NamedError("state_error", stateErr),
				zap.NamedError("name_error", nameErr))
			s.markClosed()
			s.closeConnection()
			return
		}
		characterSaveData = &CharacterSaveData{
			CharID:         s.charID,
			Name:           storedName,
			StoredName:     storedName,
			IsNewCharacter: isNewCharacter,
			Mode:           s.server.erupeConfig.RealClientMode,
			Pointers:       getPointers(s.server.erupeConfig.RealClientMode),
			compSave:       overrideData,
		}
		if err = characterSaveData.Decompress(); err == nil {
			err = characterSaveData.validateLayout()
		}
		if err == nil {
			characterSaveData.updateStructWithSaveData()
		}
	} else {
		characterSaveData, err = GetCharacterSaveData(s, s.charID)
	}
	if err != nil || characterSaveData == nil || len(characterSaveData.compSave) == 0 {
		s.logger.Warn("Failed to load valid savedata", zap.Error(err), zap.Uint32("charID", s.charID))
		s.markClosed()
		s.closeConnection()
		return
	}

	if characterSaveData.IsNewCharacter {
		if !s.validateNameInput("character", characterSaveData.Name) {
			s.logger.Warn("Disconnecting load for invalid new character name",
				zap.Uint32("charID", s.charID))
			s.markClosed()
			s.closeConnection()
			return
		}
	} else {
		if !s.validateNameInput("character", characterSaveData.StoredName) {
			s.logger.Warn("Disconnecting load for invalid stored character name",
				zap.Uint32("charID", s.charID))
			s.markClosed()
			s.closeConnection()
			return
		}
		if characterSaveData.Name != characterSaveData.StoredName {
			s.logger.Warn("Restoring stored character name before load",
				zap.String("savedata_name", characterSaveData.Name),
				zap.String("stored_name", characterSaveData.StoredName),
				zap.Uint32("charID", s.charID))
			if !characterSaveData.writeStoredName() {
				s.markClosed()
				s.closeConnection()
				return
			}
			if usingOverride {
				// save_override.bin remains a non-persistent debug override, but its
				// client-visible identity must still be the database identity.
				if characterSaveData.Mode >= cfg.G1 {
					if err := characterSaveData.Compress(); err != nil {
						s.logger.Error("Failed to recompress repaired save override", zap.Error(err))
						s.markClosed()
						s.closeConnection()
						return
					}
				} else {
					characterSaveData.compSave = characterSaveData.decompSave
				}
			} else {
				// Persist the repaired blob before sending it. Diff saves sent later
				// are based on this response, so the database and client must share
				// the exact same base bytes.
				if err := characterSaveData.Save(s); err != nil {
					s.logger.Error("Failed to persist repaired savedata", zap.Error(err))
					s.markClosed()
					s.closeConnection()
					return
				}
			}
		}
	}

	doAckBufSucceed(s, pkt.AckHandle, characterSaveData.compSave)
	s.playtime = characterSaveData.Playtime
	s.playtimeTime = time.Now()
	nameBytes := append([]byte(nil), bfutil.UpToNull(
		characterSaveData.decompSave[saveFieldNameOffset:saveFieldNameOffset+saveFieldNameLen])...)
	s.server.userBinary.Set(s.charID, 1, append(nameBytes, 0x00))
	s.Name = characterSaveData.Name
}

func handleMsgMhfSaveScenarioData(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfSaveScenarioData)
	saveCharacterData(s, pkt.AckHandle, "scenariodata", pkt.RawDataPayload, 65536)
}

func handleMsgMhfLoadScenarioData(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfLoadScenarioData)
	loadCharacterData(s, pkt.AckHandle, "scenariodata", make([]byte, 10))
}

func handleMsgSysAuthData(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented
