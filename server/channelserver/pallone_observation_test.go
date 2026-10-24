package channelserver

import (
	"encoding/binary"
	"strings"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestPalloneObservationIsOptInAndFiltersOtherQuests(t *testing.T) {
	s, _ := palloneHandlerSession()
	core, logs := observer.New(zap.DebugLevel)
	s.logger = zap.New(core)
	s.observePalloneFileReply("27438d0", 1, []byte("private body"))
	if logs.Len() != 0 {
		t.Fatal("diagnostics enabled by default")
	}
	s.server.erupeConfig.DebugOptions.QuestTools = true
	for _, name := range []string{"21189d0", "27438d3", "27438x0", "27438d0.bin", "27438d0/", "99999d0", "27438d", "2743xd0"} {
		s.observePalloneFileReply(name, 1, nil)
	}
	for n := 0; n < 16; n++ {
		s.observePalloneRecord(make([]byte, n))
	}
	s.observePalloneStage("stage_setup_accepted", 21189, "Qs1")
	if logs.Len() != 0 {
		t.Fatal("logged unrelated or malformed native input")
	}
	s.server.erupeConfig.RealClientMode = cfg.G101
	s.observePalloneFileReply("27438d0", 1, nil)
	if logs.Len() != 0 {
		t.Fatal("used ZZ diagnostics for another client")
	}
}

func TestPalloneObservationsNeverAuthorizeScoreOrDumpBodies(t *testing.T) {
	s, r := palloneHandlerSession()
	s.server.erupeConfig.DebugOptions.QuestTools = true
	core, logs := observer.New(zap.DebugLevel)
	s.logger = zap.New(core)
	s.observePalloneFileReply("27438n2", 22, []byte("private body not to be logged"))
	s.observePalloneStage("stage_setup_accepted", 27438, "Qs1")
	s.observePalloneStage("quest_stage_join_accepted", 27438, "Qs1")
	record := make([]byte, 40)
	binary.LittleEndian.PutUint16(record, 27365)
	record[questResultCodeOffset] = questResultCodeCleared
	binary.LittleEndian.PutUint32(record[questElapsedFramesOffset:], 900)
	copy(record[16:], "private record contents")
	s.observePalloneRecord(record)
	handleMsgMhfCaravanMyScore(s, &mhfpacket.MsgMhfCaravanMyScore{AckHandle: 23, Unk0: 2, Unk1: 3, Unk2: 7, Unk3: 100})
	ack := readAck(t, s)
	if ack.ErrorCode == 0 || ack.IsBufferResponse || s.palloneReceipt != nil || r.settled != 0 {
		t.Fatal("observations became a completion authorization")
	}
	entries := logs.FilterMessage("PalloneObservation").All()
	if len(entries) != 6 {
		t.Fatalf("expected file/stage/join/result/submit/reject observations, got %d", len(entries))
	}
	for _, entry := range entries {
		fields := entry.ContextMap()
		if fields["phase"] == nil || fields["charID"] == nil {
			t.Fatal("missing correlation fields")
		}
		for key, value := range fields {
			if key == "dataHex" || key == "payloadHex" || key == "name" || strings.Contains(strings.ToLower(key), "token") {
				t.Fatal("unexpected sensitive/raw field", key)
			}
			if text, ok := value.(string); ok && strings.Contains(text, "private") {
				t.Fatal("dumped private packet contents")
			}
		}
	}
	file, result := entries[0].ContextMap(), entries[3].ContextMap()
	if file["filename"] != "27438n2" || len(file["sha256"].(string)) != 64 || result["resultCode"] != uint8(4) || result["elapsedFrames"] != uint32(900) {
		t.Fatal("lost native variant/result fields", file, result)
	}
}

func TestPalloneObservationHooksFollowAcceptedMenuFileAndSetup(t *testing.T) {
	s, r := palloneHandlerSession()
	installPalloneTestProfile(t, s, r)
	s.server.erupeConfig.DebugOptions.QuestTools = true
	core, logs := observer.New(zap.DebugLevel)
	s.logger = zap.New(core)
	handleMsgMhfGetWeeklySeibatuRankingReward(s, &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{AckHandle: 81, Unk1: 3})
	if a := readAck(t, s); a.ErrorCode != 0 {
		t.Fatal("menu failed", a)
	}
	handleMsgSysGetFile(s, &mhfpacket.MsgSysGetFile{AckHandle: 82, Filename: "27438d0"})
	if a := readAck(t, s); a.ErrorCode != 0 {
		t.Fatal("file failed", a)
	}
	stageID := "sl1Qs27438p0a0u1"
	s.server.stages.Store(stageID, NewStage(stageID))
	handleMsgSysSetStageBinary(s, &mhfpacket.MsgSysSetStageBinary{StageID: stageID, BinaryType0: 1, BinaryType1: 3, RawDataPayload: questRunStagePayload(27438, 0)})
	seen := map[string]bool{}
	for _, entry := range logs.FilterMessage("PalloneObservation").All() {
		seen[entry.ContextMap()["phase"].(string)] = true
	}
	for _, phase := range []string{"delivery_reply_queued", "file_reply_queued", "stage_setup_accepted"} {
		if !seen[phase] {
			t.Fatal("successful handler did not emit observation", phase)
		}
	}
	if s.palloneReceipt != nil || r.settled != 0 {
		t.Fatal("observing accepted packets issued points or a receipt")
	}
}
