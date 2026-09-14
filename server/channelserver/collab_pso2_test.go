package channelserver

import (
	"testing"

	cfg "erupe-ce/config"
)

func TestPSO2LegacyFlagAndWorldOverride(t *testing.T) {
	for _, tt := range []struct {
		name string
		mode string
		flag bool
		want bool
	}{
		{"default-disabled", "", false, false},
		{"legacy-enabled", "", true, true},
		{"explicit-overrides-disabled-flag", collabPSO2, false, true},
		{"none-overrides-enabled-flag", collabNone, true, false},
		{"other-event-overrides-enabled-flag", collabEvangelion, true, false},
		{"unselected-random", collabRandom, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{server: &Server{
				worldType: 1, collabEvent: tt.mode,
				erupeConfig: &cfg.Config{GameplayOptions: cfg.GameplayOptions{EnablePSO2Event: tt.flag}},
			}}
			if got := s.allowsCollabQuest(EventQuest{QuestID: 40239}); got != tt.want {
				t.Errorf("PSO2 quest visible = %t, want %t", got, tt.want)
			}
			found := false
			for _, tune := range s.appendCollabTuneValues(nil) {
				if tune.ID == 1130 && tune.Value == 1 {
					found = true
				}
			}
			if found != tt.want {
				t.Errorf("PSO2 tune present = %t, want %t", found, tt.want)
			}
		})
	}
}

func TestPSO2RandomDeliveryAndDatabaseOverride(t *testing.T) {
	rotation := newCollabRotation(func() string { return collabPSO2 })
	s := &Session{server: &Server{worldType: 1, collabEvent: collabRandom, collabRotation: rotation}}
	s.acquireCollabEvent()
	defer s.releaseCollabEvent()
	quests := s.appendBuiltInCollabQuests(nil)
	if len(quests) != 1 || quests[0].QuestID != 40239 || quests[0].MaxPlayers != 4 || quests[0].QuestType != 18 || quests[0].Mark != 1 || quests[0].Flags != -1 || quests[0].CollabScope != collabPSO2 {
		t.Fatalf("unexpected built-in PSO2 quest: %#v", quests)
	}
	if got := s.appendCollabTuneValues(nil); len(got) != 1 || got[0].ID != 1130 || got[0].Value != 1 {
		t.Fatalf("PSO2 tunes = %#v, want only 1130=1", got)
	}
	for _, scope := range []string{"", collabPSO2} {
		override := EventQuest{ID: 77, QuestID: 40239, MaxPlayers: 2, Mark: 9, CollabScope: scope, ActiveDays: 7, InactiveDays: 3}
		got := s.appendBuiltInCollabQuests([]EventQuest{override})
		if len(got) != 1 || got[0] != override || !s.allowsCollabQuest(got[0]) {
			t.Fatalf("DB override was modified or duplicated: %#v", got)
		}
	}
	s.releaseCollabEvent()
	if got := s.appendCollabTuneValues(nil); len(got) != 0 {
		t.Fatalf("released random session retained tunes: %#v", got)
	}
}
