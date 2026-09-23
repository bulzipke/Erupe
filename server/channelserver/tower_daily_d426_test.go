package channelserver

import (
	"bytes"
	"encoding/binary"
	"testing"

	"erupe-ce/network/mhfpacket"
)

func TestTowerDailyMissionTypesUseClientTable(t *testing.T) {
	for i, m := range towerDailyMissionPool {
		if m.Unk1 < 1 || m.Unk1 > 5 {
			t.Fatalf("mission %d uses type %d outside the client text table", i+1, m.Unk1)
		}
		if m.Unk2 <= 0 || (m.Unk1 == 2 && m.Unk2 > 100) {
			t.Fatalf("mission %d goal %d (TRP goals are in hundreds)", i+1, m.Unk2)
		}
	}
}

func TestTowerDailyMissionMetReadsClientTypes(t *testing.T) {
	trp := PaperMissionData{Unk1: 2, Unk2: 5}
	if towerDailyMissionMet(TowerDailyCounters{TRP: 499}, trp) {
		t.Fatal("499 TRP met a 500 TRP mission")
	}
	if !towerDailyMissionMet(TowerDailyCounters{TRP: 500}, trp) {
		t.Fatal("500 TRP did not meet a 500 TRP mission")
	}
	monsters := PaperMissionData{Unk1: 5, Unk2: 2}
	if !towerDailyMissionMet(TowerDailyCounters{Slays: 2}, monsters) {
		t.Fatal("two large monsters did not meet the monster mission")
	}
	if towerDailyMissionMet(TowerDailyCounters{Antiques: 9, TRP: 900}, monsters) {
		t.Fatal("the monster mission must read large-monster slays only")
	}
	antiques := PaperMissionData{Unk1: 4, Unk2: 3}
	if !towerDailyMissionMet(TowerDailyCounters{Antiques: 3}, antiques) {
		t.Fatal("three antiques did not meet the antique mission")
	}
}

func TestTowerDailyBinRoundTrip(t *testing.T) {
	repo := &mockTowerRepo{}
	server := createMockServer()
	server.towerRepo = repo
	s := createMockSession(1, server)
	blob := make([]byte, towerDailyBinSize)
	for i := range blob {
		blob[i] = byte(i + 1)
	}
	handleMsgMhfPostTinyBin(s, &mhfpacket.MsgMhfPostTinyBin{AckHandle: 1, Unk1: 1, Unk2: 1, Unk3: 1, Data: blob})
	<-s.sendPackets
	if !bytes.Equal(repo.savedBin, blob) {
		t.Fatalf("daily progress not stored: %x", repo.savedBin)
	}
	handleMsgMhfGetTinyBin(s, &mhfpacket.MsgMhfGetTinyBin{AckHandle: 2, Unk1: 1, Unk2: 1})
	_, code, payload := parseAckBufData(t, (<-s.sendPackets).data)
	if code != 0 || !bytes.Equal(payload, blob) {
		t.Fatalf("daily progress not returned: code=%d %x", code, payload)
	}
	handleMsgMhfPostTinyBin(s, &mhfpacket.MsgMhfPostTinyBin{AckHandle: 3, Unk1: 1, Unk2: 1, Unk3: 1, Data: blob[:8]})
	<-s.sendPackets
	if !bytes.Equal(repo.savedBin, blob) {
		t.Fatal("a short blob replaced the stored progress")
	}
	handleMsgMhfGetTinyBin(s, &mhfpacket.MsgMhfGetTinyBin{AckHandle: 4, Unk0: 1, Unk1: 1, Unk2: 1})
	_, _, payload = parseAckBufData(t, (<-s.sendPackets).data)
	if len(payload) != 0 {
		t.Fatalf("another tiny bin key returned the tower progress: %x", payload)
	}
}

func TestTowerSurveyHistoryInfoType4(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.EarthID = 3
	server.towerRepo = &mockTowerRepo{surveyHistory: [5]int32{12, 40, 0, 0, 0}}
	s := createMockSession(1, server)
	handleMsgMhfGetTowerInfo(s, &mhfpacket.MsgMhfGetTowerInfo{AckHandle: 1, InfoType: 4})
	_, code, payload := parseAckBufData(t, (<-s.sendPackets).data)
	if code != 0 || len(payload) != 16+20 || binary.BigEndian.Uint32(payload[12:16]) != 1 {
		t.Fatalf("history ack code=%d payload=%x", code, payload)
	}
	entry := payload[16:]
	for i, want := range []int16{3, 2, 1, 0, -1} {
		if got := int16(binary.BigEndian.Uint16(entry[i*2:])); got != want {
			t.Fatalf("round %d = %d, want %d", i, got, want)
		}
	}
	if binary.BigEndian.Uint16(entry[10:]) != 12 || binary.BigEndian.Uint16(entry[12:]) != 40 {
		t.Fatalf("history floors %x", entry[10:])
	}
}

func TestEarthValue1001IsTowerSurveyRound(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.EarthID = 3
	s := createMockSession(1, server)
	handleMsgMhfGetEarthValue(s, &mhfpacket.MsgMhfGetEarthValue{AckHandle: 1, ReqType: 3})
	_, _, payload := parseAckBufData(t, (<-s.sendPackets).data)
	if binary.BigEndian.Uint32(payload[16:20]) != 1001 || binary.BigEndian.Uint32(payload[20:24]) != 3 {
		t.Fatalf("earth value 1001 = %x", payload[16:24])
	}
}
