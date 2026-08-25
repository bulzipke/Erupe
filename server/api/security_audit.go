package api

import (
	"context"

	"erupe-ce/server/securityaudit"

	"go.uber.org/zap"
)

func (s *APIServer) recordSecurityAudit(ctx context.Context, event securityaudit.Event) {
	if s == nil || s.securityAudit == nil {
		return
	}
	if err := s.securityAudit.Record(ctx, event); err != nil && s.logger != nil {
		s.logger.Error("Failed to persist API security audit event",
			zap.String("eventType", event.Type), zap.Error(err))
	}
}

func (s *APIServer) auditImport(userID, charID uint32, severity, decision, reason string) {
	s.recordSecurityAudit(context.Background(), securityaudit.Event{
		UserID:      userID,
		CharacterID: charID,
		Source:      "api",
		Type:        "savedata_import",
		Severity:    severity,
		Decision:    decision,
		Details: map[string]interface{}{
			"reason": reason,
		},
	})
}
