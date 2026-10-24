package channelserver

import (
	"context"
	"errors"
	"fmt"
)

// QuestAccessService prepares authority outside gameplay locks. A caller must
// publish the resulting immutable snapshot and pin that same snapshot at stage
// admission. It is not connected to gameplay in this backend stage.
type QuestAccessService struct {
	store    QuestAccessStore
	licenses *QuestLicenseService
}

func NewQuestAccessService(store QuestAccessStore, licenses *QuestLicenseService) (*QuestAccessService, error) {
	if store == nil {
		return nil, fmt.Errorf("quest access service requires a store")
	}
	return &QuestAccessService{store: store, licenses: licenses}, nil
}

// Load always preserves legacy authentication as an independent source. Account
// authority is used only after the complete permanent definitions are bound.
// A failed account-definition guard falls back to a verified legacy snapshot
// with an explicit diagnostic; it cannot silently grant any account authority.
func (s *QuestAccessService) Load(ctx context.Context, userID, characterID uint32, questIDs []int) (*QuestAccessSnapshot, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("quest access service unavailable")
	}
	request := QuestAccessLoadRequest{UserID: userID, CharacterID: characterID, QuestIDs: questIDs}
	ids, _, _, err := prepareQuestAccessRequest(request)
	if err != nil {
		return nil, err
	}
	request.QuestIDs = ids
	var definitionError error
	if licenses := s.licenses; licenses != nil && licenses.enabled && licenses.catalog != nil && licenses.store != nil {
		definitionError = licenses.ensureDefinitions(ctx)
		if definitionError == nil {
			request.AccountLicensesEnabled = true
			request.Catalog = licenses.catalog
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	snapshot, err := s.store.Load(ctx, request)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.userID != userID || snapshot.characterID != characterID {
		return nil, fmt.Errorf("quest access store returned a different identity")
	}
	if definitionError != nil {
		return newQuestAccessSnapshot(userID, characterID, snapshot.observedAt, snapshot.grants,
			errors.Join(definitionError, snapshot.AccountReadError())), nil
	}
	return snapshot, nil
}
