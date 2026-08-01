package channelserver

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"

	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func newQuestDiagnosticTestSession(enabled bool) (*Server, *Session, *observer.ObservedLogs) {
	server := createMockServer()
	server.erupeConfig.DebugOptions.QuestTools = enabled
	server.logger = zap.NewNop()
	core, logs := observer.New(zapcore.DebugLevel)
	session := createMockSession(42, server)
	session.Name = "DiagnosticHunter"
	session.logger = zap.New(core)
	return server, session, logs
}

func requireQuestDiagnosticField(t *testing.T, fields map[string]interface{}, key, want string) {
	t.Helper()
	got, exists := fields[key]
	if !exists {
		t.Fatalf("diagnostic field %q is missing", key)
	}
	if fmt.Sprint(got) != want {
		t.Fatalf("diagnostic field %q = %v, want %s", key, got, want)
	}
}

func TestQuestStageBinaryDiagnosticFollowsQuestTools(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			server, session, logs := newQuestDiagnosticTestSession(enabled)
			stageID := "sl1Qs54605p0a0u1"
			stage := NewStage(stageID)
			server.stages.Store(stageID, stage)
			payload := []byte{0x01, 0x23, 0x45, 0x67, 0x89}

			handleMsgSysSetStageBinary(session, &mhfpacket.MsgSysSetStageBinary{
				BinaryType0:    3,
				BinaryType1:    4,
				StageID:        stageID,
				RawDataPayload: payload,
			})

			entries := logs.FilterMessage("QuestStageBinaryDiagnostic").All()
			wantEntries := 0
			if enabled {
				wantEntries = 1
			}
			if len(entries) != wantEntries {
				t.Fatalf("diagnostic log entries = %d, want %d", len(entries), wantEntries)
			}
			if enabled {
				fields := entries[0].ContextMap()
				requireQuestDiagnosticField(t, fields, "charID", "42")
				requireQuestDiagnosticField(t, fields, "name", "DiagnosticHunter")
				requireQuestDiagnosticField(t, fields, "stageID", stageID)
				requireQuestDiagnosticField(t, fields, "binaryType0", "3")
				requireQuestDiagnosticField(t, fields, "binaryType1", "4")
				requireQuestDiagnosticField(t, fields, "payloadBytes", "5")
				requireQuestDiagnosticField(t, fields, "payloadHex", hex.EncodeToString(payload))
			}
		})
	}
}

func TestQuestRecordLogDiagnosticFollowsQuestTools(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			_, session, logs := newQuestDiagnosticTestSession(enabled)
			data := make([]byte, killLogHeaderSize+killLogMonsterCount)
			binary.LittleEndian.PutUint16(data[questIDOffset:questIDOffset+2], 54605)
			binary.LittleEndian.PutUint32(data[questElapsedFramesOffset:questElapsedFramesOffset+4], 2610)

			handleMsgSysRecordLog(session, &mhfpacket.MsgSysRecordLog{
				AckHandle: 77,
				Unk0:      11,
				Unk1:      22,
				Data:      data,
			})

			entries := logs.FilterMessage("QuestRecordLogDiagnostic").All()
			wantEntries := 0
			if enabled {
				wantEntries = 1
			}
			if len(entries) != wantEntries {
				t.Fatalf("diagnostic log entries = %d, want %d", len(entries), wantEntries)
			}
			if enabled {
				fields := entries[0].ContextMap()
				requireQuestDiagnosticField(t, fields, "charID", "42")
				requireQuestDiagnosticField(t, fields, "name", "DiagnosticHunter")
				requireQuestDiagnosticField(t, fields, "ackHandle", "77")
				requireQuestDiagnosticField(t, fields, "unk0", "11")
				requireQuestDiagnosticField(t, fields, "unk1", "22")
				requireQuestDiagnosticField(t, fields, "dataBytes", fmt.Sprint(len(data)))
				requireQuestDiagnosticField(t, fields, "dataHex", hex.EncodeToString(data))
				requireQuestDiagnosticField(t, fields, "questID", "54605")
				requireQuestDiagnosticField(t, fields, "elapsedFrames", "2610")
			}
		})
	}
}

func TestQuestTerminalLogDiagnosticFollowsQuestTools(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			_, session, logs := newQuestDiagnosticTestSession(enabled)
			terminalEntries := make([]mhfpacket.TerminalLogEntry, 20)
			for i := range terminalEntries {
				terminalEntries[i] = mhfpacket.TerminalLogEntry{
					Index: uint32(i + 1),
					Type1: uint8(i),
					Unk1:  int32(i * 10),
				}
			}

			handleMsgSysTerminalLog(session, &mhfpacket.MsgSysTerminalLog{
				AckHandle: 88,
				LogID:     9,
				Entries:   terminalEntries,
			})

			entries := logs.FilterMessage("QuestTerminalLogDiagnostic").All()
			wantEntries := 0
			if enabled {
				wantEntries = 1
			}
			if len(entries) != wantEntries {
				t.Fatalf("diagnostic log entries = %d, want %d", len(entries), wantEntries)
			}
			if enabled {
				fields := entries[0].ContextMap()
				requireQuestDiagnosticField(t, fields, "charID", "42")
				requireQuestDiagnosticField(t, fields, "name", "DiagnosticHunter")
				requireQuestDiagnosticField(t, fields, "ackHandle", "88")
				requireQuestDiagnosticField(t, fields, "logID", "9")
				requireQuestDiagnosticField(t, fields, "entryCount", "20")
				loggedTerminalEntries, ok := fields["entries"].([]mhfpacket.TerminalLogEntry)
				if !ok {
					t.Fatalf("entries field type = %T, want []mhfpacket.TerminalLogEntry", fields["entries"])
				}
				if len(loggedTerminalEntries) != len(terminalEntries) {
					t.Fatalf("logged terminal entries = %d, want %d", len(loggedTerminalEntries), len(terminalEntries))
				}
			}
		})
	}
}
