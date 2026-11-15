package channelserver

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"erupe-ce/common/savejournal"
	"erupe-ce/network"

	"go.uber.org/zap"
)

// saveJournalRegions lists the savedata spans this client version is known to
// use, so a journal range can say what it touched. Unmapped versions simply
// yield fewer names; the byte ranges are recorded either way.
func saveJournalRegions(save *CharacterSaveData) []savejournal.Region {
	regions := []savejournal.Region{{Name: "name", Off: saveFieldNameOffset, Len: saveFieldNameLen}}
	add := func(name string, pointer SavePointer, size int, numeric bool) {
		// A missing pointer reads as offset 0, which is the blob header and
		// never one of these fields.
		if off, ok := save.Pointers[pointer]; ok && off > 0 && size > 0 {
			regions = append(regions, savejournal.Region{Name: name, Off: off, Len: size, Numeric: numeric})
		}
	}
	add("gender", pGender, 1, true)
	add("zenny", pZenny, saveFieldZenny, true)
	add("gzenny", pGZenny, saveFieldGZenny, true)
	add("cp", pCP, saveFieldCP, true)
	add("playtime", pPlaytime, saveFieldPlaytime, true)
	add("weapon_id", pWeaponID, saveFieldWeaponID, true)
	add("weapon_type", pWeaponType, 1, true)
	add("hr", pHR, saveFieldHR, true)
	add("grp", pGRP, saveFieldGRP, true)
	add("rp", pRP, saveFieldRP, true)
	add("house_tier", pHouseTier, saveFieldHouseTier, false)
	add("tore", pToreData, saveFieldTore, false)
	add("house_data", pHouseData, saveFieldHouseData, false)
	add("bookshelf", pBookshelfData, save.Pointers[lBookshelfData], false)
	add("gallery", pGalleryData, saveFieldGallery, false)
	add("garden", pGardenData, saveFieldGarden, false)
	add("kqf", pKQF, saveFieldKQF, false)
	return regions
}

// journalClientSave records what an accepted client save changed. prev is the
// decompressed savedata the server held before this upload; save holds what
// was just persisted. It runs after the save succeeded and never fails it.
func (s *Session) journalClientSave(saveType uint8, prev []byte, wasNew bool, save *CharacterSaveData) {
	journal := s.server.saveJournal
	if !journal.Enabled() {
		return
	}
	now := time.Now()
	window := s.saveActivity.Take(now)
	current := save.decompSave

	kind := "blob"
	if saveType == 1 {
		kind = "diff"
	}
	newHash := sha256.Sum256(current)
	rec := &savejournal.Record{
		V:         savejournal.RecordVersion,
		Time:      now.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		CharID:    save.CharID,
		UserID:    s.userID,
		Name:      save.Name,
		Kind:      kind,
		OldLen:    len(prev),
		NewLen:    len(current),
		NewSHA256: hex.EncodeToString(newHash[:]),
	}
	if len(prev) > 0 {
		oldHash := sha256.Sum256(prev)
		rec.OldSHA256 = hex.EncodeToString(oldHash[:])
	}
	// A new character's first save replaces a template (or nothing), so a
	// byte diff would be the whole blob and say nothing about the player.
	if wasNew || len(prev) == 0 {
		rec.Baseline = true
	} else {
		regions := saveJournalRegions(save)
		diff := savejournal.Diff(prev, current, regions, savejournal.DefaultByteBudget)
		rec.Fields = savejournal.Fields(prev, current, regions)
		rec.Changed, rec.Ranges, rec.Truncated = diff.Changed, diff.Ranges, diff.Truncated
	}
	rec.Activity = &savejournal.ActivityRecord{Seconds: window.Seconds, Events: window.Events, Dropped: window.Dropped}
	if len(window.Ops) > 0 {
		rec.Activity.Ops = make(map[string]uint32, len(window.Ops))
		for opcode, count := range window.Ops {
			rec.Activity.Ops[network.PacketID(opcode).String()] = count
		}
	}
	if err := journal.Write(rec); err != nil {
		s.logger.Warn("Failed to write save journal record", zap.Error(err), zap.Uint32("charID", save.CharID))
	}
}

// countSaveActivity notes a received packet for the next journal record.
// Keep-alive and movement packets carry no information about what the player
// did, and the save packet itself is what closes the window.
func (s *Session) countSaveActivity(opcode network.PacketID) {
	if !s.server.saveJournal.Enabled() || ignored(opcode) || opcode == network.MSG_MHF_SAVEDATA {
		return
	}
	s.saveActivity.Count(uint16(opcode), time.Now())
}

// noteSaveActivity adds a notable event to the next journal record.
func (s *Session) noteSaveActivity(e savejournal.Event) {
	if s.server == nil || !s.server.saveJournal.Enabled() {
		return
	}
	s.saveActivity.Add(e, time.Now())
}
