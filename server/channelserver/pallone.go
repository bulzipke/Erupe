package channelserver

import (
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"time"

	"erupe-ce/common/mhfpallone"
	cfg "erupe-ce/config"
)

var (
	ErrPalloneInvalid    = errors.New("invalid Pallone event or result")
	ErrPalloneConflict   = errors.New("Pallone event or result conflicts with an existing receipt")
	ErrPalloneIneligible = errors.New("Pallone departure or result is outside its event")
	ErrPalloneGuild      = errors.New("Pallone guild does not match accepted membership")
	ErrPalloneScoreLimit = errors.New("Pallone score exceeds the native signed 32-bit range")
)

type PalloneEvent struct {
	ID         int32     `db:"id"`
	Start      time.Time `db:"starts_at"`
	End        time.Time `db:"ends_at"`
	ClaimUntil time.Time `db:"claim_until"`
}

func (e PalloneEvent) Active(t time.Time) bool  { return !t.Before(e.Start) && t.Before(e.End) }
func (e PalloneEvent) Visible(t time.Time) bool { return !t.Before(e.Start) && t.Before(e.ClaimUntil) }

// PalloneRoute describes a selected event route. Key 0 is reserved by the
// native ranking UI for the overall ranking. A published positive key must
// match its installed profile's active-bank ordinal, not a catalogue index.
type PalloneRoute struct {
	Key      int32
	Star     uint16
	QuestIDs []uint16
}

// PalloneRunReceipt is SERVER state, never a client-provided authorization.
// A native departure/result adapter must issue it only after validating the
// selected route and final stage. That adapter remains unarmed pending a real
// native departure/stage/result capture; a score packet alone issues nothing.
type PalloneRunReceipt struct {
	EventID      int32
	CharID       uint32
	RouteKey     int32
	RunID        string
	StartedAt    time.Time
	CompletedAt  time.Time
	FinalQuestID uint16
}

type PalloneSubmission struct {
	BaseScore    int32
	PremiumBonus int32
	ItemBonus    int32
	GuildID      uint32
}

type PalloneSettlement struct {
	// Zero is the client's native "no event multiplier" response. Event
	// multiplier schedules are not inferred from unrelated Earth/Tower values.
	Multiplier uint32
	Duplicate  bool
}

type PalloneService struct{ repo PalloneRepo }

func NewPalloneService(repo PalloneRepo) *PalloneService { return &PalloneService{repo: repo} }

func validPalloneSubmission(v PalloneSubmission) bool {
	if v.BaseScore <= 0 || v.PremiumBonus < 0 || v.ItemBonus < 0 {
		return false
	}
	// The stock client sends 0/2500/5000/10000 here. The existing D463
	// HCandleSend restoration also adds earned antique bonuses to this word,
	// so restricting it to only those four stock values rejects valid runs.
	return int64(v.BaseScore)+int64(v.ItemBonus)+int64(v.PremiumBonus) <= math.MaxInt32
}

func normalizePalloneReceipt(v PalloneRunReceipt) (PalloneRunReceipt, error) {
	if v.EventID <= 0 || v.CharID == 0 || v.CharID > math.MaxInt32 || v.RouteKey <= 0 || v.RouteKey > math.MaxInt16 || v.FinalQuestID == 0 || len(v.RunID) != 32 {
		return PalloneRunReceipt{}, ErrPalloneInvalid
	}
	if _, err := hex.DecodeString(v.RunID); err != nil {
		return PalloneRunReceipt{}, ErrPalloneInvalid
	}
	v.RunID = strings.ToLower(v.RunID)
	v.StartedAt = v.StartedAt.UTC().Truncate(time.Microsecond)
	v.CompletedAt = v.CompletedAt.UTC().Truncate(time.Microsecond)
	if v.StartedAt.IsZero() || v.CompletedAt.IsZero() || v.CompletedAt.Before(v.StartedAt) {
		return PalloneRunReceipt{}, ErrPalloneInvalid
	}
	return v, nil
}

func (s *PalloneService) Submit(charID uint32, receipt PalloneRunReceipt, submission PalloneSubmission, now time.Time) (PalloneSettlement, error) {
	r, err := normalizePalloneReceipt(receipt)
	if err != nil || r.CharID != charID || !validPalloneSubmission(submission) || now.Before(r.CompletedAt) || s == nil || s.repo == nil {
		return PalloneSettlement{}, ErrPalloneInvalid
	}
	return s.repo.SettlePalloneResult(r, submission, now)
}

func validatePalloneEvent(e PalloneEvent, routes []PalloneRoute) error {
	if e.ID <= 0 || !e.Start.Before(e.End) || !e.End.Before(e.ClaimUntil) || e.Start.Unix() < 0 || e.ClaimUntil.Unix() > math.MaxUint32 || len(routes) == 0 || len(routes) > mhfpallone.MaxRoutes {
		return ErrPalloneInvalid
	}
	seen := make(map[int32]bool)
	var groups [mhfpallone.RouteBankCount]int
	for _, r := range routes {
		if r.Key <= 0 || r.Key > math.MaxInt16 || seen[r.Key] || r.Star >= mhfpallone.RouteBankCount || len(r.QuestIDs) == 0 || len(r.QuestIDs) >= 12 {
			return ErrPalloneInvalid
		}
		seen[r.Key] = true
		groups[r.Star]++
		if groups[r.Star] > 12 {
			return ErrPalloneInvalid
		}
		for _, q := range r.QuestIDs {
			if q == 0 {
				return ErrPalloneInvalid
			}
		}
	}
	return nil
}

// Runtime activation uses the EXISTING explicit manual Earth selection. Empty
// event tables, other client versions, and Tower rotation retain their current
// behavior. Creating an event does not change EarthStatus or activate it.
func (s *Server) palloneEvent(now time.Time) (*PalloneEvent, error) {
	e, err := s.selectedPalloneEvent()
	if err != nil || e == nil {
		return e, err
	}
	if !e.Visible(now) {
		return nil, nil
	}
	return e, nil
}

func (s *Server) selectedPalloneEvent() (*PalloneEvent, error) {
	c := s.erupeConfig
	if s.palloneRepo == nil || c.RealClientMode != cfg.ZZ || c.TowerRotation.Enabled || (c.EarthStatus != 11 && c.EarthStatus != 12) {
		return nil, nil
	}
	return s.palloneRepo.GetPalloneEvent(c.EarthID)
}
