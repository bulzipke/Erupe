package channelserver

import (
	"bytes"
	"testing"
	"time"

	"erupe-ce/common/mhfmon"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func supportRecordFixture(used bool, monster int) []byte {
	data := settingsRecordFixture()
	tail := data[nativeQuestRecordBytes:]
	copy(tail, "VUI3")
	if used {
		tail[4] |= 16
	}
	data[32+6] = 0
	data[32+monster] = 1
	if monster == mhfmon.Raviente || monster == mhfmon.BerserkRaviente {
		tail[7], tail[15] = 0, 0
	}
	return data
}

func TestSupportRankEvidence(t *testing.T) {
	for _, monster := range []int{6, mhfmon.Raviente, mhfmon.BerserkRaviente} {
		for _, used := range []bool{false, true} {
			data := supportRecordFixture(used, monster)
			original := append([]byte(nil), data...)
			settings := decodeHuntClientSettings(data)
			if settings == nil || !settings.SupportKnown || settings.SupportUsed != used {
				t.Fatalf("monster %d used %v: settings %#v", monster, used, settings)
			}
			if huntRecordSupportAllowed(data) == used {
				t.Fatalf("monster %d used %v accepted incorrectly", monster, used)
			}
			if !bytes.Equal(data, original) {
				t.Fatal("support check changed native result")
			}
			for _, offset := range []int{8, 12, 0, nativeQuestRecordBytes + 6} {
				bad := append([]byte(nil), data...)
				bad[offset] ^= 0x80
				if huntRecordSupportAllowed(bad) {
					t.Fatalf("unbound/invalid result accepted at %d", offset)
				}
			}
		}
	}
	for flags := 0; flags < 256; flags++ {
		data := supportRecordFixture(false, 6)
		data[nativeQuestRecordBytes+4] = byte(flags)
		want := flags < 16
		if huntRecordSupportAllowed(data) != want {
			t.Fatalf("flags %02x: expected rank eligibility %v", flags, want)
		}
	}
	for _, version := range []string{"VUI1", "VUI2", "VUI4"} {
		data := settingsRecordFixture()
		copy(data[nativeQuestRecordBytes:], version)
		if version == "VUI1" {
			data[nativeQuestRecordBytes+7], data[nativeQuestRecordBytes+15] = 0, 0
		}
		if huntRecordSupportAllowed(data) || huntRecordSupportAllowed(data[:nativeQuestRecordBytes]) {
			t.Fatalf("unknown Support status treated as OFF: %s", version)
		}
	}
}

type supportKillLogRepo struct {
	mockGuildRepo
	kills int
}

func (m *supportKillLogRepo) InsertKillLog(_ uint32, _ int, count uint8, _ time.Time) error {
	m.kills += int(count)
	return nil
}

func TestSupportExcludesTimeButKeepsKills(t *testing.T) {
	for _, monster := range []int{6, mhfmon.Raviente, mhfmon.BerserkRaviente} {
		for _, used := range []bool{false, true} {
			server := createMockServer()
			server.erupeConfig.RealClientMode = cfg.ZZ
			kills := &supportKillLogRepo{}
			server.guildRepo = kills
			records := &mockHuntRecordRepo{}
			server.huntRecordRepo = records
			session := createMockSession(1, server)
			session.stage = NewStage("sl1Ns200p0a0u1")
			handleMsgSysRecordLog(session, &mhfpacket.MsgSysRecordLog{AckHandle: 100, Data: supportRecordFixture(used, monster)})
			want := 1
			if used || monster == mhfmon.BerserkRaviente {
				want = 0
			}
			if len(records.records) != want || kills.kills != 1 {
				t.Fatalf("monster %d used %v: ranked %d want %d, kills %d want 1", monster, used, len(records.records), want, kills.kills)
			}
		}
	}
}

func TestSupportRepositoryRejectsBeforeDatabase(t *testing.T) {
	repo := &HuntRecordRepository{} // Invalid records must never access its nil DB.
	for _, settings := range []*HuntClientSettings{nil, {Version: 1}, {Version: 1, SupportKnown: true, SupportUsed: true}} {
		if err := repo.UpsertPersonalBest(HuntRecordUpsert{ClientSettings: settings, BestTimeFrames: 1}); err != nil {
			t.Fatal(err)
		}
	}
}
