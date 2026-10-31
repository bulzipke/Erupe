package channelserver

import (
	"testing"

	"erupe-ce/common/mhfmon"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestHuntRankingRetainsSoloNPCEvidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		npc     bool
		party   bool
		want    int
	}{
		{"solo including Halk", "VUI4", false, false, 1},
		{"NPC used", "VUI4", true, false, 1},
		{"party even if member left", "VUI4", false, true, 0},
		{"party with NPC", "VUI4", true, true, 0},
		{"old client NPC unknown", "VUI3", false, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.RealClientMode = cfg.ZZ
			kills := &supportKillLogRepo{}
			server.guildRepo = kills
			records := &mockHuntRecordRepo{}
			server.huntRecordRepo = records
			session := createMockSession(1, server)
			session.stage = NewStage("sl1Ns200p0a0u1")
			session.questHadParty.Store(tc.party)
			data := supportRecordFixture(false, 6)
			copy(data[nativeQuestRecordBytes:], tc.version)
			if tc.npc {
				data[nativeQuestRecordBytes+4] |= 32
			}
			handleMsgSysRecordLog(session, &mhfpacket.MsgSysRecordLog{AckHandle: 100, Data: data})
			if len(records.records) != tc.want || kills.kills != 1 {
				t.Fatalf("ranked=%d want=%d, kills=%d want=1", len(records.records), tc.want, kills.kills)
			}
			if tc.want == 1 {
				settings := records.records[0].ClientSettings
				if !settings.Solo || settings.NPCKnown != (tc.version == "VUI4") || settings.NPCUsed != tc.npc {
					t.Fatalf("record lost its solo/NPC evidence: %+v", settings)
				}
			}
		})
	}
	for _, monster := range []int{mhfmon.Raviente, mhfmon.BerserkRaviente} {
		settings := decodeHuntClientSettings(supportRecordFixture(false, monster))
		if settings == nil || settings.NPCKnown || !settings.SupportKnown || settings.SupportUsed {
			t.Fatalf("Raviente's existing VUI3 settings changed: %#v", settings)
		}
	}
}

func TestNPCUsageMustBelongToValidatedResult(t *testing.T) {
	for flags := 0; flags < 256; flags++ {
		data := supportRecordFixture(false, 6)
		data[nativeQuestRecordBytes+4] = byte(flags)
		settings := decodeHuntClientSettings(data)
		if flags >= 64 {
			if settings != nil {
				t.Fatalf("invalid flags=%02x accepted", flags)
			}
		} else if settings == nil || !settings.NPCKnown || settings.NPCUsed != (flags&32 != 0) {
			t.Fatalf("NPC evidence flags=%02x settings=%+v", flags, settings)
		}
	}
	data := supportRecordFixture(false, 6)
	data[nativeQuestRecordBytes+12] ^= 1
	if decodeHuntClientSettings(data) != nil {
		t.Fatal("NPC evidence from another quest accepted")
	}
}
