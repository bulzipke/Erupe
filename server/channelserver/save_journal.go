package channelserver

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"erupe-ce/common/savejournal"
	cfg "erupe-ce/config"
	"erupe-ce/network"
	"erupe-ce/server/channelserver/compression/nullcomp"

	"go.uber.org/zap"
)

// ZZ savedata spans read from the client code rather than from Erupe's
// pointer table. The client copies exactly 7000 eight-byte stacks from the
// item box and walks 30 from the carried items.
const (
	saveJournalItemBoxOffsetZZ   = 0x11A14
	saveJournalItemBoxStacksZZ   = 7000
	saveJournalItemPouchOffsetZZ = 0x23E28
	saveJournalItemPouchStacksZZ = 30
	// Four u16 the client rewrites by itself: one of them on every save, all
	// four on a quest return. Their meaning is unknown; the name only keeps
	// them from reading as an unexplained change.
	saveJournalEverySaveOffsetZZ = 0x1F722
	saveJournalEverySaveLenZZ    = 8
	saveJournalStackSize         = 8
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
	if save.Mode == cfg.ZZ {
		regions = append(regions,
			savejournal.Region{Name: "item_box", Off: saveJournalItemBoxOffsetZZ,
				Len: saveJournalItemBoxStacksZZ * saveJournalStackSize, Stacks: true},
			savejournal.Region{Name: "item_pouch", Off: saveJournalItemPouchOffsetZZ,
				Len: saveJournalItemPouchStacksZZ * saveJournalStackSize, Stacks: true},
			savejournal.Region{Name: "every_save", Off: saveJournalEverySaveOffsetZZ, Len: saveJournalEverySaveLenZZ})
	}
	return regions
}

// journalClientSave records what an accepted client save changed. prev is the
// decompressed savedata the server held before this upload; save holds what
// was just persisted. It runs after the save succeeded and never fails it.
func (s *Session) journalClientSave(saveType uint8, prev []byte, wasNew bool, save *CharacterSaveData) {
	if !s.server.saveJournal.Enabled() {
		return
	}
	now := time.Now()
	window := s.saveActivity.Take(now)
	kind := "blob"
	if saveType == 1 {
		kind = "diff"
	}
	rec := s.saveJournalRecord(kind, now, prev, wasNew, save)
	rec.Activity = &savejournal.ActivityRecord{Seconds: window.Seconds, Events: window.Events, Dropped: window.Dropped}
	if len(window.Ops) > 0 {
		rec.Activity.Ops = make(map[string]uint32, len(window.Ops))
		for opcode, count := range window.Ops {
			rec.Activity.Ops[network.PacketID(opcode).String()] = count
		}
	}
	s.writeSaveJournal(rec)
}

// journalServerSave records a save the server wrote on its own: the logout
// save, a reward added to a stored save, a repaired blob. prevComp is the
// stored savedata the write replaced. Without these lines a character's hash
// chain would break at every login, and a save edited outside the server
// could not be told apart from an ordinary logout.
//
// The session's activity window is left alone: it belongs to the next client
// save.
func (s *Session) journalServerSave(prevComp []byte, wasNew bool, save *CharacterSaveData) {
	if s.server == nil || !s.server.saveJournal.Enabled() {
		return
	}
	prev := prevComp
	if len(prevComp) > 0 && save.Mode >= cfg.G1 {
		var err error
		if prev, err = nullcomp.DecompressWithLimit(prevComp, saveDataMaxDecompressedPayload); err != nil {
			// The new save is still worth a line; it just has no predecessor.
			s.logger.Warn("Save journal could not read the save a server write replaced",
				zap.Error(err), zap.Uint32("charID", save.CharID))
			prev = nil
		}
	}
	s.writeSaveJournal(s.saveJournalRecord("server", time.Now(), prev, wasNew, save))
}

// saveJournalRecord describes the change from prev to the savedata now held
// in save.
func (s *Session) saveJournalRecord(kind string, now time.Time, prev []byte, wasNew bool, save *CharacterSaveData) *savejournal.Record {
	current := save.decompSave
	newHash := sha256.Sum256(current)
	rec := &savejournal.Record{
		V:         savejournal.RecordVersion,
		Time:      now.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		CharID:    save.CharID,
		Name:      save.Name,
		Kind:      kind,
		OldLen:    len(prev),
		NewLen:    len(current),
		NewSHA256: hex.EncodeToString(newHash[:]),
	}
	// A server write can be for a character other than the session's own;
	// the session's account is then not that character's.
	if save.CharID == s.charID {
		rec.UserID = s.userID
	}
	if len(prev) > 0 {
		oldHash := sha256.Sum256(prev)
		rec.OldSHA256 = hex.EncodeToString(oldHash[:])
	}
	// A new character's first save replaces a template (or nothing), so a
	// byte diff would be the whole blob and say nothing about the player.
	if wasNew || len(prev) == 0 {
		rec.Baseline = true
		return rec
	}
	regions := saveJournalRegions(save)
	diff := savejournal.Diff(prev, current, regions, savejournal.DefaultByteBudget)
	rec.Fields = savejournal.Fields(prev, current, regions)
	rec.Items = savejournal.StackTotals(prev, current, regions)
	rec.Changed, rec.Ranges, rec.Truncated = diff.Changed, diff.Ranges, diff.Truncated
	return rec
}

func (s *Session) writeSaveJournal(rec *savejournal.Record) {
	if err := s.server.saveJournal.Write(rec); err != nil {
		s.logger.Warn("Failed to write save journal record", zap.Error(err), zap.Uint32("charID", rec.CharID))
	}
}

// saveBundleOpcodes arrive with every client save, so counting them says
// nothing about what the player did.
var saveBundleOpcodes = map[network.PacketID]struct{}{
	network.MSG_MHF_SAVEDATA:            {}, // closes the window, not part of it
	network.MSG_MHF_SAVE_FAVORITE_QUEST: {},
	network.MSG_MHF_SAVE_PARTNER:        {},
	network.MSG_MHF_SAVE_SCENARIO_DATA:  {},
}

// countSaveActivity notes a received packet for the next journal record.
// Keep-alive and movement packets carry no information about what the player
// did, and neither do the packets that make up the save itself.
func (s *Session) countSaveActivity(opcode network.PacketID) {
	if !s.server.saveJournal.Enabled() || ignored(opcode) {
		return
	}
	if _, bundled := saveBundleOpcodes[opcode]; bundled {
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
