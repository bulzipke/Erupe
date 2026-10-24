package channelserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
)

// Backup configuration constants.
const (
	saveBackupSlots    = 3                // number of rotating backup slots per character
	saveBackupInterval = 30 * time.Minute // minimum time between backups
)

// GetCharacterSaveData loads a character's save data from the database and
// verifies its integrity checksum when one is stored.
func GetCharacterSaveData(s *Session, charID uint32) (*CharacterSaveData, error) {
	var id uint32
	var savedata, storedHash []byte
	var isNew bool
	var name string
	var err error
	var starterEpoch *QuestLicenseStarterEpochBaseline
	if s.usesQuestLicenseStarterEpoch() {
		store, ok := s.server.charRepo.(QuestLicenseStarterEpochStore)
		if !ok {
			return nil, errors.New("character repository has no starter inventory snapshot support")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var snapshot QuestLicenseStarterEpochSnapshot
		snapshot, err = store.LoadSaveDataWithStarterEpoch(ctx, s.userID, charID)
		id, savedata, isNew, name, storedHash = snapshot.ID, snapshot.Savedata, snapshot.IsNew, snapshot.Name, snapshot.Hash
		starterEpoch = &QuestLicenseStarterEpochBaseline{UserID: s.userID, Epoch: snapshot.Epoch}
	} else {
		id, savedata, isNew, name, storedHash, err = s.server.charRepo.LoadSaveDataWithHash(charID)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.logger.Error("No savedata found", zap.Uint32("charID", charID))
			return nil, errors.New("no savedata found")
		}
		s.logger.Error("Failed to get savedata", zap.Error(err), zap.Uint32("charID", charID))
		return nil, err
	}

	saveData := &CharacterSaveData{
		CharID:         id,
		compSave:       savedata,
		IsNewCharacter: isNew,
		Name:           name,
		StoredName:     name,
		Mode:           s.server.erupeConfig.RealClientMode,
		Pointers:       getPointers(s.server.erupeConfig.RealClientMode),
		starterEpoch:   starterEpoch,
	}

	if saveData.compSave == nil {
		return saveData, nil
	}

	err = saveData.Decompress()
	if err != nil {
		s.logger.Error("Failed to decompress savedata", zap.Error(err))
		return nil, err
	}

	// Verify integrity checksum if one was stored with this save.
	// A nil hash means the character was saved before checksums were introduced,
	// so we skip verification (the next save will compute and store the hash).
	// DisableSaveIntegrityCheck bypasses this entirely for cross-server save transfers.
	if storedHash != nil && !s.server.erupeConfig.DisableSaveIntegrityCheck {
		computedHash := sha256.Sum256(saveData.decompSave)
		if !bytes.Equal(storedHash, computedHash[:]) {
			s.logger.Error("Savedata integrity check failed: hash mismatch — "+
				"if this character was imported from another server, set DisableSaveIntegrityCheck=true in config.json "+
				"or run: UPDATE characters SET savedata_hash = NULL WHERE id = <charID>",
				zap.Uint32("charID", charID),
				zap.Binary("stored_hash", storedHash),
				zap.Binary("computed_hash", computedHash[:]),
			)
			return recoverFromBackups(s, saveData, charID)
		}
	} else if storedHash != nil && s.server.erupeConfig.DisableSaveIntegrityCheck {
		s.logger.Warn("Savedata integrity check skipped (DisableSaveIntegrityCheck=true)",
			zap.Uint32("charID", charID),
		)
	}

	if err = saveData.validateLayout(); err != nil {
		s.logger.Error("Invalid savedata layout; attempting backup recovery",
			zap.Error(err), zap.Uint32("charID", charID))
		return recoverFromBackups(s, saveData, charID)
	}

	saveData.updateStructWithSaveData()

	return saveData, nil
}

// recoverFromBackups is called when the primary savedata fails its checksum or
// fixed-layout validation.
// It queries savedata_backups in recency order and returns the first slot whose
// compressed blob decompresses cleanly. It never writes to the database — the
// next successful Save() will overwrite the primary with fresh data and a new hash,
// self-healing the corruption without any extra recovery writes.
func recoverFromBackups(s *Session, base *CharacterSaveData, charID uint32) (*CharacterSaveData, error) {
	var backups []SavedataBackup
	var err error
	if s.usesQuestLicenseStarterEpoch() {
		store, ok := s.server.charRepo.(QuestLicenseStarterEpochStore)
		if !ok || base.starterEpoch == nil || base.starterEpoch.UserID != s.userID {
			return nil, errors.New("backup has no starter inventory snapshot support")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		backups, err = store.LoadBackupsWithStarterEpoch(ctx, s.userID, charID, base.starterEpoch.Epoch)
	} else {
		backups, err = s.server.charRepo.LoadBackupsByRecency(charID)
	}
	if err != nil {
		s.logger.Error("Failed to load savedata backups during recovery",
			zap.Uint32("charID", charID),
			zap.Error(err),
		)
		return nil, errors.New("savedata integrity check failed")
	}

	if len(backups) == 0 {
		s.logger.Error("Savedata corrupted and no backups available",
			zap.Uint32("charID", charID),
		)
		return nil, errors.New("savedata integrity check failed: no backups available")
	}

	for _, backup := range backups {
		candidate := &CharacterSaveData{
			CharID:         base.CharID,
			IsNewCharacter: base.IsNewCharacter,
			Name:           base.Name,
			StoredName:     base.StoredName,
			Mode:           base.Mode,
			Pointers:       base.Pointers,
			starterEpoch:   base.starterEpoch,
			compSave:       backup.Data,
		}

		if err := candidate.Decompress(); err != nil {
			s.logger.Warn("Backup slot decompression failed during recovery, trying next",
				zap.Uint32("charID", charID),
				zap.Int("slot", backup.Slot),
				zap.Time("saved_at", backup.SavedAt),
				zap.Error(err),
			)
			continue
		}

		if err := candidate.validateLayout(); err != nil {
			s.logger.Warn("Backup slot has an invalid savedata layout, skipping",
				zap.Uint32("charID", charID),
				zap.Int("slot", backup.Slot),
				zap.Error(err),
			)
			continue
		}

		s.logger.Warn("Savedata recovered from backup — primary was corrupt",
			zap.Uint32("charID", charID),
			zap.Int("slot", backup.Slot),
			zap.Time("saved_at", backup.SavedAt),
		)
		candidate.updateStructWithSaveData()
		return candidate, nil
	}

	s.logger.Error("Savedata corrupted and all backup slots failed decompression",
		zap.Uint32("charID", charID),
		zap.Int("backups_tried", len(backups)),
	)
	return nil, errors.New("savedata integrity check failed: all backup slots exhausted")
}

func (save *CharacterSaveData) Save(s *Session) error {
	return save.saveWithRewards(s, nil, nil, false)
}

// SaveWithDivaRewards is used only by the client's SAVEDATA handler. Server-side
// edits must use Save: their DB-derived blob need not contain the local rewards.
func (save *CharacterSaveData) SaveWithDivaRewards(s *Session, divaRewardIDs []uint32) error {
	return save.SaveWithClientRewards(s, divaRewardIDs, nil)
}

// SaveWithClientRewards is for a client-originated savedata snapshot only.
// DB-derived safety saves cannot consume pending client inventory receipts.
func (save *CharacterSaveData) SaveWithClientRewards(s *Session, divaRewardIDs, starterRewardIDs []uint32) error {
	return save.saveWithRewards(s, divaRewardIDs, starterRewardIDs, true)
}

func (save *CharacterSaveData) saveWithRewards(s *Session, divaRewardIDs, starterRewardIDs []uint32, clientOriginated bool) error {
	if len(divaRewardIDs) != 0 && save.CharID != s.charID {
		return errors.New("diva reward savedata character mismatch")
	}
	if len(starterRewardIDs) != 0 && (save.CharID != s.charID || s.userID == 0) {
		return errors.New("starter savedata character mismatch")
	}
	if save.decompSave == nil {
		s.logger.Warn("No decompressed save data, skipping save",
			zap.Uint32("charID", save.CharID),
		)
		return errors.New("no decompressed save data")
	}
	if err := save.validateLayout(); err != nil {
		return fmt.Errorf("invalid savedata layout: %w", err)
	}
	if save.IsNewCharacter {
		if !s.validateNameInput("character", save.Name) {
			return errors.New("invalid new character name")
		}
	} else {
		if !s.validateNameInput("character", save.StoredName) {
			return errors.New("invalid stored character name")
		}
		if save.Name != save.StoredName && !save.writeStoredName() {
			return errors.New("failed to restore stored character name")
		}
	}

	// Capture the previous compressed savedata before it's overwritten by
	// Compress(). This is what gets backed up — the last known-good state.
	prevCompSave := save.compSave

	if !s.kqfOverride {
		s.kqf = save.KQF
	} else {
		save.KQF = s.kqf
	}

	save.updateSaveDataWithStruct()

	if s.server.erupeConfig.RealClientMode >= cfg.G1 {
		err := save.Compress()
		if err != nil {
			s.logger.Error("Failed to compress savedata", zap.Error(err))
			return fmt.Errorf("compress savedata: %w", err)
		}
	} else {
		// Saves were not compressed
		save.compSave = save.decompSave
	}

	// Compute integrity hash over the decompressed save.
	hash := sha256.Sum256(save.decompSave)

	// Build the atomic save params — character data, house data, hash, and
	// optionally a backup snapshot, all in one transaction.
	params := SaveAtomicParams{
		CharID:           save.CharID,
		Name:             save.Name,
		CompSave:         save.compSave,
		Hash:             hash[:],
		HR:               save.HR,
		GR:               save.GR,
		IsFemale:         save.Gender,
		WeaponType:       save.WeaponType,
		WeaponID:         save.WeaponID,
		Playtime:         save.Playtime,
		HouseTier:        save.HouseTier,
		HouseData:        save.HouseData,
		BookshelfData:    save.BookshelfData,
		GalleryData:      save.GalleryData,
		ToreData:         save.ToreData,
		GardenData:       save.GardenData,
		DivaRewardIDs:    append([]uint32(nil), divaRewardIDs...),
		StarterRewardIDs: append([]uint32(nil), starterRewardIDs...),
	}
	if len(starterRewardIDs) != 0 {
		params.StarterUserID = s.userID
	}

	// Time-gated rotating backup: include the previous compressed savedata
	// in the transaction if enough time has elapsed since the last backup.
	if len(prevCompSave) > 0 {
		if slot, ok := shouldBackup(s, save.CharID); ok {
			params.BackupSlot = slot
			params.BackupData = prevCompSave
		}
	}

	var saveErr error
	var nextEpoch int64
	var baseline QuestLicenseStarterEpochBaseline
	if s.usesQuestLicenseStarterEpoch() {
		store, ok := s.server.charRepo.(QuestLicenseStarterEpochStore)
		if !ok || save.starterEpoch == nil || save.starterEpoch.UserID != s.userID || save.CharID != s.charID {
			return errors.New("save has no authenticated starter inventory snapshot")
		}
		baseline = *save.starterEpoch
		if clientOriginated {
			var err error
			baseline, err = s.clientStarterEpochBaseline()
			if err != nil {
				return err
			}
		}
		if params.BackupData != nil {
			epoch := save.starterEpoch.Epoch
			params.BackupStarterEpoch = &epoch
		}
		nextEpoch, saveErr = store.SaveCharacterDataAtomicWithStarterEpoch(params, baseline)
	} else {
		saveErr = s.server.charRepo.SaveCharacterDataAtomic(params)
	}
	if saveErr != nil {
		s.logger.Error("Failed to save character data atomically",
			zap.Error(saveErr), zap.Uint32("charID", save.CharID))
		return fmt.Errorf("atomic save: %w", saveErr)
	}
	if s.usesQuestLicenseStarterEpoch() {
		baseline.Epoch = nextEpoch
		save.starterEpoch = &baseline
		if clientOriginated {
			s.questLicenseClientInventory.Store(&questLicenseClientInventory{characterID: s.charID, baseline: baseline})
		}
	}
	s.completeDivaRewardClaims(7, params.DivaRewardIDs)
	s.questLicenseStarterPending.Complete(s.userID, s.charID, params.StarterRewardIDs)

	return nil
}

// shouldBackup checks whether enough time has elapsed since the last backup
// and returns the target slot if a backup should be included in the save
// transaction. Returns (slot, true) if a backup is due, (0, false) otherwise.
func shouldBackup(s *Session, charID uint32) (int, bool) {
	lastBackup, err := s.server.charRepo.GetLastBackupTime(charID)
	if err != nil {
		s.logger.Warn("Failed to query last backup time, skipping backup",
			zap.Error(err), zap.Uint32("charID", charID))
		return 0, false
	}

	if time.Since(lastBackup) < saveBackupInterval {
		return 0, false
	}

	slot := int(lastBackup.Unix()/int64(saveBackupInterval.Seconds())) % saveBackupSlots
	return slot, true
}

func handleMsgMhfSexChanger(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfSexChanger)
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}
