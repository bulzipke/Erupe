package channelserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"testing"

	"erupe-ce/common/savejournal"
	cfg "erupe-ce/config"
	"erupe-ce/network"
	"erupe-ce/network/mhfpacket"
	"erupe-ce/server/channelserver/compression/nullcomp"
)

type journalBuffer struct{ bytes.Buffer }

func (*journalBuffer) Close() error { return nil }

func (b *journalBuffer) records(t *testing.T) []savejournal.Record {
	t.Helper()
	var out []savejournal.Record
	for _, line := range bytes.Split(bytes.TrimSpace(b.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec savejournal.Record
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("journal line is not JSON: %v\n%s", err, line)
		}
		out = append(out, rec)
	}
	return out
}

// journalTestSession prepares an existing ZZ character whose stored savedata
// holds the given zenny, with the journal captured in memory.
func journalTestSession(t *testing.T, zenny uint32) (*Session, *mockCharacterRepo, *journalBuffer, []byte) {
	t.Helper()
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	out := &journalBuffer{}
	server.saveJournal = savejournal.NewWriter(out)

	stored := characterSaveBlob(t, "Hunter")
	binary.LittleEndian.PutUint32(stored[0xB0:], zenny)
	repo := newMockCharacterRepo()
	repo.loadSaveDataID = 42
	repo.loadSaveDataName = "Hunter"
	repo.strings["name"] = "Hunter"
	repo.loadSaveDataData, _ = nullcomp.Compress(stored)
	server.charRepo = repo

	session := createMockSession(42, server)
	session.Name = "Hunter"
	session.userID = 7
	session.done = make(chan struct{})
	return session, repo, out, stored
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestClientSaveWritesJournalRecord(t *testing.T) {
	session, repo, out, stored := journalTestSession(t, 1000)

	session.countSaveActivity(network.MSG_MHF_ACQUIRE_EXCHANGE_SHOP)
	session.countSaveActivity(network.MSG_MHF_ACQUIRE_EXCHANGE_SHOP)
	session.countSaveActivity(network.MSG_SYS_PING)     // keep-alive, not activity
	session.countSaveActivity(network.MSG_MHF_SAVEDATA) // closes the window, not part of it
	// These three accompany every client save.
	session.countSaveActivity(network.MSG_MHF_SAVE_PARTNER)
	session.countSaveActivity(network.MSG_MHF_SAVE_SCENARIO_DATA)
	session.countSaveActivity(network.MSG_MHF_SAVE_FAVORITE_QUEST)
	cleared := true
	session.noteSaveActivity(savejournal.Event{Kind: "quest_enter", Quest: 23527, Name: "시험 퀘스트"})
	session.noteSaveActivity(savejournal.Event{Kind: "quest_result", Quest: 23527, Frames: 5400, Cleared: &cleared})

	incoming := append([]byte(nil), stored...)
	binary.LittleEndian.PutUint32(incoming[0xB0:], 5000)
	incoming[0x4000] = 0xAB // a change the server has no name for
	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{AckHandle: 1, RawDataPayload: incoming})

	if len(repo.saveAtomicParams) != 1 {
		t.Fatalf("atomic saves = %d, want 1", len(repo.saveAtomicParams))
	}
	persisted, err := nullcomp.Decompress(repo.saveAtomicParams[0].CompSave)
	if err != nil {
		t.Fatalf("decompress persisted save: %v", err)
	}
	records := out.records(t)
	if len(records) != 1 {
		t.Fatalf("journal records = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.V != savejournal.RecordVersion || rec.CharID != 42 || rec.UserID != 7 || rec.Name != "Hunter" || rec.Kind != "blob" || rec.Baseline {
		t.Fatalf("record header = %+v", rec)
	}
	if rec.OldSHA256 != sha256Hex(stored) || rec.NewSHA256 != sha256Hex(persisted) {
		t.Fatal("record hashes do not match the stored and persisted savedata")
	}
	if got := rec.Fields["zenny"]; got != [2]uint64{1000, 5000} {
		t.Fatalf("zenny field = %v, want [1000 5000]", got)
	}
	var sawZenny, sawUnnamed bool
	for _, r := range rec.Ranges {
		switch r.Off {
		case 0xB0:
			sawZenny = r.At == "zenny" && r.Old == "e803" && r.New == "8813"
		case 0x4000:
			sawUnnamed = r.At == "" && r.Old == "00" && r.New == "ab"
		}
	}
	if !sawZenny || !sawUnnamed {
		t.Fatalf("ranges = %+v", rec.Ranges)
	}
	if rec.Activity == nil || len(rec.Activity.Ops) != 1 || rec.Activity.Ops["MSG_MHF_ACQUIRE_EXCHANGE_SHOP"] != 2 {
		t.Fatalf("activity ops = %+v", rec.Activity)
	}
	events := rec.Activity.Events
	if len(events) != 2 || events[0].Kind != "quest_enter" || events[0].Name != "시험 퀘스트" ||
		events[1].Kind != "quest_result" || events[1].Frames != 5400 || events[1].Cleared == nil || !*events[1].Cleared {
		t.Fatalf("activity events = %+v", events)
	}

	// The next save starts a new window and chains from the previous hash.
	repo.loadSaveDataData = repo.saveAtomicParams[0].CompSave
	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{AckHandle: 2, RawDataPayload: persisted})
	records = out.records(t)
	if len(records) != 2 {
		t.Fatalf("journal records = %d, want 2", len(records))
	}
	second := records[1]
	if second.OldSHA256 != rec.NewSHA256 || second.Changed != 0 || len(second.Ranges) != 0 || len(second.Fields) != 0 {
		t.Fatalf("unchanged save = %+v", second)
	}
	if len(second.Activity.Ops) != 0 || len(second.Activity.Events) != 0 {
		t.Fatalf("activity leaked into the next window: %+v", second.Activity)
	}
}

func putSaveStack(blob []byte, table, slot int, item, quantity uint16) {
	off := table + slot*saveJournalStackSize
	binary.LittleEndian.PutUint16(blob[off:], item)
	binary.LittleEndian.PutUint16(blob[off+2:], quantity)
}

func TestClientSaveJournalReportsItemTotals(t *testing.T) {
	session, repo, out, stored := journalTestSession(t, 9203512)
	last := saveJournalItemBoxStacksZZ - 1
	putSaveStack(stored, saveJournalItemBoxOffsetZZ, 3, 7976, 20)
	putSaveStack(stored, saveJournalItemBoxOffsetZZ, 10, 181, 999)
	putSaveStack(stored, saveJournalItemBoxOffsetZZ, last, 8070, 5)
	putSaveStack(stored, saveJournalItemPouchOffsetZZ, saveJournalItemPouchStacksZZ-1, 8070, 1)
	repo.loadSaveDataData, _ = nullcomp.Compress(stored)

	// Sell the 7976 stack, move the 181 stack into its slot, carry one 8070.
	incoming := append([]byte(nil), stored...)
	putSaveStack(incoming, saveJournalItemBoxOffsetZZ, 3, 181, 999)
	putSaveStack(incoming, saveJournalItemBoxOffsetZZ, 10, 0, 0)
	putSaveStack(incoming, saveJournalItemBoxOffsetZZ, last, 8070, 4)
	putSaveStack(incoming, saveJournalItemPouchOffsetZZ, saveJournalItemPouchStacksZZ-1, 8070, 2)
	binary.LittleEndian.PutUint32(incoming[0xB0:], 9999999)
	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{AckHandle: 1, RawDataPayload: incoming})

	records := out.records(t)
	if len(records) != 1 {
		t.Fatalf("journal records = %d, want 1", len(records))
	}
	rec := records[0]
	box, pouch := rec.Items["item_box"], rec.Items["item_pouch"]
	if len(rec.Items) != 2 || len(box) != 2 || len(pouch) != 1 ||
		box["7976"] != [2]uint32{20, 0} || box["8070"] != [2]uint32{5, 4} || pouch["8070"] != [2]uint32{1, 2} {
		t.Fatalf("items = %+v", rec.Items)
	}
	if got := rec.Fields["zenny"]; got != [2]uint64{9203512, 9999999} {
		t.Fatalf("zenny field = %v", got)
	}
	var boxRanges, pouchRanges int
	for _, r := range rec.Ranges {
		switch r.At {
		case "item_box":
			boxRanges++
		case "item_pouch":
			pouchRanges++
		}
	}
	if boxRanges != 3 || pouchRanges != 1 {
		t.Fatalf("ranges = %+v", rec.Ranges)
	}
}

func TestServerSaveIsJournaledAndKeepsTheHashChain(t *testing.T) {
	session, repo, out, stored := journalTestSession(t, 1000)
	session.countSaveActivity(network.MSG_MHF_PLAY_NORMAL_GACHA)

	// What the logout path does: load the stored save, change a value the
	// server owns, write it back.
	save, err := GetCharacterSaveData(session, 42)
	if err != nil {
		t.Fatalf("load save: %v", err)
	}
	save.Playtime += 90
	if err := save.Save(session); err != nil {
		t.Fatalf("server save: %v", err)
	}
	persisted, err := nullcomp.Decompress(repo.saveAtomicParams[0].CompSave)
	if err != nil {
		t.Fatalf("decompress persisted save: %v", err)
	}

	records := out.records(t)
	if len(records) != 1 {
		t.Fatalf("journal records = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.Kind != "server" || rec.Activity != nil || rec.Baseline || rec.CharID != 42 || rec.UserID != 7 {
		t.Fatalf("server record = %+v", rec)
	}
	if rec.OldSHA256 != sha256Hex(stored) || rec.NewSHA256 != sha256Hex(persisted) {
		t.Fatal("server record hashes do not match the stored and persisted savedata")
	}
	if got := rec.Fields["playtime"]; got != [2]uint64{0, 90} {
		t.Fatalf("playtime field = %v, want [0 90]", got)
	}

	// The next client save chains from the server record and still carries
	// the activity counted before it.
	repo.loadSaveDataData = repo.saveAtomicParams[0].CompSave
	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{AckHandle: 1, RawDataPayload: persisted})
	records = out.records(t)
	if len(records) != 2 {
		t.Fatalf("journal records = %d, want 2", len(records))
	}
	next := records[1]
	if next.Kind != "blob" || next.OldSHA256 != rec.NewSHA256 || next.Activity == nil ||
		next.Activity.Ops["MSG_MHF_PLAY_NORMAL_GACHA"] != 1 {
		t.Fatalf("client record after a server save = %+v", next)
	}
}

func TestServerSaveForAnotherCharacterOmitsTheSessionAccount(t *testing.T) {
	session, _, out, _ := journalTestSession(t, 1000)
	session.charID = 99 // the stored save belongs to character 42

	save, err := GetCharacterSaveData(session, 42)
	if err != nil {
		t.Fatalf("load save: %v", err)
	}
	save.Playtime++
	if err := save.Save(session); err != nil {
		t.Fatalf("server save: %v", err)
	}
	records := out.records(t)
	if len(records) != 1 || records[0].Kind != "server" || records[0].CharID != 42 || records[0].UserID != 0 {
		t.Fatalf("records = %+v", records)
	}
}

func TestNewCharacterSaveIsJournaledAsBaseline(t *testing.T) {
	session, repo, out, _ := journalTestSession(t, 0)
	repo.loadSaveDataNew = true
	repo.loadSaveDataData = nil
	session.Name = ""

	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{AckHandle: 1, RawDataPayload: characterSaveBlob(t, "Hunter")})

	records := out.records(t)
	if len(records) != 1 {
		t.Fatalf("journal records = %d, want 1", len(records))
	}
	rec := records[0]
	if !rec.Baseline || len(rec.Ranges) != 0 || rec.OldSHA256 != "" || rec.NewSHA256 == "" || rec.NewLen != 150000 {
		t.Fatalf("baseline record = %+v", rec)
	}
}

func TestRejectedSaveIsNotJournaledAndKeepsActivity(t *testing.T) {
	session, _, out, stored := journalTestSession(t, 1000)
	session.countSaveActivity(network.MSG_MHF_PLAY_NORMAL_GACHA)

	corrupt := append([]byte(nil), stored...)
	corrupt[saveFieldNameOffset] = 0x0b
	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{AckHandle: 1, RawDataPayload: corrupt})
	if records := out.records(t); len(records) != 0 {
		t.Fatalf("rejected save was journaled: %+v", records)
	}

	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{AckHandle: 2, RawDataPayload: stored})
	records := out.records(t)
	if len(records) != 1 || records[0].Activity.Ops["MSG_MHF_PLAY_NORMAL_GACHA"] != 1 {
		t.Fatalf("activity before the rejected save was lost: %+v", records)
	}
}

func TestJournalRecordNamesClientDiffSaves(t *testing.T) {
	session, _, out, stored := journalTestSession(t, 1000)
	save := &CharacterSaveData{CharID: 42, Name: "Hunter", Mode: cfg.ZZ, Pointers: getPointers(cfg.ZZ),
		decompSave: append([]byte(nil), stored...)}
	session.journalClientSave(1, stored, false, save)
	records := out.records(t)
	if len(records) != 1 || records[0].Kind != "diff" {
		t.Fatalf("records = %+v", records)
	}
}

func TestDisabledSaveJournalCollectsNothing(t *testing.T) {
	session, repo, _, stored := journalTestSession(t, 1000)
	session.server.saveJournal = nil

	session.countSaveActivity(network.MSG_MHF_PLAY_NORMAL_GACHA)
	session.noteSaveActivity(savejournal.Event{Kind: "quest_enter", Quest: 1})
	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{AckHandle: 1, RawDataPayload: stored})

	if len(repo.saveAtomicParams) != 1 {
		t.Fatalf("atomic saves = %d, want 1", len(repo.saveAtomicParams))
	}
	if w := session.saveActivity.Take(session.playtimeTime); len(w.Ops) != 0 || len(w.Events) != 0 {
		t.Fatalf("disabled journal still collected activity: %+v", w)
	}
}

func TestSaveJournalRegionsFollowClientVersion(t *testing.T) {
	names := func(mode cfg.Mode) map[string]savejournal.Region {
		out := make(map[string]savejournal.Region)
		for _, r := range saveJournalRegions(&CharacterSaveData{Mode: mode, Pointers: getPointers(mode)}) {
			out[r.Name] = r
		}
		return out
	}
	zz := names(cfg.ZZ)
	if r := zz["zenny"]; r.Off != 0xB0 || r.Len != 4 || !r.Numeric {
		t.Fatalf("ZZ zenny region = %+v", r)
	}
	if r := zz["house_data"]; r.Len != saveFieldHouseData || r.Numeric {
		t.Fatalf("ZZ house_data region = %+v", r)
	}
	// The client copies 7000 stacks from the box and walks 30 carried ones.
	if r := zz["item_box"]; r.Off != 0x11A14 || r.Len != 56000 || !r.Stacks || r.Numeric {
		t.Fatalf("ZZ item_box region = %+v", r)
	}
	if r := zz["item_pouch"]; r.Off != 0x23E28 || r.Len != 240 || !r.Stacks {
		t.Fatalf("ZZ item_pouch region = %+v", r)
	}
	if r := zz["every_save"]; r.Off != 0x1F722 || r.Len != 8 || r.Stacks || r.Numeric {
		t.Fatalf("ZZ every_save region = %+v", r)
	}
	// The item tables are known for ZZ only; another version must not get
	// ZZ offsets.
	if _, ok := names(cfg.G10)["item_box"]; ok {
		t.Fatal("a non-ZZ version was given the ZZ item box")
	}
	// G10 has no verified zenny pointer; it must not be guessed at offset 0.
	if _, ok := names(cfg.G10)["zenny"]; ok {
		t.Fatal("unmapped zenny pointer produced a region")
	}
	if _, ok := names(cfg.G10)["hr"]; !ok {
		t.Fatal("mapped HR pointer produced no region")
	}
}
