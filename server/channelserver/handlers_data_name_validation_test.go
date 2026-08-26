package channelserver

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"erupe-ce/common/bfutil"
	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"erupe-ce/network"
	"erupe-ce/network/mhfpacket"
	"erupe-ce/server/channelserver/compression/nullcomp"
)

func characterSaveBlob(t *testing.T, name string) []byte {
	t.Helper()
	save := make([]byte, 150000)
	copy(save[saveFieldNameOffset:], stringsupport.UTF8ToSJIS(name))
	return save
}

func TestRejectedNewCharacterNameClosesSession(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.ngWordFilter = stringsupport.NewNGWordFilter([]string{"시발"})
	repo := newMockCharacterRepo()
	repo.loadSaveDataID = 42
	repo.loadSaveDataNew = true
	server.charRepo = repo

	conn := &mockConn{}
	session := createMockSession(42, server)
	session.Name = ""
	session.rawConn = conn
	session.done = make(chan struct{})

	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{
		AckHandle:      1,
		RawDataPayload: characterSaveBlob(t, "시발"),
	})

	if !session.closed.Load() {
		t.Fatal("rejected initial character name did not close the session")
	}
	if !conn.WasClosed() {
		t.Fatal("rejected initial character name did not close the connection")
	}
	if session.Name != "" {
		t.Fatalf("rejected name became the session name: %q", session.Name)
	}
	if len(repo.saveAtomicParams) != 0 {
		t.Fatal("rejected character name reached the atomic save")
	}
}

func TestInvalidStoredCharacterNameIsRejectedAtLogin(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.DebugOptions.DisableTokenCheck = true
	server.ngWordFilter = stringsupport.NewNGWordFilter([]string{"시발"})
	repo := newMockCharacterRepo()
	repo.strings["name"] = "시발"
	server.charRepo = repo
	server.sessionRepo = &mockSessionRepo{}

	conn := &mockConn{}
	session := createMockSession(0, server)
	session.rawConn = conn
	session.done = make(chan struct{})
	handleMsgSysLogin(session, &mhfpacket.MsgSysLogin{
		AckHandle:        10,
		CharID0:          42,
		LoginTokenString: "valid-token",
	})

	if !session.closed.Load() || !conn.WasClosed() {
		t.Fatal("invalid stored character name was not rejected during login")
	}
	if session.charID != 0 {
		t.Fatalf("rejected login committed charID %d and could trigger logout saves", session.charID)
	}
	if len(session.sendPackets) != 0 {
		t.Fatal("login acknowledgement was queued before stored-name validation")
	}
}

func TestAcceptedNewCharacterNameIsSavedAtomically(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.ngWordFilter = stringsupport.NewNGWordFilter([]string{"시발"})
	repo := newMockCharacterRepo()
	repo.loadSaveDataID = 42
	repo.loadSaveDataNew = true
	server.charRepo = repo

	session := createMockSession(42, server)
	session.Name = ""
	session.done = make(chan struct{})
	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{
		AckHandle:      11,
		RawDataPayload: characterSaveBlob(t, "헌터"),
	})

	if session.closed.Load() {
		t.Fatal("valid initial character name closed the session")
	}
	if session.Name != "헌터" {
		t.Fatalf("session name = %q, want 헌터", session.Name)
	}
	if len(repo.saveAtomicParams) != 1 {
		t.Fatalf("atomic saves = %d, want 1", len(repo.saveAtomicParams))
	}
	if got := repo.saveAtomicParams[0].Name; got != "헌터" {
		t.Fatalf("atomic character name = %q, want 헌터", got)
	}
}

func TestExistingCharacterNameTamperIsRestoredBeforeSave(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.ngWordFilter = stringsupport.NewNGWordFilter([]string{"시발"})
	repo := newMockCharacterRepo()
	repo.loadSaveDataID = 42
	repo.loadSaveDataNew = false
	repo.loadSaveDataName = "Hunter"
	repo.strings["name"] = "Hunter"
	repo.loadSaveDataData, _ = nullcomp.Compress(characterSaveBlob(t, "Hunter"))
	server.charRepo = repo

	session := createMockSession(42, server)
	session.Name = "Hunter"
	session.done = make(chan struct{})
	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{
		AckHandle:      2,
		RawDataPayload: characterSaveBlob(t, "시발"),
	})

	if session.closed.Load() {
		t.Fatal("valid existing character session was closed")
	}
	if len(repo.saveAtomicParams) != 1 {
		t.Fatalf("atomic saves = %d, want 1", len(repo.saveAtomicParams))
	}
	saved, err := nullcomp.Decompress(repo.saveAtomicParams[0].CompSave)
	if err != nil {
		t.Fatalf("decompress saved character: %v", err)
	}
	got := stringsupport.SJISToUTF8Lossy(bfutil.UpToNull(saved[saveFieldNameOffset : saveFieldNameOffset+saveFieldNameLen]))
	if got != "Hunter" {
		t.Fatalf("savedata name = %q, want server-authoritative name", got)
	}
	if gotDB := repo.saveAtomicParams[0].Name; gotDB != "Hunter" {
		t.Fatalf("atomic repository name = %q, want Hunter", gotDB)
	}
}

func TestCorruptExistingCharacterNameIsRejectedBeforeRepair(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.ngWordFilter = stringsupport.NewNGWordFilter([]string{"시발"})
	repo := newMockCharacterRepo()
	repo.loadSaveDataID = 42
	repo.loadSaveDataNew = false
	repo.loadSaveDataName = "Hunter"
	repo.loadSaveDataData, _ = nullcomp.Compress(characterSaveBlob(t, "Hunter"))
	server.charRepo = repo

	corrupt := characterSaveBlob(t, "Hunter")
	corrupt[saveFieldNameOffset] = 0x0b
	session := createMockSession(42, server)
	session.Name = "Hunter"
	session.done = make(chan struct{})
	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{
		AckHandle:      12,
		RawDataPayload: corrupt,
	})

	if len(repo.saveAtomicParams) != 0 {
		t.Fatal("corrupted incoming savedata was repaired and persisted")
	}
	if len(session.sendPackets) != 1 {
		t.Fatalf("responses = %d, want one failed acknowledgement", len(session.sendPackets))
	}
}

func TestShortIncomingSavedataIsRejectedWithoutPanic(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.ngWordFilter = stringsupport.NewNGWordFilter(nil)
	repo := newMockCharacterRepo()
	repo.loadSaveDataID = 42
	repo.loadSaveDataNew = false
	repo.loadSaveDataName = "Hunter"
	repo.loadSaveDataData, _ = nullcomp.Compress(characterSaveBlob(t, "Hunter"))
	server.charRepo = repo

	session := createMockSession(42, server)
	session.Name = "Hunter"
	session.done = make(chan struct{})
	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{
		AckHandle:      13,
		RawDataPayload: make([]byte, 100),
	})

	if len(repo.saveAtomicParams) != 0 {
		t.Fatal("short savedata reached persistence")
	}
	if len(session.sendPackets) != 1 {
		t.Fatalf("responses = %d, want one failed acknowledgement", len(session.sendPackets))
	}
}

func TestInvalidPrimaryLayoutRecoversFromBackup(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.ngWordFilter = stringsupport.NewNGWordFilter(nil)
	repo := newMockCharacterRepo()
	repo.loadSaveDataID = 42
	repo.loadSaveDataNew = false
	repo.loadSaveDataName = "Hunter"
	repo.loadSaveDataData = make([]byte, 100)
	backup, _ := nullcomp.Compress(characterSaveBlob(t, "Hunter"))
	repo.loadBackups = []SavedataBackup{{Slot: 1, Data: backup}}
	server.charRepo = repo

	session := createMockSession(42, server)
	character, err := GetCharacterSaveData(session, 42)
	if err != nil {
		t.Fatalf("recover invalid primary layout: %v", err)
	}
	if character.Name != "Hunter" {
		t.Fatalf("recovered character name = %q, want Hunter", character.Name)
	}
}

func TestValidateLayoutPreservesLegacyDebugClientModes(t *testing.T) {
	save := &CharacterSaveData{
		Mode:       cfg.S7,
		Pointers:   getPointers(cfg.S7),
		decompSave: make([]byte, 150000),
	}
	if err := save.validateLayout(); err != nil {
		t.Fatalf("legacy debug client layout was newly rejected: %v", err)
	}
}

func TestValidateLayoutRejectsShortF4RPField(t *testing.T) {
	pointers := getPointers(cfg.F4)
	save := &CharacterSaveData{
		Mode:       cfg.F4,
		Pointers:   pointers,
		decompSave: make([]byte, pointers[pRP]+saveFieldRP-1),
	}
	if err := save.validateLayout(); err == nil {
		t.Fatal("F4 savedata missing the final RP byte passed layout validation")
	}
}

func TestLoadDataRepairsImportedCharacterName(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.ngWordFilter = stringsupport.NewNGWordFilter([]string{"시발"})
	server.userBinary = NewUserBinaryStore()
	repo := newMockCharacterRepo()
	repo.loadSaveDataID = 42
	repo.loadSaveDataNew = false
	repo.loadSaveDataName = "Hunter"
	repo.loadSaveDataData, _ = nullcomp.Compress(characterSaveBlob(t, "시발"))
	server.charRepo = repo

	session := createMockSession(42, server)
	session.Name = "Hunter"
	session.done = make(chan struct{})
	handleMsgMhfLoaddata(session, &mhfpacket.MsgMhfLoaddata{AckHandle: 3})

	if session.closed.Load() {
		t.Fatal("load of repairable imported savedata closed the session")
	}
	if session.Name != "Hunter" {
		t.Fatalf("session name = %q, want Hunter", session.Name)
	}
	queued := <-session.sendPackets
	bf := byteframe.NewByteFrameFromBytes(queued.data)
	if opcode := network.PacketID(bf.ReadUint16()); opcode != network.MSG_SYS_ACK {
		t.Fatalf("response opcode = %s, want MSG_SYS_ACK", opcode)
	}
	ack := &mhfpacket.MsgSysAck{}
	if err := ack.Parse(bf, session.clientContext); err != nil {
		t.Fatalf("parse load ack: %v", err)
	}
	loaded, err := nullcomp.Decompress(ack.AckData)
	if err != nil {
		t.Fatalf("decompress load response: %v", err)
	}
	got := stringsupport.SJISToUTF8Lossy(bfutil.UpToNull(loaded[saveFieldNameOffset : saveFieldNameOffset+saveFieldNameLen]))
	if got != "Hunter" {
		t.Fatalf("loaded savedata name = %q, want Hunter", got)
	}
}

func TestLoadDataRejectsInvalidStoredCharacterName(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.ngWordFilter = stringsupport.NewNGWordFilter([]string{"시발"})
	server.userBinary = NewUserBinaryStore()
	repo := newMockCharacterRepo()
	repo.loadSaveDataID = 42
	repo.loadSaveDataNew = false
	repo.loadSaveDataName = "시발"
	repo.loadSaveDataData, _ = nullcomp.Compress(characterSaveBlob(t, "시발"))
	server.charRepo = repo

	conn := &mockConn{}
	session := createMockSession(42, server)
	session.rawConn = conn
	session.done = make(chan struct{})
	handleMsgMhfLoaddata(session, &mhfpacket.MsgMhfLoaddata{AckHandle: 4})

	if !session.closed.Load() || !conn.WasClosed() {
		t.Fatal("invalid stored character name was allowed to load")
	}
	if len(session.sendPackets) != 0 {
		t.Fatal("savedata was sent before the stored name was rejected")
	}
}

func TestLoadDataRejectsInvalidImportedNewCharacterName(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.ngWordFilter = stringsupport.NewNGWordFilter([]string{"시발"})
	server.userBinary = NewUserBinaryStore()
	repo := newMockCharacterRepo()
	repo.loadSaveDataID = 42
	repo.loadSaveDataNew = true
	repo.loadSaveDataName = ""
	repo.loadSaveDataData, _ = nullcomp.Compress(characterSaveBlob(t, "시발"))
	server.charRepo = repo

	conn := &mockConn{}
	session := createMockSession(42, server)
	session.rawConn = conn
	session.done = make(chan struct{})
	handleMsgMhfLoaddata(session, &mhfpacket.MsgMhfLoaddata{AckHandle: 5})

	if !session.closed.Load() || !conn.WasClosed() {
		t.Fatal("invalid name imported into a new slot was allowed to load")
	}
	if len(session.sendPackets) != 0 {
		t.Fatal("imported savedata was sent before the new name was rejected")
	}
}

func TestLoadDataRepairsNameInSaveOverride(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.erupeConfig.BinPath = t.TempDir()
	server.ngWordFilter = stringsupport.NewNGWordFilter([]string{"시발"})
	server.userBinary = NewUserBinaryStore()
	repo := newMockCharacterRepo()
	repo.loadSaveDataID = 42
	repo.loadSaveDataNew = false
	repo.loadSaveDataName = "Hunter"
	repo.strings["name"] = "Hunter"
	repo.loadSaveDataData, _ = nullcomp.Compress(characterSaveBlob(t, "Hunter"))
	server.charRepo = repo

	override, _ := nullcomp.Compress(characterSaveBlob(t, "시발"))
	if err := os.WriteFile(filepath.Join(server.erupeConfig.BinPath, "save_override.bin"), override, 0600); err != nil {
		t.Fatalf("write save override: %v", err)
	}

	session := createMockSession(42, server)
	session.Name = "Hunter"
	session.done = make(chan struct{})
	handleMsgMhfLoaddata(session, &mhfpacket.MsgMhfLoaddata{AckHandle: 14})

	if session.closed.Load() {
		t.Fatal("repairable save override closed the session")
	}
	queued := <-session.sendPackets
	bf := byteframe.NewByteFrameFromBytes(queued.data)
	if opcode := network.PacketID(bf.ReadUint16()); opcode != network.MSG_SYS_ACK {
		t.Fatalf("response opcode = %s, want MSG_SYS_ACK", opcode)
	}
	ack := &mhfpacket.MsgSysAck{}
	if err := ack.Parse(bf, session.clientContext); err != nil {
		t.Fatalf("parse load ack: %v", err)
	}
	loaded, err := nullcomp.Decompress(ack.AckData)
	if err != nil {
		t.Fatalf("decompress override response: %v", err)
	}
	got := stringsupport.SJISToUTF8Lossy(bfutil.UpToNull(loaded[saveFieldNameOffset : saveFieldNameOffset+saveFieldNameLen]))
	if got != "Hunter" {
		t.Fatalf("override savedata name = %q, want Hunter", got)
	}
	if len(repo.saveAtomicParams) != 0 {
		t.Fatal("non-persistent save override was written to the database")
	}
}

func TestLoadDataRejectsInvalidNameInNewCharacterSaveOverride(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.erupeConfig.BinPath = t.TempDir()
	server.ngWordFilter = stringsupport.NewNGWordFilter([]string{"시발"})
	server.userBinary = NewUserBinaryStore()
	repo := newMockCharacterRepo()
	repo.loadSaveDataID = 42
	repo.loadSaveDataNew = true
	repo.bools["is_new_character"] = true
	repo.loadSaveDataData, _ = nullcomp.Compress(characterSaveBlob(t, ""))
	server.charRepo = repo

	override, _ := nullcomp.Compress(characterSaveBlob(t, "시발"))
	if err := os.WriteFile(filepath.Join(server.erupeConfig.BinPath, "save_override.bin"), override, 0600); err != nil {
		t.Fatalf("write save override: %v", err)
	}

	conn := &mockConn{}
	session := createMockSession(42, server)
	session.rawConn = conn
	session.done = make(chan struct{})
	handleMsgMhfLoaddata(session, &mhfpacket.MsgMhfLoaddata{AckHandle: 15})

	if !session.closed.Load() || !conn.WasClosed() {
		t.Fatal("invalid new-character name in save override was allowed to load")
	}
	if len(session.sendPackets) != 0 {
		t.Fatal("save override was sent before its name was rejected")
	}
}

func TestPacketGroupStopsAfterHandlerClosesSession(t *testing.T) {
	server := createMockServer()
	firstCalls := 0
	secondCalls := 0
	server.handlerTable[network.MSG_SYS_NOP] = func(s *Session, _ mhfpacket.MHFPacket) {
		firstCalls++
		s.markClosed()
	}
	server.handlerTable[network.MSG_SYS_END] = func(_ *Session, _ mhfpacket.MHFPacket) {
		secondCalls++
	}

	session := createMockSession(42, server)
	session.done = make(chan struct{})
	group := make([]byte, 4)
	binary.BigEndian.PutUint16(group[0:2], uint16(network.MSG_SYS_NOP))
	binary.BigEndian.PutUint16(group[2:4], uint16(network.MSG_SYS_END))

	session.handlePacketGroup(group, 0)

	if firstCalls != 1 {
		t.Fatalf("first handler calls = %d, want 1", firstCalls)
	}
	if secondCalls != 0 {
		t.Fatalf("batched handler ran after session close: %d calls", secondCalls)
	}
}
