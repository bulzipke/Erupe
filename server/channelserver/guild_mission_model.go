package channelserver

import (
	"errors"
	"time"
)

// GuildMissionDefinition is one target advertised by
// MSG_MHF_GET_GUILD_MISSION_LIST.
type GuildMissionDefinition struct {
	ID          uint32
	Unk         uint32 // Second master-list word; the ZZ client does not use it as cancellation cost.
	Type        uint16
	Goal        uint16
	Quantity    uint16
	SkipTickets uint16
	GR          bool
	RewardType  uint16
	RewardLevel uint16
}

// GuildMissionRunState is the persistent lifecycle state of a selected target.
type GuildMissionRunState string

const (
	GuildMissionRunActive    GuildMissionRunState = "active"
	GuildMissionRunCompleted GuildMissionRunState = "completed"
	GuildMissionRunCancelled GuildMissionRunState = "cancelled"
	GuildMissionRunExpired   GuildMissionRunState = "expired"
)

// GuildMissionRun is a selected target and its shared guild progress.
type GuildMissionRun struct {
	ID                  uint64               `db:"id"`
	GuildID             uint32               `db:"guild_id"`
	MissionID           uint32               `db:"mission_id"`
	TargetType          uint16               `db:"target_type"`
	TargetID            uint32               `db:"target_id"`
	RequiredCount       uint32               `db:"required_count"`
	SkipTickets         uint16               `db:"skip_tickets"`
	ProgressPerExchange uint16               `db:"progress_per_exchange"`
	CancelTicketCost    uint16               `db:"cancel_ticket_cost"`
	GR                  bool                 `db:"is_gr"`
	RewardType          uint16               `db:"reward_type"`
	RewardLevel         uint16               `db:"reward_level"`
	Progress            uint32               `db:"progress"`
	State               GuildMissionRunState `db:"state"`
	SetBy               *uint32              `db:"set_by"`
	CompletedBy         *uint32              `db:"completed_by"`
	CancelledBy         *uint32              `db:"cancelled_by"`
	SetAt               time.Time            `db:"set_at"`
	TargetExpiresAt     time.Time            `db:"target_expires_at"`
	CompletedAt         *time.Time           `db:"completed_at"`
	EffectExpiresAt     *time.Time           `db:"effect_expires_at"`
	CancelledAt         *time.Time           `db:"cancelled_at"`
	UpdatedAt           time.Time            `db:"updated_at"`
}

// GuildMissionSnapshot is the current target plus still-active effects from
// previously completed targets.
type GuildMissionSnapshot struct {
	Active  *GuildMissionRun
	Effects []GuildMissionRun
}

// GuildMissionProgressResult describes how much of a client report was
// accepted. Reports larger than the remaining target quantity are rejected so
// the client can roll back all items or tickets it removed before the request.
type GuildMissionProgressResult struct {
	Run       GuildMissionRun
	Applied   uint32
	Completed bool
}

var (
	ErrGuildMissionNotMember       = errors.New("character is not a guild member")
	ErrGuildMissionUnknown         = errors.New("unknown guild mission")
	ErrGuildMissionAlreadyActive   = errors.New("guild already has an active mission")
	ErrGuildMissionNoActiveTarget  = errors.New("guild has no active mission")
	ErrGuildMissionTargetMismatch  = errors.New("guild mission target does not match")
	ErrGuildMissionTooMuchProgress = errors.New("guild mission progress exceeds remaining target")
)
