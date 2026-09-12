package channelserver

import (
	"fmt"
	"testing"

	cfg "erupe-ce/config"
)

func TestCollabQuestDeliveryByWorldType(t *testing.T) {
	worlds := []struct {
		typ  uint8
		want bool
	}{{0, false}, {1, true}, {2, false}, {3, true}, {4, false}, {5, true}, {6, false}, {255, false}}
	for _, world := range worlds {
		for _, mode := range []string{collabKaiji, collabHiganjima, collabNier, collabRandom, ""} {
			for _, selected := range collabEvents {
				t.Run(fmt.Sprintf("world%d/%s/%s", world.typ, mode, selected), func(t *testing.T) {
					s := &Session{
						server: &Server{
							worldType: world.typ, collabEvent: mode,
							erupeConfig: &cfg.Config{GameplayOptions: cfg.GameplayOptions{
								EnableKaijiEvent: true, EnableHiganjimaEvent: true, EnableNierEvent: true,
							}},
						},
						collabEvent: selected,
					}
					isActive := func(event string) bool {
						return mode == "" || mode == event || (mode == collabRandom && selected == event)
					}
					wantCount := 0
					for _, quest := range builtInCollabQuests {
						want := world.want && isActive(quest.event)
						if want {
							wantCount++
						}
						// Both explicitly scoped rows and unscoped DB overrides of
						// known quest IDs must follow the same delivery restriction.
						for _, scope := range []string{quest.event, ""} {
							if got := s.allowsCollabQuest(EventQuest{QuestID: quest.questID, CollabScope: scope}); got != want {
								t.Errorf("quest %d scope %q = %t, want %t", quest.questID, scope, got, want)
							}
						}
					}
					ordinary := EventQuest{ID: 10, QuestID: 50000}
					if !s.allowsCollabQuest(ordinary) {
						t.Fatal("ordinary event quest was blocked")
					}
					if got := s.appendBuiltInCollabQuests([]EventQuest{ordinary}); len(got) != 1+wantCount {
						t.Errorf("list count = %d, want %d", len(got), 1+wantCount)
					}
					if got := s.allowsCollabQuest(EventQuest{QuestID: 59999, CollabScope: collabHiganjima}); got != (world.want && isActive(collabHiganjima)) {
						t.Error("additional scoped DB quest did not follow world restriction")
					}
					// This change restricts quest delivery, not NPC tune flags.
					wantTunes := 1
					if mode == "" {
						wantTunes = 3
					}
					if got := s.appendCollabTuneValues(nil); len(got) != wantTunes {
						t.Errorf("NPC tune count = %d, want %d", len(got), wantTunes)
					}
				})
			}
		}
	}
}

func TestUnscopedBuiltInCollabQuestRequiresActiveEvent(t *testing.T) {
	for _, mode := range []string{collabNone, collabRandom} {
		s := &Session{server: &Server{worldType: 1, collabEvent: mode}}
		for _, quest := range builtInCollabQuests {
			if s.allowsCollabQuest(EventQuest{QuestID: quest.questID}) {
				t.Errorf("mode %q exposed inactive quest %d", mode, quest.questID)
			}
		}
	}
}

func TestExplicitCollabEventOverridesLegacyFlags(t *testing.T) {
	s := &Session{server: &Server{worldType: 1,
		collabEvent: collabHiganjima,
		erupeConfig: &cfg.Config{GameplayOptions: cfg.GameplayOptions{
			EnableKaijiEvent:     true,
			EnableHiganjimaEvent: true,
			EnableNierEvent:      true,
		}},
	}}

	if s.allowsCollabQuest(EventQuest{CollabScope: collabKaiji}) {
		t.Error("Kaiji quest should be hidden by explicit Higanjima world")
	}
	if !s.allowsCollabQuest(EventQuest{CollabScope: collabHiganjima}) {
		t.Error("Higanjima quest should be visible in its explicit world")
	}
	if s.allowsCollabQuest(EventQuest{CollabScope: collabNier}) {
		t.Error("NieR quest should be hidden by explicit Higanjima world")
	}
	if !s.allowsCollabQuest(EventQuest{CollabScope: ""}) {
		t.Error("Unscoped event quest should remain visible")
	}
}

func TestNoneCollabEventHidesScopedQuestsAndTuneValues(t *testing.T) {
	s := &Session{server: &Server{worldType: 1,
		collabEvent: collabNone,
		erupeConfig: &cfg.Config{GameplayOptions: cfg.GameplayOptions{
			EnableKaijiEvent:     true,
			EnableHiganjimaEvent: true,
			EnableNierEvent:      true,
		}},
	}}

	if s.allowsCollabQuest(EventQuest{CollabScope: collabKaiji}) || s.allowsCollabQuest(EventQuest{CollabScope: collabHiganjima}) || s.allowsCollabQuest(EventQuest{CollabScope: collabNier}) {
		t.Error("none world should hide every scoped collaboration quest")
	}
	if got := s.appendCollabTuneValues(nil); len(got) != 0 {
		t.Errorf("none world produced %d collaboration tune values, want 0", len(got))
	}
}

func TestLegacyCollabFlagsRemainSupported(t *testing.T) {
	s := &Session{server: &Server{worldType: 1,
		erupeConfig: &cfg.Config{GameplayOptions: cfg.GameplayOptions{
			EnableKaijiEvent: true,
			EnableNierEvent:  true,
		}},
	}}

	if !s.allowsCollabQuest(EventQuest{CollabScope: collabKaiji}) || s.allowsCollabQuest(EventQuest{CollabScope: collabHiganjima}) || !s.allowsCollabQuest(EventQuest{CollabScope: collabNier}) {
		t.Error("legacy flags should determine visibility when no per-world mode is configured")
	}
	got := s.appendCollabTuneValues(nil)
	if len(got) != 2 || got[0].ID != 1106 || got[1].ID != 1153 {
		t.Errorf("legacy tune values = %#v, want Kaiji and NieR", got)
	}
}

func TestRandomHiganjimaAddsBuiltInQuestWithoutDatabaseRow(t *testing.T) {
	s := &Session{
		server:      &Server{worldType: 1, collabEvent: collabRandom},
		collabEvent: collabHiganjima,
	}
	existing := []EventQuest{{ID: 10, QuestID: 50000}}

	quests := s.appendBuiltInCollabQuests(existing)
	if len(quests) != 2 {
		t.Fatalf("built-in quest count = %d, want 2", len(quests))
	}
	got := quests[0]
	if got.ID != 11 || got.QuestID != 40217 || got.MaxPlayers != 4 || got.QuestType != 18 || got.Mark != 1 || got.Flags != -1 || got.CollabScope != collabHiganjima {
		t.Fatalf("built-in Higanjima quest = %#v", got)
	}
}

func TestBuiltInCollabQuestsFollowActiveNPC(t *testing.T) {
	tests := []struct {
		event      string
		maxPlayers uint8
		want       []int
	}{
		{event: collabKaiji, maxPlayers: 1, want: []int{40215}},
		{event: collabHiganjima, maxPlayers: 4, want: []int{40217}},
		{event: collabNier, maxPlayers: 4, want: []int{40221, 40223, 40224, 40225, 40226, 40227}},
		{event: collabNone},
	}

	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			s := &Session{server: &Server{worldType: 1, collabEvent: tt.event}}
			quests := s.appendBuiltInCollabQuests(nil)
			if len(quests) != len(tt.want) {
				t.Fatalf("quest count = %d, want %d", len(quests), len(tt.want))
			}
			for i, wantID := range tt.want {
				got := quests[i]
				if got.QuestID != wantID || got.MaxPlayers != tt.maxPlayers || got.QuestType != 18 || got.Mark != 1 || got.Flags != -1 || got.CollabScope != tt.event {
					t.Fatalf("quest[%d] = %#v, want ID %d scoped to %q", i, got, wantID, tt.event)
				}
			}
		})
	}
}

func TestDatabaseCollabQuestOverridesBuiltInQuest(t *testing.T) {
	s := &Session{server: &Server{worldType: 1, collabEvent: collabHiganjima}}
	existing := []EventQuest{{
		ID:           77,
		MaxPlayers:   2,
		QuestType:    18,
		QuestID:      40217,
		Mark:         9,
		CollabScope:  collabHiganjima,
		ActiveDays:   7,
		InactiveDays: 3,
	}}

	quests := s.appendBuiltInCollabQuests(existing)
	if len(quests) != 1 || quests[0].ID != 77 || quests[0].MaxPlayers != 2 || quests[0].Mark != 9 {
		t.Fatalf("database quest was not preserved: %#v", quests)
	}
}

func TestRandomCollabRotationKeepsEventUntilLastSessionLeaves(t *testing.T) {
	choices := []string{collabKaiji, collabNier}
	next := 0
	rotation := newCollabRotation(func() string {
		choice := choices[next]
		next++
		return choice
	})

	first, started := rotation.acquire()
	if first != collabKaiji || !started {
		t.Fatalf("first acquire = (%q, %t), want (%q, true)", first, started, collabKaiji)
	}
	second, started := rotation.acquire()
	if second != collabKaiji || started {
		t.Fatalf("second acquire = (%q, %t), want (%q, false)", second, started, collabKaiji)
	}

	if _, ended := rotation.release(); ended {
		t.Fatal("rotation ended while one session was still active")
	}
	if event, sessions := rotation.snapshot(); event != collabKaiji || sessions != 1 {
		t.Fatalf("rotation after first release = (%q, %d), want (%q, 1)", event, sessions, collabKaiji)
	}
	if event, ended := rotation.release(); event != collabKaiji || !ended {
		t.Fatalf("last release = (%q, %t), want (%q, true)", event, ended, collabKaiji)
	}
	if event, sessions := rotation.snapshot(); event != "" || sessions != 0 {
		t.Fatalf("empty rotation = (%q, %d), want (empty, 0)", event, sessions)
	}

	nextEvent, started := rotation.acquire()
	if nextEvent != collabNier || !started {
		t.Fatalf("next 0 -> 1 acquire = (%q, %t), want (%q, true)", nextEvent, started, collabNier)
	}
}

func TestRandomCollabRotationIsSharedAcrossWorldChannels(t *testing.T) {
	rotation := newCollabRotation(func() string { return collabHiganjima })
	first := &Session{server: &Server{worldType: 1, collabEvent: collabRandom, collabRotation: rotation}}
	second := &Session{server: &Server{worldType: 1, collabEvent: collabRandom, collabRotation: rotation}}

	first.acquireCollabEvent()
	second.acquireCollabEvent()
	if first.effectiveCollabEvent() != collabHiganjima || second.effectiveCollabEvent() != collabHiganjima {
		t.Fatalf("sessions received different events: first=%q second=%q", first.effectiveCollabEvent(), second.effectiveCollabEvent())
	}
	if !first.allowsCollabQuest(EventQuest{CollabScope: collabHiganjima}) || first.allowsCollabQuest(EventQuest{CollabScope: collabKaiji}) {
		t.Fatal("random session quest filtering did not match the selected event")
	}

	first.releaseCollabEvent()
	if event, sessions := rotation.snapshot(); event != collabHiganjima || sessions != 1 {
		t.Fatalf("shared rotation ended too early: event=%q sessions=%d", event, sessions)
	}
	second.releaseCollabEvent()
	if event, sessions := rotation.snapshot(); event != "" || sessions != 0 {
		t.Fatalf("shared rotation did not reset: event=%q sessions=%d", event, sessions)
	}
}
