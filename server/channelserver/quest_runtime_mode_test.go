package channelserver

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"erupe-ce/common/mhfquest"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func questRunStagePayload(questID uint16, flags uint8) []byte {
	payload := make([]byte, questRunStageBinaryPayloadSize)
	binary.LittleEndian.PutUint16(payload[questRunStageQuestIDOffset:questRunStageQuestIDOffset+2], questID)
	binary.LittleEndian.PutUint16(payload[questRunStageQuestIDMirrorOffset:questRunStageQuestIDMirrorOffset+2], questID)
	payload[questRunStageHardcoreFlagsOffset] = flags
	return payload
}

func TestDecodeQuestRunModeFromStageBinary(t *testing.T) {
	const questID = uint16(23303)
	tests := []struct {
		name        string
		stageID     string
		binaryType0 uint8
		binaryType1 uint8
		payload     []byte
		wantMode    questRunMode
		wantOK      bool
	}{
		{name: "normal", stageID: "sl2Qs200p0a1u0", binaryType0: 1, binaryType1: 3, payload: questRunStagePayload(questID, 0), wantMode: questRunModeNormal, wantOK: true},
		{name: "hardcore", stageID: "sl2Qs200p0a1u0", binaryType0: 1, binaryType1: 3, payload: questRunStagePayload(questID, 0x08), wantMode: questRunModeHardcore, wantOK: true},
		{name: "hardcore bit among other flags", stageID: "sl2Qs200p0a1u0", binaryType0: 1, binaryType1: 3, payload: questRunStagePayload(questID, 0x98), wantMode: questRunModeHardcore, wantOK: true},
		{name: "not a quest stage", stageID: "sl2Ns200p0a1u0", binaryType0: 1, binaryType1: 3, payload: questRunStagePayload(questID, 0)},
		{name: "wrong outer type", stageID: "sl2Qs200p0a1u0", binaryType0: 2, binaryType1: 3, payload: questRunStagePayload(questID, 0)},
		{name: "wrong index", stageID: "sl2Qs200p0a1u0", binaryType0: 1, binaryType1: 4, payload: questRunStagePayload(questID, 0)},
		{name: "short payload", stageID: "sl2Qs200p0a1u0", binaryType0: 1, binaryType1: 3, payload: make([]byte, questRunStageBinaryPayloadSize-1)},
		{name: "long payload", stageID: "sl2Qs200p0a1u0", binaryType0: 1, binaryType1: 3, payload: make([]byte, questRunStageBinaryPayloadSize+1)},
		{name: "zero quest ID", stageID: "sl2Qs200p0a1u0", binaryType0: 1, binaryType1: 3, payload: questRunStagePayload(0, 0)},
	}

	mismatched := questRunStagePayload(questID, 0)
	binary.LittleEndian.PutUint16(mismatched[questRunStageQuestIDMirrorOffset:questRunStageQuestIDMirrorOffset+2], questID+1)
	tests = append(tests, struct {
		name        string
		stageID     string
		binaryType0 uint8
		binaryType1 uint8
		payload     []byte
		wantMode    questRunMode
		wantOK      bool
	}{name: "mismatched mirrored quest ID", stageID: "sl2Qs200p0a1u0", binaryType0: 1, binaryType1: 3, payload: mismatched})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotQuestID, gotMode, gotOK := decodeQuestRunModeFromStageBinary(tt.stageID, tt.binaryType0, tt.binaryType1, tt.payload)
			if gotOK != tt.wantOK || gotMode != tt.wantMode {
				t.Fatalf("decode = (quest=%d, mode=%s, ok=%t), want mode=%s ok=%t", gotQuestID, gotMode, gotOK, tt.wantMode, tt.wantOK)
			}
			if tt.wantOK && gotQuestID != questID {
				t.Fatalf("quest ID = %d, want %d", gotQuestID, questID)
			}
		})
	}
}

func TestDecodeQuestRunModeFromCapturedLayout(t *testing.T) {
	// Literal offsets pin the decoder to the two captured quest 23303 runs
	// instead of constructing the fixture from the production constants.
	stagePayload := make([]byte, 144)
	stagePayload[0x24], stagePayload[0x25] = 0x07, 0x5b
	stagePayload[0x54] = 0x08
	stagePayload[0x68], stagePayload[0x69] = 0x07, 0x5b
	questID, mode, ok := decodeQuestRunModeFromStageBinary("sl2Qs200p0a1u0", 1, 3, stagePayload)
	if !ok || questID != 23303 || mode != questRunModeHardcore {
		t.Fatalf("captured stage layout = (%d, %s, %t)", questID, mode, ok)
	}

	recordPayload := make([]byte, 1196)
	recordPayload[0x3c4] = 1
	if got := decodeQuestRunModeFromRecordLog(recordPayload); got != questRunModeHardcore {
		t.Fatalf("captured record layout = %s, want hardcore", got)
	}
}

func TestDecodeQuestRunModeFromRecordLog(t *testing.T) {
	data := make([]byte, questRunRecordPayloadSize)
	if got := decodeQuestRunModeFromRecordLog(data); got != questRunModeNormal {
		t.Fatalf("zero mode = %s, want normal", got)
	}
	data[questRunRecordHardcoreModeOffset] = 1
	if got := decodeQuestRunModeFromRecordLog(data); got != questRunModeHardcore {
		t.Fatalf("one mode = %s, want hardcore", got)
	}
	data[questRunRecordHardcoreModeOffset] = 2
	if got := decodeQuestRunModeFromRecordLog(data); got != questRunModeUnknown {
		t.Fatalf("unexpected mode byte = %s, want unknown", got)
	}
	if got := decodeQuestRunModeFromRecordLog(data[:len(data)-1]); got != questRunModeUnknown {
		t.Fatalf("short record = %s, want unknown", got)
	}
	if got := decodeQuestRunModeFromRecordLog(append(data, 0)); got != questRunModeUnknown {
		t.Fatalf("long record = %s, want unknown", got)
	}
}

func TestResolveQuestRunVariant(t *testing.T) {
	tests := []struct {
		name                  string
		base                  mhfquest.HuntVariant
		stageMode, recordMode questRunMode
		want                  mhfquest.HuntVariant
		wantResolved          bool
	}{
		{name: "optional normal", base: mhfquest.HuntVariantHardcoreOptional, stageMode: questRunModeNormal, recordMode: questRunModeNormal, want: mhfquest.HuntVariantNormal, wantResolved: true},
		{name: "optional hardcore", base: mhfquest.HuntVariantHardcoreOptional, stageMode: questRunModeHardcore, recordMode: questRunModeHardcore, want: mhfquest.HuntVariantHardcore, wantResolved: true},
		{name: "stage only", base: mhfquest.HuntVariantHardcoreOptional, stageMode: questRunModeHardcore, want: mhfquest.HuntVariantHardcore, wantResolved: true},
		{name: "record alone rejected", base: mhfquest.HuntVariantHardcoreOptional, recordMode: questRunModeNormal, want: mhfquest.HuntVariantHardcoreOptional},
		{name: "unknown", base: mhfquest.HuntVariantHardcoreOptional, want: mhfquest.HuntVariantHardcoreOptional},
		{name: "conflict", base: mhfquest.HuntVariantHardcoreOptional, stageMode: questRunModeHardcore, recordMode: questRunModeNormal, want: mhfquest.HuntVariantHardcoreOptional},
		{name: "fixed HC unchanged", base: mhfquest.HuntVariantHardcore, stageMode: questRunModeNormal, recordMode: questRunModeNormal, want: mhfquest.HuntVariantHardcore, wantResolved: true},
		{name: "Zenith unchanged", base: mhfquest.HuntVariantZenith, stageMode: questRunModeHardcore, recordMode: questRunModeNormal, want: mhfquest.HuntVariantZenith, wantResolved: true},
		{name: "normal unchanged", base: mhfquest.HuntVariantNormal, stageMode: questRunModeHardcore, recordMode: questRunModeHardcore, want: mhfquest.HuntVariantNormal, wantResolved: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, resolved := resolveQuestRunVariant(tt.base, tt.stageMode, tt.recordMode)
			if got != tt.want || resolved != tt.wantResolved {
				t.Fatalf("resolve = (%q, %t), want (%q, %t)", got, resolved, tt.want, tt.wantResolved)
			}
		})
	}
}

func writeOptionalHardcoreQuestFixture(t *testing.T, binPath string, questID uint16) {
	t.Helper()
	questsDir := filepath.Join(binPath, "quests")
	if err := os.MkdirAll(questsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(fmt.Sprintf(`{"quest_id":%d,"quest_variant1":12}`, questID))
	path := filepath.Join(questsDir, fmt.Sprintf("%05dn0.json", questID))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func optionalHardcoreRecordData(questID uint16, elapsedFrames uint32, mode questRunMode, full bool) []byte {
	size := killLogHeaderSize + killLogMonsterCount
	if full {
		size = questRunRecordPayloadSize
	}
	data := make([]byte, size)
	binary.LittleEndian.PutUint16(data[questIDOffset:questIDOffset+2], questID)
	binary.LittleEndian.PutUint32(data[questElapsedFramesOffset:questElapsedFramesOffset+4], elapsedFrames)
	data[killLogHeaderSize+68] = 1
	if full && mode == questRunModeHardcore {
		data[questRunRecordHardcoreModeOffset] = 1
	}
	return data
}

func newOptionalHardcoreRecordTest(t *testing.T) (*Server, *Session, *mockHuntRecordRepo, uint16, string) {
	t.Helper()
	const questID = uint16(23303)
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.erupeConfig.BinPath = t.TempDir()
	writeOptionalHardcoreQuestFixture(t, server.erupeConfig.BinPath, questID)
	server.guildRepo = &mockGuildRepo{}
	repo := &mockHuntRecordRepo{}
	server.huntRecordRepo = repo

	stageID := "sl2Qs200p0a1u0"
	stage := NewStage(stageID)
	server.stages.Store(stageID, stage)
	session := createMockSession(1, server)
	session.stage = stage
	return server, session, repo, questID, stageID
}

func setOptionalHardcoreStageMode(session *Session, stageID string, questID uint16, mode questRunMode) {
	flags := uint8(0)
	if mode == questRunModeHardcore {
		flags = questRunStageHardcoreFlag
	}
	handleMsgSysSetStageBinary(session, &mhfpacket.MsgSysSetStageBinary{
		StageID:        stageID,
		BinaryType0:    questRunStageBinaryType0,
		BinaryType1:    questRunStageBinaryType1,
		RawDataPayload: questRunStagePayload(questID, flags),
	})
}

func TestOptionalHardcoreRecordsSplitNormalAndHardcore(t *testing.T) {
	_, session, repo, questID, stageID := newOptionalHardcoreRecordTest(t)

	setOptionalHardcoreStageMode(session, stageID, questID, questRunModeNormal)
	handleMsgSysRecordLog(session, &mhfpacket.MsgSysRecordLog{
		AckHandle: 1,
		Data:      optionalHardcoreRecordData(questID, 900, questRunModeNormal, true),
	})
	if got := session.peekQuestRunMode(questID); got != questRunModeUnknown {
		t.Fatalf("normal run state was not consumed: %s", got)
	}

	setOptionalHardcoreStageMode(session, stageID, questID, questRunModeHardcore)
	handleMsgSysRecordLog(session, &mhfpacket.MsgSysRecordLog{
		AckHandle: 2,
		Data:      optionalHardcoreRecordData(questID, 1_000, questRunModeHardcore, true),
	})

	if len(repo.records) != 2 {
		t.Fatalf("record count = %d, want 2", len(repo.records))
	}
	if got := repo.records[0].VariantKind; got != string(mhfquest.HuntVariantNormal) {
		t.Fatalf("first variant = %q, want normal", got)
	}
	if got := repo.records[1].VariantKind; got != string(mhfquest.HuntVariantHardcore) {
		t.Fatalf("second variant = %q, want hardcore", got)
	}
}

func TestOptionalHardcoreRecordRejection(t *testing.T) {
	t.Run("record alone", func(t *testing.T) {
		_, session, repo, questID, _ := newOptionalHardcoreRecordTest(t)
		handleMsgSysRecordLog(session, &mhfpacket.MsgSysRecordLog{
			AckHandle: 1,
			Data:      optionalHardcoreRecordData(questID, 900, questRunModeHardcore, true),
		})
		if len(repo.records) != 0 {
			t.Fatalf("record-only signal created %d records, want 0", len(repo.records))
		}
	})

	t.Run("conflicting signals", func(t *testing.T) {
		_, session, repo, questID, stageID := newOptionalHardcoreRecordTest(t)
		setOptionalHardcoreStageMode(session, stageID, questID, questRunModeHardcore)
		handleMsgSysRecordLog(session, &mhfpacket.MsgSysRecordLog{
			AckHandle: 1,
			Data:      optionalHardcoreRecordData(questID, 900, questRunModeNormal, true),
		})
		if len(repo.records) != 0 {
			t.Fatalf("conflicting signals created %d records, want 0", len(repo.records))
		}
	})

	t.Run("unknown mode", func(t *testing.T) {
		_, session, repo, questID, _ := newOptionalHardcoreRecordTest(t)
		handleMsgSysRecordLog(session, &mhfpacket.MsgSysRecordLog{
			AckHandle: 1,
			Data:      optionalHardcoreRecordData(questID, 900, questRunModeUnknown, false),
		})
		if len(repo.records) != 0 {
			t.Fatalf("unknown mode created %d records, want 0", len(repo.records))
		}
	})
}

func TestQuestRunModeClearedOnNonQuestStageTransfer(t *testing.T) {
	server, session, _, questID, _ := newOptionalHardcoreRecordTest(t)
	session.storeQuestRunMode(questID, questRunModeHardcore)
	nonQuestStageID := "sl2Ns200p0a1u0"
	server.stages.Store(nonQuestStageID, NewStage(nonQuestStageID))
	if !doStageTransfer(session, 1, nonQuestStageID) {
		t.Fatal("non-quest stage transfer failed")
	}
	if got := session.peekQuestRunMode(questID); got != questRunModeUnknown {
		t.Fatalf("run state after non-quest transfer = %s, want unknown", got)
	}
}
