package channelserver

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	"erupe-ce/common/mhfmon"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func settingsRecordFixture() []byte {
	data := make([]byte, nativeQuestRecordBytes+16)
	binary.LittleEndian.PutUint16(data, 50001)
	data[8] = 4
	binary.LittleEndian.PutUint32(data[12:], 8051)
	data[32+6] = 1
	tail := data[nativeQuestRecordBytes:]
	copy(tail, "VUI2")
	tail[4], tail[5], tail[6], tail[7], tail[15] = 15, 2, 60, 2, 9
	copy(tail[8:12], data[12:16])
	copy(tail[12:14], data[:2])
	tail[14] = data[8]
	return data
}

// mainTargetRecordFixture retains every native byte except setting its clear
// outcome, then appends the same evidence produced by the new client.
func mainTargetRecordFixture(native []byte, mode, state byte) []byte {
	data := make([]byte, nativeQuestRecordBytes+16)
	copy(data, native)
	data[questResultCodeOffset] = questResultCodeCleared
	tail := data[nativeQuestRecordBytes:]
	copy(tail, "VUI2")
	tail[6], tail[7], tail[15] = 45, mode, state
	copy(tail[8:12], data[12:16])
	copy(tail[12:14], data[:2])
	tail[14] = data[8]
	return data
}

func TestHuntClientSettingsNativeResult(t *testing.T) {
	data := settingsRecordFixture()
	original := append([]byte(nil), data...)
	got := decodeHuntClientSettings(data)
	if got == nil || got.Version != 1 || !got.HPView || !got.HPViewUsed || got.ElapsedMode != 2 || !got.ElapsedUsed || got.FOV != 60 || !got.FOVUsed {
		t.Fatalf("settings = %#v", got)
	}
	if !bytes.Equal(data, original) || binary.LittleEndian.Uint32(data[12:16]) != 8051 {
		t.Fatal("native result modified")
	}
	for mode := byte(0); mode <= 2; mode++ {
		data[nativeQuestRecordBytes+5] = mode
		got = decodeHuntClientSettings(data)
		if got == nil || got.ElapsedMode != mode {
			t.Fatalf("mode %d lost", mode)
		}
	}
	if decodeHuntClientSettings(data[:nativeQuestRecordBytes]) != nil {
		t.Fatal("old client must remain unknown")
	}
}

func TestHuntClientSettingsRejectsUnboundOrInvalidTrailer(t *testing.T) {
	for _, offset := range []int{nativeQuestRecordBytes, nativeQuestRecordBytes + 7, nativeQuestRecordBytes + 8, nativeQuestRecordBytes + 12, nativeQuestRecordBytes + 14, nativeQuestRecordBytes + 15} {
		data := settingsRecordFixture()
		data[offset] ^= 1
		if decodeHuntClientSettings(data) != nil {
			t.Fatalf("accepted invalid offset %d", offset)
		}
	}
	for _, value := range []struct {
		offset int
		value  byte
	}{{4, 16}, {5, 3}, {6, 29}, {6, 91}} {
		data := settingsRecordFixture()
		data[nativeQuestRecordBytes+value.offset] = value.value
		if decodeHuntClientSettings(data) != nil {
			t.Fatalf("accepted invalid %#v", value)
		}
	}
	for _, monster := range []int{93, 149} {
		data := settingsRecordFixture()
		data[32+monster] = 1
		if decodeHuntClientSettings(data) != nil {
			t.Fatalf("Raviente %d changed", monster)
		}
	}
	for _, n := range []int{0, 15, 31, 1195, 1197, 1211, 1213} {
		data := make([]byte, n)
		if decodeHuntClientSettings(data) != nil {
			t.Fatalf("accepted length %d", n)
		}
	}
}

func TestHuntRecordSettingsStayWithFasterTime(t *testing.T) {
	db := SetupTestDB(t)
	defer TeardownTestDB(t, db)
	user := CreateTestUser(t, db, "hunt_ui_user")
	char := CreateTestCharacter(t, db, user, "HuntUIHunter")
	repo := NewHuntRecordRepository(db)
	best := HuntRecordUpsert{CharacterID: char, MonsterID: 6, QuestID: 50001, RankKind: "g", VariantKind: "normal", BestTimeFrames: 8051, RecordedAt: time.Now(), ClientSettings: decodeHuntClientSettings(settingsRecordFixture())}
	if err := repo.UpsertPersonalBest(best); err != nil {
		t.Fatal(err)
	}
	slower := best
	slower.BestTimeFrames = 9000
	slower.ClientSettings = &HuntClientSettings{Version: 1, FOV: 45}
	if err := repo.UpsertPersonalBest(slower); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := db.Get(&raw, "SELECT client_settings FROM monster_hunt_records WHERE character_id=$1", char); err != nil {
		t.Fatal(err)
	}
	var saved HuntClientSettings
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ElapsedMode != 2 || saved.FOV != 60 {
		t.Fatalf("slower metadata overwrote best: %#v", saved)
	}
	faster := slower
	faster.BestTimeFrames = 7800
	if err := repo.UpsertPersonalBest(faster); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&raw, "SELECT client_settings FROM monster_hunt_records WHERE character_id=$1", char); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.FOV != 45 || saved.ElapsedMode != 0 {
		t.Fatalf("faster metadata missing: %#v", saved)
	}
}

func TestHuntMainTargetEvidence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode, state byte
		want        bool
	}{
		{"alternative incomplete", 1, 7, false}, {"alternative achieved", 1, 7 | 16, true},
		{"main only", 2, 1 | 8, true}, {"main subs incomplete", 2, 7 | 8, true},
		{"main and sub", 2, 7 | 24, true}, {"all", 2, 63, true},
		{"sub only", 2, 7 | 16, false}, {"both subs only", 2, 7 | 48, false},
		{"two mains", 3, 7 | 24, true}, {"one main plus sub", 3, 7 | 40, false},
		{"all active only", 4, 3 | 24, true}, {"all active incomplete", 4, 7 | 24, false},
		{"invalid mode", 0, 63, false}, {"invalid high bits", 2, 0xc9, false},
		{"inactive achieved", 2, 8, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := settingsRecordFixture()
			data[nativeQuestRecordBytes+7], data[nativeQuestRecordBytes+15] = tc.mode, tc.state
			if got := huntRecordMainTargetCleared(data); got != tc.want {
				t.Fatalf("main clear=%t, want %t", got, tc.want)
			}
		})
	}
	for _, result := range []byte{0, 3, 5, 6, 7} {
		data := settingsRecordFixture()
		data[8] = result
		data[nativeQuestRecordBytes+14] = result
		if huntRecordMainTargetCleared(data) {
			t.Fatalf("result %d accepted", result)
		}
	}
	legacy := settingsRecordFixture()
	copy(legacy[nativeQuestRecordBytes:], "VUI1")
	legacy[nativeQuestRecordBytes+7], legacy[nativeQuestRecordBytes+15] = 0, 0
	if decodeHuntClientSettings(legacy) == nil || huntRecordMainTargetCleared(legacy) {
		t.Fatal("legacy metadata is readable but not main-clear evidence")
	}
	if huntRecordMainTargetCleared(legacy[:nativeQuestRecordBytes]) {
		t.Fatal("native result alone is not main evidence")
	}
}

func TestQuestRunModeWithClientTrailer(t *testing.T) {
	for _, version := range []string{"VUI1", "VUI2"} {
		for _, hc := range []byte{0, 1} {
			data := settingsRecordFixture()
			copy(data[nativeQuestRecordBytes:], version)
			if version == "VUI1" {
				data[nativeQuestRecordBytes+7], data[nativeQuestRecordBytes+15] = 0, 0
			}
			data[questRunRecordHardcoreModeOffset] = hc
			want := questRunModeNormal
			if hc == 1 {
				want = questRunModeHardcore
			}
			if got := decodeQuestRunModeFromRecordLog(data); got != want {
				t.Fatalf("%s HC=%d: got %s", version, hc, got)
			}
			data[nativeQuestRecordBytes+12] ^= 1
			if decodeQuestRunModeFromRecordLog(data) != questRunModeUnknown {
				t.Fatal("invalid trailer accepted for HC")
			}
		}
	}
}

func TestMainTargetTimeRankingEligibility(t *testing.T) {
	for _, tc := range []struct {
		name                string
		result, mode, state byte
		legacy              bool
		monster             int
		want                int
	}{
		{"main no subs", 4, 2, 7 | 8, false, 6, 1},
		{"main and subs", 4, 2, 63, false, 6, 1},
		{"sub only clear code", 4, 2, 7 | 16, false, 6, 0},
		{"both subs only", 4, 2, 7 | 48, false, 6, 0},
		{"one of two mains", 4, 3, 7 | 40, false, 6, 0},
		{"two mains no sub", 4, 3, 7 | 24, false, 6, 1},
		{"fainted", 6, 2, 63, false, 6, 0},
		{"retired", 7, 2, 63, false, 6, 0},
		{"unfinished success", 3, 2, 63, false, 6, 0},
		{"legacy clear unknown main", 4, 2, 63, true, 6, 0},
		{"normal Raviente legacy", 4, 0, 0, true, mhfmon.Raviente, 1},
		{"normal Raviente prior failure rule", 6, 0, 0, true, mhfmon.Raviente, 1},
		{"Berserk prior phase exclusion", 4, 0, 0, true, mhfmon.BerserkRaviente, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.RealClientMode = cfg.ZZ
			server.guildRepo = &mockGuildRepo{}
			repo := &mockHuntRecordRepo{}
			server.huntRecordRepo = repo
			session := createMockSession(1, server)
			session.stage = NewStage("sl1Ns200p0a0u1")
			data := make([]byte, nativeQuestRecordBytes)
			binary.LittleEndian.PutUint16(data, 12345)
			binary.LittleEndian.PutUint32(data[12:], 8051)
			data[32+tc.monster] = 1
			if !tc.legacy {
				data = mainTargetRecordFixture(data, tc.mode, tc.state)
			}
			data[8] = tc.result
			if !tc.legacy {
				data[nativeQuestRecordBytes+14] = tc.result
			}
			handleMsgSysRecordLog(session, &mhfpacket.MsgSysRecordLog{AckHandle: 100, Data: data})
			if len(repo.records) != tc.want {
				t.Fatalf("time records=%d, want %d", len(repo.records), tc.want)
			}
			if tc.want == 1 && repo.records[0].BestTimeFrames != 8051 {
				t.Fatal("native final frames changed")
			}
		})
	}
}
