package channelserver

import (
	"time"

	"go.uber.org/zap"
)

const (
	guildMissionTargetLifetime             = 24 * time.Hour
	guildMissionEffectLifetime             = 48 * time.Hour
	guildMissionDefaultProgressPerExchange = 1
	// No non-empty official record capture establishes a non-zero value.
	// Zero prevents the client from destructively consuming guild tickets.
	guildMissionDefaultCancelTicketCost = 0
)

// guildMissionDefinitions is the target master currently advertised to the
// client. Mission IDs supplied by clients must resolve through this table.
var guildMissionDefinitions = []GuildMissionDefinition{
	{431201, 574, 1, 4761, 35, 1, false, 2, 1},
	{431202, 755, 0, 95, 12, 2, false, 3, 2},
	{431203, 746, 0, 95, 6, 1, false, 1, 1},
	{431204, 581, 0, 83, 16, 2, false, 4, 2},
	{431205, 694, 1, 4763, 25, 1, false, 2, 1},
	{431206, 988, 0, 27, 16, 1, false, 6, 1},
	{431207, 730, 1, 4768, 25, 1, false, 4, 1},
	{431208, 680, 1, 3567, 50, 2, false, 2, 2},
	{431209, 1109, 0, 34, 60, 2, false, 6, 2},
	{431210, 128, 1, 8921, 70, 2, false, 3, 2},
	{431211, 406, 0, 59, 10, 1, false, 1, 1},
	{431212, 1170, 0, 70, 90, 3, false, 6, 3},
	{431213, 164, 0, 38, 24, 2, false, 6, 2},
	{431214, 378, 1, 3556, 150, 3, false, 1, 3},
	{431215, 446, 0, 94, 20, 2, false, 4, 2},
}

var guildMissionDefinitionsByID = func() map[uint32]GuildMissionDefinition {
	defs := make(map[uint32]GuildMissionDefinition, len(guildMissionDefinitions))
	for _, def := range guildMissionDefinitions {
		defs[def.ID] = def
	}
	return defs
}()

// GuildMissionService validates protocol input before it reaches persistence.
type GuildMissionService struct {
	repo   GuildMissionRepo
	logger *zap.Logger
	now    func() time.Time
}

// NewGuildMissionService creates a guild mission service.
func NewGuildMissionService(repo GuildMissionRepo, logger *zap.Logger) *GuildMissionService {
	return &GuildMissionService{
		repo:   repo,
		logger: logger,
		now:    TimeAdjusted,
	}
}

// GetSnapshot returns current state for the actor's actual guild membership.
func (svc *GuildMissionService) GetSnapshot(charID uint32) (GuildMissionSnapshot, error) {
	return svc.repo.GetSnapshot(charID, svc.now())
}

// Start validates and selects a mission.
func (svc *GuildMissionService) Start(charID, missionID uint32) (GuildMissionRun, error) {
	def, ok := guildMissionDefinitionsByID[missionID]
	if !ok {
		return GuildMissionRun{}, ErrGuildMissionUnknown
	}
	return svc.repo.Start(charID, def, svc.now())
}

// AddProgress validates the mission ID and reports progress. The repository
// rejects an untrusted client count that exceeds the server-defined remainder.
func (svc *GuildMissionService) AddProgress(charID, missionID, count uint32) (GuildMissionProgressResult, error) {
	if _, ok := guildMissionDefinitionsByID[missionID]; !ok {
		return GuildMissionProgressResult{}, ErrGuildMissionUnknown
	}
	return svc.repo.AddProgress(charID, missionID, count, svc.now())
}

// Cancel validates and cancels the active mission.
func (svc *GuildMissionService) Cancel(charID, missionID uint32) error {
	if _, ok := guildMissionDefinitionsByID[missionID]; !ok {
		return ErrGuildMissionUnknown
	}
	return svc.repo.Cancel(charID, missionID, svc.now())
}
