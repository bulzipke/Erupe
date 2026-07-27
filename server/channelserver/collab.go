package channelserver

const (
	collabNone      = "none"
	collabKaiji     = "kaiji"
	collabHiganjima = "higanjima"
	collabNier      = "nier"
)

var collabTuneValues = []struct {
	event  string
tuneID uint16
}{
	{event: collabKaiji, tuneID: 1106},
	{event: collabHiganjima, tuneID: 1144},
	{event: collabNier, tuneID: 1153},
}

// enabledCollabEvents returns the collaboration content enabled for this
// channel server. A per-world value takes precedence over the legacy global
// settings so incompatible Rasta Bar NPC layouts cannot be enabled together.
func (s *Server) enabledCollabEvents() map[string]bool {
	if s.collabEvent != "" {
		if s.collabEvent == collabNone {
			return map[string]bool{}
		}
		return map[string]bool{s.collabEvent: true}
	}

	options := s.erupeConfig.GameplayOptions
	return map[string]bool{
		collabKaiji:     options.EnableKaijiEvent,
		collabHiganjima: options.EnableHiganjimaEvent,
		collabNier:      options.EnableNierEvent,
	}
}

// allowsCollabQuest reports whether an event quest is visible in this world.
// Empty scopes are ordinary event quests and remain available everywhere.
func (s *Server) allowsCollabQuest(scope string) bool {
	if scope == "" {
		return true
	}
	return s.enabledCollabEvents()[scope]
}

func (s *Server) appendCollabTuneValues(values []tuneValue) []tuneValue {
	enabled := s.enabledCollabEvents()
	for _, value := range collabTuneValues {
		if enabled[value.event] {
			values = append(values, tuneValue{ID: value.tuneID, Value: 1})
		}
	}
	return values
}
