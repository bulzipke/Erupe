package channelserver

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	cfg "erupe-ce/config"
)

// This detached candidate changes no benefit period. Each value is one of the
// two original account columns, with NULL and PostgreSQL infinities distinct.
type returnQuestExpiryKind uint8

const (
	returnQuestExpiryNull returnQuestExpiryKind = iota
	returnQuestExpiryNegativeInfinity
	returnQuestExpiryFinite
	returnQuestExpiryPositiveInfinity
)

type returnQuestExpiry struct {
	kind returnQuestExpiryKind
	at   time.Time // populated only for a finite database value
}

type returnQuestExpiries struct {
	userID                       uint32
	returnExpiry, newcomerExpiry returnQuestExpiry
}

// Optional capability: old UserRepo and ReturnWorldEligible contracts remain
// intact. An old boolean result is never a substitute for these two values.
type returnQuestExpiryReader interface {
	ReadReturnQuestExpiries(context.Context, uint32) (returnQuestExpiries, error)
}

// Detached from the repository, immutable, and request-local until a successful
// departure pins it. There is deliberately no Session-level privilege cache.
type returnQuestEligibilitySnapshot struct {
	userID, characterID uint32
	observedAt          time.Time
	expiries            returnQuestExpiries
}

func (e returnQuestExpiry) valid() bool {
	if e.kind == returnQuestExpiryFinite {
		// PostgreSQL's native timestamp precision is one microsecond. Reject a
		// malformed reader value rather than silently deriving another expiry.
		return e.at.Nanosecond()%1000 == 0
	}
	return e.kind <= returnQuestExpiryPositiveInfinity && e.at.IsZero()
}

func (s *Session) returnQuestModeSupported() bool {
	return s != nil && s.server != nil && s.server.erupeConfig != nil && s.server.erupeConfig.RealClientMode >= cfg.G10
}

func (s *Session) loadReturnQuestEligibility(ctx context.Context) (*returnQuestEligibilitySnapshot, error) {
	if !s.returnQuestModeSupported() {
		return nil, nil
	}
	if ctx == nil || s.userID == 0 || s.charID == 0 {
		return nil, fmt.Errorf("Return quest eligibility requires an authenticated account and character")
	}
	reader, ok := s.server.userRepo.(returnQuestExpiryReader)
	if !ok {
		return nil, fmt.Errorf("Return quest expiry reader is unavailable")
	}
	userID, characterID := s.userID, s.charID
	values, err := reader.ReadReturnQuestExpiries(ctx, userID)
	if err != nil {
		return nil, err
	}
	if values.userID != userID || !values.returnExpiry.valid() || !values.newcomerExpiry.valid() {
		return nil, fmt.Errorf("Return quest expiry reader returned invalid account evidence")
	}
	return &returnQuestEligibilitySnapshot{
		userID: userID, characterID: characterID, observedAt: time.Now().UTC(), expiries: values,
	}, nil
}

// returnQuestPGInputTime models lib/pq v1.12.3's fractional timestamp text and
// PostgreSQL 16 ParseFractionalSecond: decimal -> double -> rint(frac*1e6).
// It is intentionally not time.Truncate or time.Round. Half-microsecond values
// can be affected by double conversion before ties-to-even rounding. The live
// server/driver differential suite remains required before operating activation
// or deployment: this pure model is not a live SQL equivalence proof.
// Sources: lib/pq/internal/pqtime.Format and PostgreSQL datetime.c
// ParseFraction/ParseFractionalSecond, REL_16_STABLE. Only the comparison value
// is converted; the actual admittedAt and Type9 observation keep nanoseconds.
func returnQuestPGInputTime(at time.Time) time.Time {
	nanos := at.Nanosecond()
	if nanos == 0 {
		return at
	}
	var fraction [10]byte
	fraction[0] = '.'
	value := nanos
	for i := len(fraction) - 1; i > 0; i-- {
		fraction[i] = byte('0' + value%10)
		value /= 10
	}
	parsed, err := strconv.ParseFloat(string(fraction[:]), 64)
	if err != nil {
		panic("fixed decimal fractional second could not be parsed")
	}
	micros := int64(math.RoundToEven(parsed * 1000000))
	return at.Add(-time.Duration(nanos)).Add(time.Duration(micros) * time.Microsecond)
}

func (e returnQuestExpiry) after(at time.Time) bool {
	switch e.kind {
	case returnQuestExpiryPositiveInfinity:
		return true
	case returnQuestExpiryFinite:
		return at.Before(e.at)
	default: // SQL COALESCE(NULL > at, false) and -infinity > finite at
		return false
	}
}

func (s *Session) allowsReturnQuestSnapshot(snapshot *returnQuestEligibilitySnapshot, questID int, at time.Time) bool {
	if !isReturnWorldQuest(questID) {
		return true
	}
	if !s.returnQuestModeSupported() || snapshot == nil || snapshot.userID != s.userID || snapshot.characterID != s.charID || snapshot.expiries.userID != s.userID || snapshot.observedAt.IsZero() || at.IsZero() || at.Before(snapshot.observedAt) {
		return false
	}
	if !snapshot.expiries.returnExpiry.valid() || !snapshot.expiries.newcomerExpiry.valid() {
		return false
	}
	comparedAt := returnQuestPGInputTime(at)
	return snapshot.expiries.returnExpiry.after(comparedAt) || snapshot.expiries.newcomerExpiry.after(comparedAt)
}

// Unknown setup needs original values ready before the stage lock. A failed or
// unsupported read is not authority: ordinary quests still work, whereas a
// known Return quest or subsequent Return setup fails its quest request.
func (s *Session) prepareReturnQuestAdmission(ctx context.Context, stageID string) (*returnQuestEligibilitySnapshot, error) {
	if stageKind(stageID) != "Qs" || !s.returnQuestModeSupported() {
		return nil, nil
	}
	if old := s.questDeliveryDeparture.Load(); old != nil && old.stage != nil && old.stage.id == stageID {
		return nil, nil // only SAME-stage alreadyClient admission may reuse this
	}
	return s.loadReturnQuestEligibility(ctx)
}

func (s *Session) allowsReturnQuestResult(receipt *questDeliveryReceipt, questID uint16) bool {
	protected := isReturnWorldQuest(int(questID)) || receipt != nil && isReturnWorldQuest(int(receipt.questID))
	if !protected {
		return true
	}
	return receipt != nil && receipt.questID == questID && s.allowsReturnQuestSnapshot(receipt.returnEligibility, int(questID), receipt.admittedAt)
}

func returnQuestFilename(filename string) bool {
	id, ok := deliveryQuestIDFromFilename(strings.TrimSpace(filename))
	return ok && isReturnWorldQuest(id)
}

func (s *Session) returnQuestFileAllowed(snapshot *returnQuestEligibilitySnapshot, requested, resolved string, at time.Time) bool {
	for _, filename := range []string{requested, resolved} {
		id, ok := deliveryQuestIDFromFilename(strings.TrimSpace(filename))
		if ok && !s.allowsReturnQuestSnapshot(snapshot, id, at) {
			return false
		}
	}
	return true
}

// Pure precheck under the existing stage lock. It rejects an entire Return
// setup before any mutation; it never produces a connection-close rejection.
// A participant already leaving the stage is ignored just as in the original
// publication/code validator. No repository or Session lock is taken here.
func (s *Session) returnQuestSetupAllowedLocked(stage *Stage, questID uint16, decoded bool) bool {
	for participant := range stage.clients {
		old := participant.questDeliveryDeparture.Load()
		if old != nil && old.stage == stage && isReturnWorldQuest(int(old.questID)) && (!decoded || old.questID != questID) {
			// Keep a validated Return run intact even when publication/code
			// manifests are nil. Ordinary malformed setup behavior is unchanged.
			return false
		}
	}
	if !decoded || !isReturnWorldQuest(int(questID)) {
		return true // existing ordinary/setup rules retain their old scope
	}
	if previous, exists := stage.rawBinaryData[stageBinaryKey{1, 3}]; exists && len(stage.clients) != 0 {
		previousID, _, _, valid := decodeQuestRunSetupFromStageBinary(stage.id, 1, 3, previous)
		if !valid || previousID != questID {
			return false
		}
	}
	for participant := range stage.clients {
		old := participant.questDeliveryDeparture.Load()
		if old != nil && old.stage != stage {
			continue
		}
		if old == nil || old.admittedAt.IsZero() || old.questID != 0 && old.questID != questID || !participant.allowsReturnQuestSnapshot(old.returnEligibility, int(questID), old.admittedAt) {
			return false
		}
	}
	return true
}
