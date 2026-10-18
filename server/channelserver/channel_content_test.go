package channelserver

import (
	"encoding/binary"
	"fmt"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

func TestChannelCollabOverridesKeepFixedChannelsOutOfRotation(t *testing.T) {
	choices := 0
	rotation := newCollabRotation(func() string {
		choices++
		if choices == 1 {
			return collabKaiji
		}
		return collabNier
	})
	world := cfg.EntranceServerInfo{Type: 1, CollabEvent: collabRandom}
	config := &cfg.Config{GameplayOptions: cfg.GameplayOptions{
		EnableKaijiEvent: true, EnableHiganjimaEvent: true, EnableNierEvent: true,
		EnableEvangelionEvent: true, EnablePSO2Event: true,
	}}
	for _, event := range append([]string{collabNone}, collabEvents...) {
		channel := cfg.EntranceChannelInfo{CollabEvent: event}
		server := newChannelContentTestServer(&Config{Logger: zap.NewNop(), ErupeConfig: config,
			WorldType: world.Type, CollabEvent: channel.EffectiveCollabEvent(world.CollabEvent), CollabRotation: rotation})
		s := &Session{server: server}
		s.acquireCollabEvent()
		if s.collabRotationAcquired || choices != 0 {
			t.Fatalf("fixed channel %q acquired the shared rotation", event)
		}
		tunes := s.appendCollabTuneValues(nil)
		quests := s.appendBuiltInCollabQuests(nil)
		if event == collabNone {
			if len(tunes) != 0 || len(quests) != 0 {
				t.Fatal("normal channel exposed collaboration content despite the world's random setting and global flags")
			}
		} else {
			if len(tunes) != 1 || len(quests) == 0 {
				t.Fatalf("fixed event %q has tunes=%v quests=%v", event, tunes, quests)
			}
			for _, quest := range quests {
				if quest.CollabScope != event || !s.allowsCollabQuest(quest) {
					t.Fatalf("fixed channel %q exposed wrong quest: %#v", event, quest)
				}
			}
		}
		s.releaseCollabEvent()
	}

	newRandomSession := func(override string) *Session {
		channel := cfg.EntranceChannelInfo{CollabEvent: override}
		return &Session{server: newChannelContentTestServer(&Config{Logger: zap.NewNop(), ErupeConfig: config,
			WorldType: world.Type, CollabEvent: channel.EffectiveCollabEvent(world.CollabEvent), CollabRotation: rotation})}
	}
	first, second := newRandomSession(""), newRandomSession(collabRandom)
	first.acquireCollabEvent()
	second.acquireCollabEvent()
	if event, sessions := rotation.snapshot(); event != collabKaiji || sessions != 2 || choices != 1 {
		t.Fatalf("random channels did not share occupancy: event=%q sessions=%d choices=%d", event, sessions, choices)
	}
	first.releaseCollabEvent()
	second.releaseCollabEvent()
	first.acquireCollabEvent()
	defer first.releaseCollabEvent()
	if first.effectiveCollabEvent() != collabNier || choices != 2 {
		t.Fatal("fixed channels prevented a new draw after random channels emptied")
	}
}

func TestChannelMezFesFixedSelectionOverridesRandomAndLauncherDefaults(t *testing.T) {
	for _, game := range []uint16{2, 4} {
		t.Run(fmt.Sprint(game), func(t *testing.T) {
			rotation := &MezFesRotation{choose: func() uint16 { t.Fatal("fixed channel rerolled"); return 4 }}
			config := &cfg.Config{GameplayOptions: cfg.GameplayOptions{MezFesRandomMinigame: true, MezFesSwitchMinigame: game == 4}}
			server := newChannelContentTestServer(&Config{Logger: zap.NewNop(), ErupeConfig: config,
				WorldType: 6, MezFesMinigame: game, MezFesRotation: rotation})
			if server.mezFesRotation != nil {
				t.Fatal("fixed channel retained the random occupancy counter")
			}
			s := &Session{server: server}
			s.acquireMezFesMinigame()
			if s.mezFesRotationAcquired || s.effectiveMezFesMinigame() != game {
				t.Fatal("fixed game did not override random/global settings")
			}
			s.releaseMezFesMinigame()
			s.acquireMezFesMinigame()
			if s.effectiveMezFesMinigame() != game || rotation.sessions != 0 {
				t.Fatal("fixed game changed after logout/login")
			}
		})
	}
}

func TestChannelMezFesFixedAndInheritedTuneWire(t *testing.T) {
	// All rows are measured against the same unmodified ordinary tune set.
	baseline := newEnumerateQuestSession(0)
	baseTunes := enumerateChannelContentTunes(t, baseline)
	for _, tt := range []struct {
		name      string
		world     uint8
		fixed     uint16
		switchOld bool
		mode      cfg.Mode
		want      uint16
	}{
		{"fixed-tokotoko", 6, 2, false, cfg.ZZ, 2},
		{"fixed-volpakkun", 6, 4, true, cfg.ZZ, 4},
		{"fixed-z2", 6, 2, false, cfg.Z2, 2},
		{"inherited-mezfes", 6, 0, false, cfg.ZZ, 4},
		{"leave-to-open", 1, 0, false, cfg.ZZ, 4},
		{"leave-to-open-with-old-switch", 1, 0, true, cfg.ZZ, 2},
		{"older-client-no-signal", 1, 0, false, cfg.Z1, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newEnumerateQuestSession(0)
			s.server.worldType, s.server.mezFesMinigame = tt.world, tt.fixed
			s.server.erupeConfig.RealClientMode = tt.mode
			s.server.erupeConfig.GameplayOptions.MezFesSwitchMinigame = tt.switchOld
			// A fixed channel anywhere enables the reset/fallback signal globally,
			// even when GameplayOptions.MezFesRandomMinigame remains false.
			s.server.erupeConfig.Entrance.Entries = []cfg.EntranceServerInfo{{Type: 6, Channels: []cfg.EntranceChannelInfo{{MezFesMinigame: 2}, {MezFesMinigame: 4}}}}
			tunes := enumerateChannelContentTunes(t, s)
			if got := tunes[mezFesMinigameTuneID]; got != tt.want {
				t.Fatalf("game on wire = %d, want %d", got, tt.want)
			}
			if tt.mode == cfg.ZZ {
				delete(tunes, mezFesMinigameTuneID)
				if len(tunes) != len(baseTunes) {
					t.Fatal("custom signal truncated an ordinary tune")
				}
				for id, value := range baseTunes {
					if got, ok := tunes[id]; !ok || got != value {
						t.Fatalf("ordinary tune %d changed: %d -> %d", id, value, got)
					}
				}
			}
		})
	}
}

func enumerateChannelContentTunes(t *testing.T, s *Session) map[uint16]uint16 {
	t.Helper()
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 1})
	payload := readAck(t, s).Payload
	if binary.BigEndian.Uint16(payload) != 0 {
		t.Fatal("expected empty quest list before tunes")
	}
	key := binary.BigEndian.Uint16(payload[2:])
	count := int(binary.BigEndian.Uint16(payload[4:]))
	result := make(map[uint16]uint16, count)
	for i := 0; i < count; i++ {
		row := payload[6+i*10 : 6+(i+1)*10]
		id, value := binary.BigEndian.Uint16(row)^key, binary.BigEndian.Uint16(row[8:])^key
		if _, exists := result[id]; exists {
			t.Fatalf("duplicate tune ID %d", id)
		}
		result[id] = value
	}
	return result
}

func newChannelContentTestServer(config *Config) *Server {
	s := &Server{erupeConfig: config.ErupeConfig, logger: config.Logger}
	s.configureChannelContent(config)
	return s
}
