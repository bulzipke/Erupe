package channelserver

import (
	"bytes"
	"encoding/binary"
	"testing"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"erupe-ce/server/channelserver/compression/nullcomp"
)

func TestHandleMsgMhfLoadLegendDispatch(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfLoadLegendDispatch{
		AckHandle: 12345,
	}

	handleMsgMhfLoadLegendDispatch(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

// --- NEW TESTS ---

// buildCatBytes constructs a binary cat data payload suitable for GetAirouDetails.
func buildCatBytes(cats []Airou) []byte {
	buf := new(bytes.Buffer)
	// catCount
	buf.WriteByte(byte(len(cats)))
	for _, cat := range cats {
		catBuf := new(bytes.Buffer)
		// ID (uint32)
		_ = binary.Write(catBuf, binary.BigEndian, cat.ID)
		// 1 byte skip (unknown bool)
		catBuf.WriteByte(0)
		// Name (18 bytes)
		name := make([]byte, 18)
		copy(name, cat.Name)
		catBuf.Write(name)
		// Task (uint8)
		catBuf.WriteByte(cat.Task)
		// 16 bytes skip (appearance data)
		catBuf.Write(make([]byte, 16))
		// Personality (uint8)
		catBuf.WriteByte(cat.Personality)
		// Class (uint8)
		catBuf.WriteByte(cat.Class)
		// 5 bytes skip (affection and colour sliders)
		catBuf.Write(make([]byte, 5))
		// Experience (uint32)
		_ = binary.Write(catBuf, binary.BigEndian, cat.Experience)
		// 1 byte skip (bool for weapon equipped)
		catBuf.WriteByte(0)
		// WeaponType (uint8)
		catBuf.WriteByte(cat.WeaponType)
		// WeaponID (uint16)
		_ = binary.Write(catBuf, binary.BigEndian, cat.WeaponID)

		catData := catBuf.Bytes()
		// catDefLen (uint32) - total length of the cat data after this field
		_ = binary.Write(buf, binary.BigEndian, uint32(len(catData)))
		buf.Write(catData)
	}
	return buf.Bytes()
}

func TestGetAirouDetails_Empty(t *testing.T) {
	// Zero cats
	data := []byte{0x00}
	bf := byteframe.NewByteFrameFromBytes(data)
	cats := GetAirouDetails(bf)

	if len(cats) != 0 {
		t.Errorf("Expected 0 cats, got %d", len(cats))
	}
}

func TestGetAirouDetails_SingleCat(t *testing.T) {
	input := Airou{
		ID:          42,
		Name:        []byte("TestCat"),
		Task:        4,
		Personality: 3,
		Class:       2,
		Experience:  1500,
		WeaponType:  6,
		WeaponID:    100,
	}

	data := buildCatBytes([]Airou{input})
	bf := byteframe.NewByteFrameFromBytes(data)
	cats := GetAirouDetails(bf)

	if len(cats) != 1 {
		t.Fatalf("Expected 1 cat, got %d", len(cats))
	}

	cat := cats[0]
	if cat.ID != 42 {
		t.Errorf("ID = %d, want 42", cat.ID)
	}
	if cat.Task != 4 {
		t.Errorf("Task = %d, want 4", cat.Task)
	}
	if cat.Personality != 3 {
		t.Errorf("Personality = %d, want 3", cat.Personality)
	}
	if cat.Class != 2 {
		t.Errorf("Class = %d, want 2", cat.Class)
	}
	if cat.Experience != 1500 {
		t.Errorf("Experience = %d, want 1500", cat.Experience)
	}
	if cat.WeaponType != 6 {
		t.Errorf("WeaponType = %d, want 6", cat.WeaponType)
	}
	if cat.WeaponID != 100 {
		t.Errorf("WeaponID = %d, want 100", cat.WeaponID)
	}
	// Name should be 18 bytes (padded with nulls)
	if len(cat.Name) != 18 {
		t.Errorf("Name length = %d, want 18", len(cat.Name))
	}
	// First bytes should match "TestCat"
	if !bytes.HasPrefix(cat.Name, []byte("TestCat")) {
		t.Errorf("Name does not start with 'TestCat', got %v", cat.Name)
	}
}

func TestGetAirouDetails_MultipleCats(t *testing.T) {
	inputs := []Airou{
		{ID: 1, Name: []byte("Alpha"), Task: 1, Personality: 0, Class: 0, Experience: 100, WeaponType: 6, WeaponID: 10},
		{ID: 2, Name: []byte("Beta"), Task: 2, Personality: 1, Class: 1, Experience: 200, WeaponType: 6, WeaponID: 20},
		{ID: 3, Name: []byte("Gamma"), Task: 4, Personality: 2, Class: 2, Experience: 300, WeaponType: 6, WeaponID: 30},
	}

	data := buildCatBytes(inputs)
	bf := byteframe.NewByteFrameFromBytes(data)
	cats := GetAirouDetails(bf)

	if len(cats) != 3 {
		t.Fatalf("Expected 3 cats, got %d", len(cats))
	}

	for i, cat := range cats {
		if cat.ID != inputs[i].ID {
			t.Errorf("Cat %d: CatID = %d, want %d", i, cat.ID, inputs[i].ID)
		}
		if cat.Task != inputs[i].Task {
			t.Errorf("Cat %d: CurrentTask = %d, want %d", i, cat.Task, inputs[i].Task)
		}
		if cat.Experience != inputs[i].Experience {
			t.Errorf("Cat %d: Experience = %d, want %d", i, cat.Experience, inputs[i].Experience)
		}
		if cat.WeaponID != inputs[i].WeaponID {
			t.Errorf("Cat %d: WeaponID = %d, want %d", i, cat.WeaponID, inputs[i].WeaponID)
		}
	}
}

func TestGetAirouDetails_ExtraTrailingBytes(t *testing.T) {
	// The GetAirouDetails function handles extra bytes by seeking to catStart+catDefLen.
	// Simulate a cat definition with extra trailing bytes by increasing catDefLen.
	buf := new(bytes.Buffer)
	buf.WriteByte(1) // catCount = 1

	catBuf := new(bytes.Buffer)
	_ = binary.Write(catBuf, binary.BigEndian, uint32(99))  // catID
	catBuf.WriteByte(0)                                     // skip
	catBuf.Write(make([]byte, 18))                          // name
	catBuf.WriteByte(3)                                     // currentTask
	catBuf.Write(make([]byte, 16))                          // appearance skip
	catBuf.WriteByte(1)                                     // personality
	catBuf.WriteByte(2)                                     // class
	catBuf.Write(make([]byte, 5))                           // affection skip
	_ = binary.Write(catBuf, binary.BigEndian, uint32(500)) // experience
	catBuf.WriteByte(0)                                     // weapon equipped bool
	catBuf.WriteByte(6)                                     // weaponType
	_ = binary.Write(catBuf, binary.BigEndian, uint16(50))  // weaponID

	catData := catBuf.Bytes()
	// Add 10 extra trailing bytes
	extra := make([]byte, 10)
	catDataWithExtra := append(catData, extra...)

	_ = binary.Write(buf, binary.BigEndian, uint32(len(catDataWithExtra)))
	buf.Write(catDataWithExtra)

	bf := byteframe.NewByteFrameFromBytes(buf.Bytes())
	cats := GetAirouDetails(bf)

	if len(cats) != 1 {
		t.Fatalf("Expected 1 cat, got %d", len(cats))
	}
	if cats[0].ID != 99 {
		t.Errorf("ID = %d, want 99", cats[0].ID)
	}
	if cats[0].Experience != 500 {
		t.Errorf("Experience = %d, want 500", cats[0].Experience)
	}
}

func TestGetAirouDetails_CatNamePadding(t *testing.T) {
	// Verify that names shorter than 18 bytes are correctly padded with null bytes.
	input := Airou{
		ID:   1,
		Name: []byte("Hi"),
	}

	data := buildCatBytes([]Airou{input})
	bf := byteframe.NewByteFrameFromBytes(data)
	cats := GetAirouDetails(bf)

	if len(cats) != 1 {
		t.Fatalf("Expected 1 cat, got %d", len(cats))
	}
	if len(cats[0].Name) != 18 {
		t.Errorf("Name length = %d, want 18", len(cats[0].Name))
	}
	// "Hi" followed by null bytes
	if cats[0].Name[0] != 'H' || cats[0].Name[1] != 'i' {
		t.Errorf("Name first bytes = %v, want 'Hi...'", cats[0].Name[:2])
	}
}

func TestHandleMsgMhfMercenaryHuntdata_RequestTypeIs1(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfMercenaryHuntdata{
		AckHandle:   12345,
		RequestType: 1,
	}

	handleMsgMhfMercenaryHuntdata(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfMercenaryHuntdata_RequestTypeIs0(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfMercenaryHuntdata{
		AckHandle:   12345,
		RequestType: 0,
	}

	handleMsgMhfMercenaryHuntdata(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfMercenaryHuntdata_RequestTypeIs2(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfMercenaryHuntdata{
		AckHandle:   12345,
		RequestType: 2,
	}

	handleMsgMhfMercenaryHuntdata(session, pkt)

	// RequestType=2 takes the else branch (same as 0)
	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfLoadLegendDispatch_Response(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfLoadLegendDispatch{
		AckHandle: 22222,
	}

	handleMsgMhfLoadLegendDispatch(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestAirouStruct(t *testing.T) {
	cat := Airou{
		ID:          42,
		Name:        []byte("TestCat"),
		Task:        4,
		Personality: 2,
		Class:       1,
		Experience:  1500,
		WeaponType:  6,
		WeaponID:    100,
	}

	if cat.ID != 42 {
		t.Errorf("ID = %d, want 42", cat.ID)
	}
	if cat.Task != 4 {
		t.Errorf("Task = %d, want 4", cat.Task)
	}
	if cat.Experience != 1500 {
		t.Errorf("Experience = %d, want 1500", cat.Experience)
	}
	if cat.WeaponType != 6 {
		t.Errorf("WeaponType = %d, want 6", cat.WeaponType)
	}
	if cat.WeaponID != 100 {
		t.Errorf("WeaponID = %d, want 100", cat.WeaponID)
	}
}

// TestHandleMsgMhfMercenaryHuntdata_Unk0_1 tests with Unk0=1 (returns 1 byte)
func TestHandleMsgMhfMercenaryHuntdata_Unk0_1(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfMercenaryHuntdata{
		AckHandle:   12345,
		RequestType: 1,
	}

	handleMsgMhfMercenaryHuntdata(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

// TestHandleMsgMhfMercenaryHuntdata_Unk0_0 tests with Unk0=0 (returns 0 bytes payload)
func TestHandleMsgMhfMercenaryHuntdata_Unk0_0(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfMercenaryHuntdata{
		AckHandle:   12345,
		RequestType: 0,
	}

	handleMsgMhfMercenaryHuntdata(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

// TestHandleMsgMhfEnumerateMercenaryLog tests the mercenary log enumeration handler
func TestHandleMsgMhfEnumerateMercenaryLog(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfEnumerateMercenaryLog{
		AckHandle: 12345,
	}

	handleMsgMhfEnumerateMercenaryLog(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfLoadPartner(t *testing.T) {
	server := createMockServer()
	server.charRepo = newMockCharacterRepo()
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfLoadPartner{AckHandle: 1}
	handleMsgMhfLoadPartner(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfSavePartner(t *testing.T) {
	server := createMockServer()
	server.charRepo = newMockCharacterRepo()
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfSavePartner{AckHandle: 1, RawDataPayload: []byte{1, 2, 3, 4}}
	handleMsgMhfSavePartner(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfLoadHunterNavi_G8(t *testing.T) {
	server := createMockServer()
	server.charRepo = newMockCharacterRepo()
	server.erupeConfig.RealClientMode = cfg.G10
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfLoadHunterNavi{AckHandle: 1}
	handleMsgMhfLoadHunterNavi(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfLoadHunterNavi_G7(t *testing.T) {
	server := createMockServer()
	server.charRepo = newMockCharacterRepo()
	server.erupeConfig.RealClientMode = cfg.G7
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfLoadHunterNavi{AckHandle: 1}
	handleMsgMhfLoadHunterNavi(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfSaveHunterNavi_NoDiff(t *testing.T) {
	server := createMockServer()
	server.charRepo = newMockCharacterRepo()
	session := createMockSession(100, server)

	data := make([]byte, 100)
	pkt := &mhfpacket.MsgMhfSaveHunterNavi{
		AckHandle:      1,
		IsDataDiff:     false,
		RawDataPayload: data,
	}
	handleMsgMhfSaveHunterNavi(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfSaveHunterNavi_Diff(t *testing.T) {
	server := createMockServer()
	charRepo := newMockCharacterRepo()
	charRepo.columns["hunternavi"] = make([]byte, 552)
	server.charRepo = charRepo
	server.erupeConfig.RealClientMode = cfg.G10
	session := createMockSession(100, server)

	// Create a valid diff payload (deltacomp format: pairs of offset+data)
	// A simple diff: zero length means no changes
	diffData := make([]byte, 4) // minimal diff
	pkt := &mhfpacket.MsgMhfSaveHunterNavi{
		AckHandle:      1,
		IsDataDiff:     true,
		RawDataPayload: diffData,
	}
	handleMsgMhfSaveHunterNavi(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfSaveHunterNavi_OversizedPayload(t *testing.T) {
	server := createMockServer()
	server.charRepo = newMockCharacterRepo()
	session := createMockSession(100, server)

	data := make([]byte, 5000) // > 4096
	pkt := &mhfpacket.MsgMhfSaveHunterNavi{
		AckHandle:      1,
		IsDataDiff:     false,
		RawDataPayload: data,
	}
	handleMsgMhfSaveHunterNavi(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfCreateMercenary_Success(t *testing.T) {
	server := createMockServer()
	server.mercenaryRepo = &mockMercenaryRepo{nextRastaID: 42}
	server.charRepo = newMockCharacterRepo()
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfCreateMercenary{AckHandle: 1}
	handleMsgMhfCreateMercenary(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfCreateMercenary_Error(t *testing.T) {
	server := createMockServer()
	server.mercenaryRepo = &mockMercenaryRepo{rastaIDErr: errNotFound}
	server.charRepo = newMockCharacterRepo()
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfCreateMercenary{AckHandle: 1}
	handleMsgMhfCreateMercenary(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfSaveMercenary_Normal(t *testing.T) {
	server := createMockServer()
	server.charRepo = newMockCharacterRepo()
	session := createMockSession(100, server)

	mercData := make([]byte, 100)
	// Write a uint32 index at the start
	mercData[0] = 0
	mercData[1] = 0
	mercData[2] = 0
	mercData[3] = 1
	pkt := &mhfpacket.MsgMhfSaveMercenary{
		AckHandle:  1,
		GCP:        500,
		PactMercID: 10,
		MercData:   mercData,
	}
	handleMsgMhfSaveMercenary(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfSaveMercenary_Oversized(t *testing.T) {
	server := createMockServer()
	server.charRepo = newMockCharacterRepo()
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfSaveMercenary{
		AckHandle: 1,
		MercData:  make([]byte, 70000),
	}
	handleMsgMhfSaveMercenary(session, pkt)
	<-session.sendPackets
}

type gearProfileTestRepo struct {
	*mockCharacterRepo
}

func (r gearProfileTestRepo) SaveMercenary(_ uint32, data []byte, _ uint32) error {
	r.columns["savemercenary"] = bytes.Clone(data)
	return nil
}

func TestMercenaryGearProfileRoundTrip(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	charRepo := gearProfileTestRepo{newMockCharacterRepo()}
	charRepo.ints["rasta_id"] = 52
	charRepo.ints["gcp"] = 123456
	server.charRepo = charRepo
	server.mercenaryRepo = &mockMercenaryRepo{}
	session := createMockSession(100, server)

	const ackHeader = 10
	payload := func() []byte {
		frame := (<-session.sendPackets).data
		if len(frame) < ackHeader || frame[6] != 1 || frame[7] != 0 ||
			int(binary.BigEndian.Uint16(frame[8:10])) != len(frame)-ackHeader {
			t.Fatalf("invalid buffered ACK: %x", frame)
		}
		return frame[ackHeader:]
	}
	profile := make([]byte, mercenaryProfileWithAppearance)
	copy(profile[mercenaryNativeSize:], mercenaryGearVersion)
	profile[mercenaryNativeSize+4] = 0x9c
	profile[mercenaryNativeSize+7] = 0x12
	copy(profile[mercenaryProfileWithGear:], mercenaryAppearanceVersion)
	binary.LittleEndian.PutUint16(profile[mercenaryProfileWithGear+6:], 11704)
	handleMsgMhfSaveMercenary(session, &mhfpacket.MsgMhfSaveMercenary{
		AckHandle: 1, MercData: profile, GCP: 123456,
	})
	<-session.sendPackets
	if !bytes.Equal(charRepo.columns["savemercenary"], profile) {
		t.Fatal("saved profile lost the gear suffix")
	}

	handleMsgMhfReadMercenaryW(session, &mhfpacket.MsgMhfReadMercenaryW{AckHandle: 2})
	own := payload()
	const prefix = 3 // lend count, loan count, own profile present
	gpOffset := prefix + mercenaryNativeSize
	if len(own) != gpOffset+4+len(mercenaryAppearanceVersion)+len(mercenaryGearVersion) ||
		binary.BigEndian.Uint32(own[gpOffset:gpOffset+4]) != 123456 ||
		string(own[gpOffset+4:]) != mercenaryAppearanceVersion+mercenaryGearVersion {
		t.Fatalf("self read changed the GP field or lost capability: %x", own)
	}

	for _, request := range []struct {
		mercID uint32
		size   int
	}{
		{52, mercenaryProfileWithGear},
		{52 | mercenaryAppearanceRequest, mercenaryProfileWithAppearance},
		{mercenaryAppearanceRequest, mercenaryProfileWithAppearance},
	} {
		handleMsgMhfReadMercenaryM(session, &mhfpacket.MsgMhfReadMercenaryM{AckHandle: 3, CharID: 200, MercID: request.mercID})
		other := payload()
		if len(other) != request.size || binary.BigEndian.Uint32(other[:4]) != 52 ||
			binary.BigEndian.Uint32(other[4:8]) != 200 || !bytes.Equal(other[8:], profile[8:request.size]) {
			t.Fatalf("counterpart profile for request %x: %x", request.mercID, other)
		}
	}
}

func TestHandleMsgMhfReadMercenaryM_RestoresRegisteredSigils(t *testing.T) {
	const owner, rastaID = 200, 52
	const weaponSlot, sigilSize = 32, 16
	const gearBox, registeredWeapon = 0x124, 0x1fdd0
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	repo := newMockCharacterRepo()
	repo.ints["rasta_id"] = rastaID
	server.charRepo = repo
	session := createMockSession(100, server)
	session.server.mercenaryRepo = &mockMercenaryRepo{}

	profile := make([]byte, mercenaryNativeSize)
	binary.BigEndian.PutUint32(profile[:4], rastaID)
	binary.BigEndian.PutUint32(profile[4:8], owner)
	copy(profile[0x2c:], []byte{1, 7, 0x0f, 0xb2, 0x64, 0x20, 0, 1, 0, 2, 0, 3, 0x12, 0x34, 0x56, 0x78})
	repo.columns["savemercenary"] = profile
	save := make([]byte, 150820)
	item := []byte{1, 7, 0xb2, 0x0f, 0x20, 0x64, 1, 0, 2, 0, 3, 0, 0x78, 0x56, 0x34, 0x12}
	copy(save[gearBox:], item) // Identical spare weapon without the registered sigils.
	copy(save[gearBox+weaponSlot*16:], item)
	binary.LittleEndian.PutUint16(save[registeredWeapon:], weaponSlot)
	plate := make([]byte, 140000)
	var expected [3 * sigilSize]byte
	for i, values := range [][7]uint16{
		{1, 1, 1, 9, 9, 11, 0},
		{1, 1, 1, 6, 11, 8, 0},
		{1, 1, 1, 7, 9, 11, 0},
	} {
		for j, value := range values {
			binary.BigEndian.PutUint16(plate[(weaponSlot*3+i)*sigilSize+j*2:], value)
			binary.LittleEndian.PutUint16(expected[i*sigilSize+j*2:], value)
		}
	}
	compress := func(raw []byte) []byte {
		t.Helper()
		data, err := nullcomp.Compress(raw)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	repo.columns["savedata"] = compress(save)
	repo.columns["platedata"] = compress(plate)

	read := func(id uint32) []byte {
		t.Helper()
		handleMsgMhfReadMercenaryM(session, &mhfpacket.MsgMhfReadMercenaryM{AckHandle: 7, CharID: owner, MercID: id})
		frame := (<-session.sendPackets).data
		if len(frame) < 10 || int(binary.BigEndian.Uint16(frame[8:10])) != len(frame)-10 {
			t.Fatalf("invalid buffered ACK: %x", frame)
		}
		return frame[10:]
	}
	accept := uint32(rastaID) | mercenaryAppearanceRequest
	for _, id := range []uint32{accept, mercenaryAppearanceRequest} {
		received := read(id)
		if len(received) != mercenaryProfileWithGear || !mercenaryHasGear(received) ||
			!bytes.Equal(received[:mercenaryNativeSize], profile) ||
			!bytes.Equal(received[mercenaryNativeSize+8:], expected[:]) {
			t.Fatalf("registered sigils missing or changed: %x", received)
		}
	}
	if received := read(rastaID); !bytes.Equal(received, profile) {
		t.Fatal("gear suffix sent to a client without capability")
	}
	if received := read(accept + 1); !bytes.Equal(received, profile) {
		t.Fatal("sigils joined to a different Rasta profile")
	}
	if received := repo.columns["savemercenary"]; !bytes.Equal(received, profile) || len(received) != mercenaryNativeSize {
		t.Fatal("a profile read rewrote savemercenary")
	}

	copy(plate[:3*sigilSize], plate[weaponSlot*3*sigilSize:(weaponSlot+1)*3*sigilSize])
	repo.columns["platedata"] = compress(plate[:weaponSlot*3*sigilSize])
	if received := read(accept); !bytes.Equal(received, profile) {
		t.Fatal("truncated plate table attached a different weapon's sigils")
	}
	repo.columns["platedata"] = compress(plate)
	save[gearBox+weaponSlot*16+6] ^= 1
	repo.columns["savedata"] = compress(save)
	if received := read(accept); !bytes.Equal(received, profile) {
		t.Fatal("a saved item with different decorations was treated as the registered weapon")
	}
}

type mercenaryEscortTestRepo struct {
	mockMercenaryRepo
	borrowerID uint32
	pact       MercenaryLoan
	escort     bool
}

func (r *mercenaryEscortTestRepo) GetMercenaryPact(charID uint32) (MercenaryLoan, bool, error) {
	if charID != r.borrowerID {
		return MercenaryLoan{}, false, nil
	}
	return r.pact, r.escort, nil
}

func TestHandleMsgMhfReadMercenaryM_ContractEscort(t *testing.T) {
	const borrowerID, lenderID, pactID = 100, 200, 52
	tests := []struct {
		name     string
		mode     cfg.Mode
		charID   uint32
		mercID   uint32
		gear     bool
		escort   bool
		lender   byte
		wantFlag byte
	}{
		{"allow on native profile", cfg.ZZ, lenderID, pactID, false, true, 0, 1},
		{"allow with appearance capability", cfg.ZZ, lenderID, pactID | mercenaryAppearanceRequest, true, true, 0, 1},
		{"prohibit on extended profile", cfg.ZZ, lenderID, pactID, true, false, 1, 0},
		{"look up without contract ID", cfg.ZZ, lenderID, 0, true, true, 0, 0},
		{"look up with different contract ID", cfg.ZZ, lenderID, 53, false, true, 0, 0},
		{"look up another lender", cfg.ZZ, 300, pactID, false, true, 0, 0},
		{"read legacy profile", cfg.Z2, lenderID, pactID, false, true, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.RealClientMode = tc.mode
			server.mercenaryRepo = &mercenaryEscortTestRepo{
				borrowerID: borrowerID,
				pact:       MercenaryLoan{CharID: lenderID, PactID: pactID},
				escort:     tc.escort,
			}
			profile := make([]byte, mercenaryNativeSize)
			profile[8] = tc.lender
			profile[9] = 0x37
			if tc.gear {
				profile = append(profile, make([]byte, mercenaryGearSize)...)
				copy(profile[mercenaryNativeSize:], mercenaryGearVersion)
				profile[len(profile)-1] = 0xa5
			}
			charRepo := newMockCharacterRepo()
			charRepo.columns["savemercenary"] = profile
			charRepo.ints["rasta_id"] = pactID
			server.charRepo = charRepo
			session := createMockSession(borrowerID, server)

			want := bytes.Clone(profile)
			if tc.mode == cfg.ZZ {
				binary.BigEndian.PutUint32(want[:4], pactID)
				binary.BigEndian.PutUint32(want[4:8], tc.charID)
			}
			want[8] = tc.wantFlag
			handleMsgMhfReadMercenaryM(session, &mhfpacket.MsgMhfReadMercenaryM{
				AckHandle: 1, CharID: tc.charID, MercID: tc.mercID,
			})
			const ackHeader = 10
			frame := (<-session.sendPackets).data
			if len(frame) != ackHeader+len(want) || frame[6] != 1 || frame[7] != 0 ||
				int(binary.BigEndian.Uint16(frame[8:10])) != len(want) {
				t.Fatalf("invalid profile ACK: %x", frame)
			}
			if got := frame[ackHeader:]; !bytes.Equal(got, want) {
				t.Fatalf("escort byte = %d, want %d; other profile bytes identical: %t",
					got[8], want[8], bytes.Equal(got[:8], want[:8]) && bytes.Equal(got[9:], want[9:]))
			}
			if profile[8] != tc.lender {
				t.Fatal("lender's saved profile was modified")
			}
		})
	}
}

func TestHandleMsgMhfReadMercenaryM_EmptyData(t *testing.T) {
	server := createMockServer()
	server.charRepo = newMockCharacterRepo()
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfReadMercenaryM{AckHandle: 1, CharID: 200}
	handleMsgMhfReadMercenaryM(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfReadMercenaryM_WithData(t *testing.T) {
	server := createMockServer()
	charRepo := newMockCharacterRepo()
	charRepo.columns["savemercenary"] = []byte{0x01, 0x02, 0x03, 0x04}
	server.charRepo = charRepo
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfReadMercenaryM{AckHandle: 1, CharID: 100}
	handleMsgMhfReadMercenaryM(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfContractMercenary_Op0(t *testing.T) {
	server := createMockServer()
	server.charRepo = newMockCharacterRepo()
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfContractMercenary{AckHandle: 1, Op: 0, CID: 200, PactMercID: 42}
	handleMsgMhfContractMercenary(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfContractMercenary_Op1(t *testing.T) {
	server := createMockServer()
	server.charRepo = newMockCharacterRepo()
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfContractMercenary{AckHandle: 1, Op: 1}
	handleMsgMhfContractMercenary(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfContractMercenary_Op2(t *testing.T) {
	server := createMockServer()
	server.charRepo = newMockCharacterRepo()
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfContractMercenary{AckHandle: 1, Op: 2, CID: 200}
	handleMsgMhfContractMercenary(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfReadMercenaryW_NoPact(t *testing.T) {
	server := createMockServer()
	charRepo := newMockCharacterRepo()
	server.charRepo = charRepo
	server.mercenaryRepo = &mockMercenaryRepo{}
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfReadMercenaryW{AckHandle: 1, Op: 0}
	handleMsgMhfReadMercenaryW(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfReadMercenaryW_WithPact(t *testing.T) {
	server := createMockServer()
	charRepo := newMockCharacterRepo()
	charRepo.ints["pact_id"] = 42
	server.charRepo = charRepo
	server.mercenaryRepo = &mockMercenaryRepo{}
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfReadMercenaryW{AckHandle: 1, Op: 0}
	handleMsgMhfReadMercenaryW(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfReadMercenaryW_Op2(t *testing.T) {
	server := createMockServer()
	charRepo := newMockCharacterRepo()
	server.charRepo = charRepo
	server.mercenaryRepo = &mockMercenaryRepo{}
	session := createMockSession(100, server)

	// Op 2 skips loan enumeration
	pkt := &mhfpacket.MsgMhfReadMercenaryW{AckHandle: 1, Op: 2}
	handleMsgMhfReadMercenaryW(session, pkt)
	<-session.sendPackets
}

func TestHandleMsgMhfLoadOtomoAirou(t *testing.T) {
	server := createMockServer()
	server.charRepo = newMockCharacterRepo()
	session := createMockSession(100, server)

	pkt := &mhfpacket.MsgMhfLoadOtomoAirou{AckHandle: 1}
	handleMsgMhfLoadOtomoAirou(session, pkt)
	<-session.sendPackets
}
