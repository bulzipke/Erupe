package channelserver

import (
	"fmt"
	"math"
	"slices"
	"time"
)

// Bound both the trusted target batch and the repository's array query. This
// covers the current legacy catalog with room to grow without an unbounded
// per-session authorization read. Quest IDs themselves remain uint16 values.
const maxQuestAccessTargets = 4096

// QuestAccessCatalog supplies trusted immutable server-side mappings only. A
// catalog may map alternative permanent licenses to the same quest; owning any
// one suffices.
type QuestAccessCatalog interface {
	KeysForQuest(int) []string
}

// QuestAccessLoadRequest must use the authenticated connection's identity and
// exact server-selected quest IDs. Loading is a repository operation and must
// happen before acquiring Server, Stage, or Session gameplay locks.
//
// Disabling account licenses does not disable legacy character campaign rights
// and does not require a catalog or the new account-license database tables.
// Before enabling account licenses, the caller must validate the catalog's
// permanent key-to-quest definitions against the durable definition registry.
// This read-only loader neither binds nor changes those definitions.
type QuestAccessLoadRequest struct {
	UserID                 uint32
	CharacterID            uint32
	QuestIDs               []int
	AccountLicensesEnabled bool
	Catalog                QuestAccessCatalog
}

type questAccessGrant struct {
	accountPermanent bool
	legacyExpiry     time.Time
}

// QuestAccessSnapshot is detached from its inputs and immutable after creation.
// It grants only a code/license right, never publication, an entry item,
// equipment, progression, or a character-specific completion/reward allowance.
// A future admission must pin an already-issued snapshot; loading a newer one
// cannot establish rights for an older admission.
type QuestAccessSnapshot struct {
	userID           uint32
	characterID      uint32
	observedAt       time.Time
	grants           map[int]questAccessGrant
	accountReadError error
}

// Retain an immutable diagnostic value rather than exposing a mutable driver
// error (for example *pq.Error) through an otherwise immutable snapshot.
type questAccessAccountReadWarning string

func (e questAccessAccountReadWarning) Error() string { return string(e) }

func newQuestAccessSnapshot(userID, characterID uint32, observedAt time.Time, grants map[int]questAccessGrant, accountReadError error) *QuestAccessSnapshot {
	frozen := make(map[int]questAccessGrant, len(grants))
	for id, grant := range grants {
		frozen[id] = grant
	}
	snapshot := &QuestAccessSnapshot{
		userID: userID, characterID: characterID, observedAt: observedAt.UTC(), grants: frozen,
	}
	if accountReadError != nil {
		snapshot.accountReadError = questAccessAccountReadWarning(accountReadError.Error())
	}
	return snapshot
}

// ObservedAt is PostgreSQL's wall time after all consistent evidence reads. It
// deliberately does not use BEGIN time, which can precede the MVCC snapshot.
func (s *QuestAccessSnapshot) ObservedAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.observedAt
}

// AccountReadError reports a recoverable account-store failure. Such a snapshot
// contains no account grants, but independently verified legacy grants remain
// usable. A nil snapshot has no diagnostic and grants nothing.
func (s *QuestAccessSnapshot) AccountReadError() error {
	if s == nil {
		return nil
	}
	return s.accountReadError
}

// Allows is a pure, lock-free check. Unknown quests and times before this
// observation are denied. Legacy expiry is exclusive; account ownership is
// permanent and is not limited by a product's purchase window.
func (s *QuestAccessSnapshot) Allows(questID int, at time.Time) bool {
	if s == nil || s.observedAt.IsZero() || at.IsZero() || at.Before(s.observedAt) {
		return false
	}
	grant, known := s.grants[questID]
	return known && (grant.accountPermanent || (!grant.legacyExpiry.IsZero() && at.Before(grant.legacyExpiry)))
}

// prepareQuestAccessRequest copies the selected mappings before any database
// work. It deliberately never discovers additional quests from account keys.
func prepareQuestAccessRequest(request QuestAccessLoadRequest) ([]int, map[int][]string, []string, error) {
	if request.UserID == 0 || request.UserID > math.MaxInt32 || request.CharacterID == 0 || request.CharacterID > math.MaxInt32 {
		return nil, nil, nil, fmt.Errorf("invalid quest access identity")
	}
	if len(request.QuestIDs) > maxQuestAccessTargets {
		return nil, nil, nil, fmt.Errorf("too many quest access targets")
	}
	ids := slices.Clone(request.QuestIDs)
	slices.Sort(ids)
	for i, id := range ids {
		if id <= 0 || id > math.MaxUint16 || (i > 0 && ids[i-1] == id) {
			return nil, nil, nil, fmt.Errorf("invalid or duplicate quest access target %d", id)
		}
	}
	mappings := make(map[int][]string, len(ids))
	if !request.AccountLicensesEnabled {
		return ids, mappings, nil, nil
	}
	if request.Catalog == nil {
		return nil, nil, nil, fmt.Errorf("enabled account quest access requires a catalog")
	}
	uniqueKeys := make(map[string]bool)
	for _, id := range ids {
		keys := request.Catalog.KeysForQuest(id)
		if len(keys) == 0 {
			continue
		}
		canonical, _, err := canonicalQuestLicenseKeys(keys)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("quest access target %d: %w", id, err)
		}
		mappings[id] = canonical
		for _, key := range canonical {
			uniqueKeys[key] = true
		}
	}
	keys := make([]string, 0, len(uniqueKeys))
	for key := range uniqueKeys {
		keys = append(keys, key)
	}
	if len(keys) != 0 {
		var err error
		keys, _, err = canonicalQuestLicenseKeys(keys)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("quest access account keys: %w", err)
		}
	}
	return ids, mappings, keys, nil
}
