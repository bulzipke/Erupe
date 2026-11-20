package channelserver

import (
	"bytes"
	"encoding/binary"
	"maps"
	"os"
	"path/filepath"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestCollabOnlyEventQuestsNativeListAndClientCache(t *testing.T) {
	rows := []EventQuest{
		{ID: 1, QuestID: 50118, QuestType: 18, Flags: -1},
		{ID: 2, QuestID: 50128, QuestType: 28, Flags: -1},
		{ID: 3, QuestID: 50154, QuestType: 54, Flags: -1},
		{ID: 4, QuestID: 50124, QuestType: 24, Flags: -1},
		{ID: 5, QuestID: 50143, QuestType: 43, Flags: -1},
		{ID: 6, QuestID: 40215, QuestType: 18, Flags: -1}, // Built-in scope via DB override.
		{ID: 7, QuestID: 50102, QuestType: 28, Flags: -1, CollabScope: collabKaiji},
		{ID: 8, QuestID: 50103, QuestType: 18, Flags: -1, CollabScope: collabNier},
	}
	disabled := false
	for _, tc := range []struct {
		name              string
		dedicated         bool
		dedicatedChannels []cfg.EntranceChannelInfo
		wantCollab        bool
	}{
		{name: "no dedicated world", wantCollab: true},
		{name: "dedicated world", dedicated: true, dedicatedChannels: []cfg.EntranceChannelInfo{{}}, wantCollab: true},
		{name: "other world", dedicatedChannels: []cfg.EntranceChannelInfo{{}}},
		{name: "disabled dedicated channel", dedicatedChannels: []cfg.EntranceChannelInfo{{Enabled: &disabled}}, wantCollab: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := questListVisibilitySession(t, rows, `[{"quest_id":50118,"profiles":["normal"],"schedule":"always"}]`)
			config := s.server.erupeConfig
			config.QuestDeliveryCatalog = s.server.questDelivery
			if tc.dedicatedChannels != nil {
				config.Entrance.Entries = []cfg.EntranceServerInfo{
					{CollabOnlyEventQuests: true, Channels: tc.dedicatedChannels},
				}
			}
			s.server.configureChannelContent(&Config{
				ErupeConfig: config, WorldName: "Brave", WorldType: 1, ChannelNumber: 1,
				CollabEvent: collabKaiji, CollabOnlyEventQuests: tc.dedicated,
			})
			want := map[int]bool{50124: true, 50143: true}
			if tc.wantCollab {
				want[40215], want[50102] = true, true
			}
			if !tc.dedicated {
				want[50118], want[50128], want[50154] = true, true, true
			}

			questListVisibilityNativePage(t, s, 0)
			if got := questListVisibilityIDs(t, s.questDeliveryList); !maps.Equal(got, want) {
				t.Fatalf("native list IDs %v, want %v", got, want)
			}

			cache := questListVisibilityProbe(t, s)
			cacheIDs := make(map[int]bool, len(cache.bodies))
			for _, body := range cache.bodies {
				cacheIDs[int(binary.LittleEndian.Uint16(body[46:48]))] = true
			}
			if len(cache.bodies) != len(want) || !maps.Equal(cacheIDs, want) {
				t.Fatalf("client cache IDs %v, want %v", cacheIDs, want)
			}
		})
	}
}

func TestCollabOnlyEventQuestsDirectFileRequests(t *testing.T) {
	s := newEnumerateQuestSession(0)
	s.server.collabOnlyEvents, s.server.worldType, s.server.collabEvent = true, 1, collabKaiji
	s.userID = 77
	s.server.userRepo = returnWorldUserMock{allowed: true}
	if !s.canUseReturnWorldQuests() {
		t.Fatal("fixture must be eligible for Return quests")
	}
	s.server.erupeConfig.BinPath = t.TempDir()
	s.server.erupeConfig.GameplayOptions.SeasonOverride = true
	dir := filepath.Join(s.server.erupeConfig.BinPath, "quests")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	s.server.eventRepo = &mockEventRepo{eventQuests: []EventQuest{
		{QuestID: 50118, QuestType: 18},
		{QuestID: 50128, QuestType: 28},
		{QuestID: 50154, QuestType: 54},
		{QuestID: 50143, QuestType: 43},
		{QuestID: 50143, QuestType: 28}, // Same file also serves a dedicated reception.
		{QuestID: 50102, QuestType: 28, CollabScope: collabKaiji},
		{QuestID: 50103, QuestType: 18, CollabScope: collabNier},
	}}
	body := []byte{1, 2, 3}
	for _, tc := range []struct {
		file, request         string
		dedicated, otherWorld bool
	}{
		{"50118d0", "50118d0", false, true},
		{"50128d0", "50128x0", false, true}, // A nonstandard request resolves to the event file.
		{"50154d0", "50154d0", false, true},
		{"23352d0", "23352d0", false, true}, // Synthetic Return-world G event.
		{"50143d0", "50143d0", true, true},
		{"50102d0", "50102d0", true, false},
		{"50103d0", "50103d0", false, false},
		{"40215d0", "40215d0", true, false},
		{"40217d0", "40217d0", false, false},
		{"00001d0", "00001d0", true, true}, // No event table row for this normal quest.
	} {
		t.Run(tc.request, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, tc.file+".bin"), body, 0644); err != nil {
				t.Fatal(err)
			}
			for _, dedicated := range []bool{true, false} {
				s.server.collabOnlyEvents, s.server.excludeCollab = dedicated, !dedicated
				want := tc.otherWorld
				if dedicated {
					want = tc.dedicated
				}
				pkt := &mhfpacket.MsgSysGetFile{AckHandle: 42, Filename: tc.request}
				handleMsgSysGetFile(s, pkt)
				ack := licenseHandlerAck(t, s)
				if pkt.Filename != tc.file || (ack.ErrorCode == 0) != want || bytes.Equal(ack.AckData, body) != want {
					t.Errorf("dedicated=%t: resolved %q, error code %d, data %v, want allowed=%t", dedicated, pkt.Filename, ack.ErrorCode, ack.AckData, want)
				}
			}
		})
	}
}
