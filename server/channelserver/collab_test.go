package channelserver

import (
	"testing"

	cfg "erupe-ce/config"
)

func TestExplicitCollabEventOverridesLegacyFlags(t *testing.T) {
	s := &Server{
		collabEvent: collabHiganjima,
		erupeConfig: &cfg.Config{GameplayOptions: cfg.GameplayOptions{
			EnableKaijiEvent: true,
			EnableHiganjimaEvent: true,
			EnableNierEvent: true,
		}},
	}

	if s.allowsCollabQuest(collabKaiji) {
		t.Error("Kaiji quest should be hidden by explicit Higanjima world")
	}
	if !s.allowsCollabQuest(collabHiganjima) {
		t.Error("Higanjima quest should be visible in its explicit world")
	}
	if s.allowsCollabQuest(collabNier) {
		t.Error("NieR quest should be hidden by explicit Higanjima world")
	}
	if !s.allowsCollabQuest("") {
		t.Error("Unscoped event quest should remain visible")
	}
}

func TestNoneCollabEventHidesScopedQuestsAndTuneValues(t *testing.T) {
	s := &Server{
		collabEvent: collabNone,
		erupeConfig: &cfg.Config{GameplayOptions: cfg.GameplayOptions{
			EnableKaijiEvent: true,
			EnableHiganjimaEvent: true,
			EnableNierEvent: true,
		}},
	}

	if s.allowsCollabQuest(collabKaiji) || s.allowsCollabQuest(collabHiganjima) || s.allowsCollabQuest(collabNier) {
		t.Error("none world should hide every scoped collaboration quest")
	}
	if got := s.appendCollabTuneValues(nil); len(got) != 0 {
		t.Errorf("none world produced %d collaboration tune values, want 0", len(got))
	}
}

func TestLegacyCollabFlagsRemainSupported(t *testing.T) {
	s := &Server{
		erupeConfig: &cfg.Config{GameplayOptions: cfg.GameplayOptions{
			EnableKaijiEvent: true,
			EnableNierEvent:  true,
		}},
	}

	if !s.allowsCollabQuest(collabKaiji) || s.allowsCollabQuest(collabHiganjima) || !s.allowsCollabQuest(collabNier) {
		t.Error("legacy flags should determine visibility when no per-world mode is configured")
	}
	got := s.appendCollabTuneValues(nil)
	if len(got) != 2 || got[0].ID != 1106 || got[1].ID != 1153 {
		t.Errorf("legacy tune values = %#v, want Kaiji and NieR", got)
	}
}
