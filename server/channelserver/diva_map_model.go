package channelserver

import "time"

// Optional repository contract: unrelated repositories need not synthesize maps.
type DivaMapRepository interface {
	SettleDivaMaps(now time.Time, override int) error
	GetDivaMap(charID, guildID, eventID uint32, now time.Time) (DivaMapView, error)
	BindDivaMapDeparture(charID, guildID, eventID uint32, questID uint16, runKey string, startedAt, now time.Time) (DivaMapDeparture, error)
	GetDivaMapRanking(charID, eventID uint32, now time.Time) (DivaMapRanking, error)
}

// A one-time activation enables map-only accounting in the current legacy
// round. It does not change personal-score or personal-reward eligibility.
type DivaMapActivationRepository interface {
	GetDivaMapActivation(eventID uint32) (time.Time, bool, error)
}

type DivaMapView struct {
	Enabled              bool
	Map                  DivaInterceptionMap
	SpecialTreasures     []DivaMapSpecialTreasureSelection
	SpecialTreasureError error // Optional presentation failure; the ordinary map remains usable.
}

type DivaMapDeparture struct {
	Enabled   bool
	MapNumber uint16
	Route     uint16 // Zero: main route. Nonzero: branch target coordinate.
}

type DivaMapRanking struct {
	Enabled bool
	Rows    []divaAreaRank
	Own     *divaAreaRank
}
