package channelserver

import (
	"context"

	"erupe-ce/server/securityaudit"

	"go.uber.org/zap"
)

func (s *Session) recordSecurityAudit(eventType, severity, decision string, details map[string]interface{}) {
	if s == nil || s.server == nil {
		return
	}
	if s.server.securityAuditRepo != nil {
		if err := s.server.securityAuditRepo.Record(context.Background(), securityaudit.Event{
			UserID:      s.userID,
			CharacterID: s.charID,
			Source:      "channel",
			Type:        eventType,
			Severity:    severity,
			Decision:    decision,
			Details:     details,
		}); err != nil && s.logger != nil {
			s.logger.Error("Failed to persist security audit event",
				zap.String("eventType", eventType), zap.Error(err))
		}
	}
}
