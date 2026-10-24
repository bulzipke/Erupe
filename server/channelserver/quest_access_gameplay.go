package channelserver

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"math"
	"slices"
	"strings"
	"time"
)

// QuestAccessGameplayLoader is invoked before gameplay locks are acquired.
// QuestAccessService implements it, including independent legacy fallback.
type QuestAccessGameplayLoader interface {
	Load(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error)
}

// QuestAccessGameplay is an immutable, startup-frozen protected quest roster.
// It controls code/license authority only. A nil server field disables this
// integration without changing the separate publication manifest.
type QuestAccessGameplay struct {
	loader   QuestAccessGameplayLoader
	questIDs []int
	required map[int]struct{}
}

func NewQuestAccessGameplay(loader QuestAccessGameplayLoader, questIDs []int) (*QuestAccessGameplay, error) {
	if loader == nil {
		return nil, fmt.Errorf("quest access gameplay requires a loader")
	}
	if len(questIDs) > maxQuestAccessTargets {
		return nil, fmt.Errorf("too many quest access gameplay targets")
	}
	ids := slices.Clone(questIDs)
	slices.Sort(ids)
	for _, id := range ids {
		if id <= 0 || id > math.MaxUint16 {
			return nil, fmt.Errorf("invalid quest access gameplay target %d", id)
		}
	}
	ids = slices.Compact(ids)
	gameplay := &QuestAccessGameplay{loader: loader, questIDs: ids, required: make(map[int]struct{}, len(ids))}
	for _, id := range ids {
		gameplay.required[id] = struct{}{}
	}
	return gameplay, nil
}

func (g *QuestAccessGameplay) requires(questID int) bool {
	if g == nil {
		return false
	}
	_, exists := g.required[questID]
	return exists
}

// refreshQuestAccess performs every repository/definition read before any
// Server, Stage, Session, or lifecycle lock. Authenticated session identities
// and the server roster are fixed for this connection/server lifetime.
func (s *Session) refreshQuestAccess(ctx context.Context) error {
	if s == nil || s.server == nil || s.server.questAccess == nil {
		return nil
	}
	gameplay := s.server.questAccess
	previous := s.questAccessSnapshot.Load()
	snapshot, err := gameplay.loader.Load(ctx, s.userID, s.charID, slices.Clone(gameplay.questIDs))
	if err == nil && (snapshot == nil || snapshot.userID != s.userID || snapshot.characterID != s.charID || snapshot.ObservedAt().IsZero()) {
		err = fmt.Errorf("quest access gameplay received an invalid snapshot")
	}
	if err != nil {
		// A slow failed read must not erase a newer successful publication. An
		// already pinned departure/receipt remains independent of this cache.
		s.questAccessSnapshot.CompareAndSwap(previous, nil)
		return err
	}
	if warning := snapshot.AccountReadError(); warning != nil && s.logger != nil {
		s.logger.Warn("Account quest authority unavailable; preserving legacy rights", zap.Int("quest_targets", len(gameplay.questIDs)), zap.Error(warning))
	}
	for {
		old := s.questAccessSnapshot.Load()
		if old != nil && old.ObservedAt().After(snapshot.ObservedAt()) {
			return nil
		}
		if s.questAccessSnapshot.CompareAndSwap(old, snapshot) {
			return nil
		}
	}
}

// prepareQuestAccessAdmission runs before doStageTransfer takes lifecycleMu.
// A duplicate live-run transfer needs no new database read. Its nil sentinel
// may only reuse the SAME already-admitted Stage under the final stage lock;
// if another transfer won that race, admission must fail instead of borrowing
// the old cache for a new entry.
func (s *Session) prepareQuestAccessAdmission(ctx context.Context, stageID string) (*QuestAccessSnapshot, error) {
	if s == nil || s.server == nil || s.server.questAccess == nil || stageKind(stageID) != "Qs" {
		return nil, nil
	}
	if old := s.questDeliveryDeparture.Load(); old != nil && old.stage != nil && old.stage.id == stageID {
		return nil, nil
	}
	if err := s.refreshQuestAccess(ctx); err != nil {
		return nil, err
	}
	return s.questAccessSnapshot.Load(), nil
}

func (s *Session) questEntryPoliciesEnabled() bool {
	return s != nil && s.server != nil && (s.server.questDelivery != nil || s.server.questAccess != nil)
}

// questAccessDecision is pure; a listing/file request uses the current cache.
// Admission and results use allowsQuestAccessSnapshot with their pinned value.
func (s *Session) questAccessDecision(questID int, at time.Time) (bool, bool) {
	if s == nil || s.server == nil || !s.server.questAccess.requires(questID) {
		return false, false
	}
	return true, s.allowsQuestAccessSnapshot(s.questAccessSnapshot.Load(), questID, at)
}

func (s *Session) allowsQuestAccess(questID int, at time.Time) bool {
	managed, allowed := s.questAccessDecision(questID, at)
	return !managed || allowed
}

func (s *Session) allowsQuestAccessSnapshot(snapshot *QuestAccessSnapshot, questID int, at time.Time) bool {
	if s == nil || s.server == nil || !s.server.questAccess.requires(questID) {
		return true
	}
	return snapshot != nil && snapshot.userID == s.userID && snapshot.characterID == s.charID && snapshot.Allows(questID, at)
}

func (s *Session) accessFileAllowed(filename string, at time.Time) bool {
	questID, ok := deliveryQuestIDFromFilename(strings.TrimSpace(filename))
	return !ok || s.allowsQuestAccess(questID, at)
}

// A result of a protected run must match that exact run and its admission
// authority. A newly bought right can never repair an older unlicensed entry.
func (s *Session) allowsQuestAccessResult(receipt *questDeliveryReceipt, questID uint16) bool {
	if s == nil || s.server == nil || s.server.questAccess == nil {
		return true
	}
	protected := s.server.questAccess.requires(int(questID))
	if receipt != nil {
		protected = protected || s.server.questAccess.requires(int(receipt.questID))
	}
	if !protected {
		return true
	}
	return receipt != nil && receipt.questID == questID && s.allowsQuestAccessSnapshot(receipt.accessSnapshot, int(questID), receipt.admittedAt)
}
