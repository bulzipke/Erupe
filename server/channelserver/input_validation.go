package channelserver

import (
	"erupe-ce/common/stringsupport"
	"go.uber.org/zap"
)

// validateNameInput applies the server-authoritative policy for player-created
// names. Client NG-word tables remain a usability aid, not a trust boundary.
func (s *Session) validateNameInput(field, value string) bool {
	return s.validateNameInputForChar(field, value, s.charID)
}

func (s *Session) validateNameInputForChar(field, value string, charID uint32) bool {
	if !stringsupport.IsValidPlayerName(value) {
		s.logger.Warn("Rejected structurally invalid name",
			zap.String("field", field), zap.Uint32("charID", charID))
		return false
	}
	if s.server.ngWordFilter.Contains(value) {
		s.logger.Warn("Rejected name containing an NG word",
			zap.String("field", field), zap.Uint32("charID", charID))
		return false
	}
	return true
}

// validateMessageInput applies only the configured NG-word dictionary. Spaces,
// line breaks and standalone jamo remain legal in free-form messages.
func (s *Session) validateMessageInput(field, value string) bool {
	if s.server.ngWordFilter.Contains(value) {
		s.logger.Warn("Rejected message containing an NG word",
			zap.String("field", field), zap.Uint32("charID", s.charID))
		return false
	}
	return true
}
