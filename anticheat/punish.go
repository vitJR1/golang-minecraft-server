package anticheat

import (
	"context"
	"fmt"
	"time"
)

// punish.go is the punishment ladder from anticheat.md: every player has a
// persistent status, and each detection moves it one step up and applies
// that step's action. A detection is a check whose violation level reaches
// its threshold; single flags only roll back the action.

// Status is a player's place on the ladder. It persists across sessions and
// only goes down through an admin's /ac reset.
type Status int

const (
	StatusClean      Status = 0
	StatusSuspicious Status = 1
	StatusBanned1d   Status = 2
	StatusBanned3d   Status = 3
	StatusBanned7d   Status = 4
	StatusMax        Status = 5
)

func (s Status) String() string {
	switch s {
	case StatusClean:
		return "clean"
	case StatusSuspicious:
		return "suspicious"
	case StatusBanned1d:
		return "banned_1d"
	case StatusBanned3d:
		return "banned_3d"
	case StatusBanned7d:
		return "banned_7d"
	case StatusMax:
		return "max"
	}
	return fmt.Sprintf("status(%d)", int(s))
}

// Action kinds a punishment record carries.
const (
	ActionKick  = "kick"
	ActionBan   = "ban"
	ActionReset = "reset" // an admin cleared the status (/ac reset)
)

// Step is what a detection does at some status.
type Step struct {
	Next         Status
	Action       string        // ActionKick or ActionBan
	Ban          time.Duration // for ActionBan
	NotifyAdmins bool
}

// Escalate returns the step for a detection at status s:
//
//	0 clean      → 1, kick
//	1 suspicious → 2, ban 1 day
//	2            → 3, ban 3 days
//	3            → 4, ban 7 days
//	4            → 5 (max), ban 1 day + notify admins
//	5 max        → 5, kick + notify admins (the ban is the admins' call)
func Escalate(s Status) Step {
	const day = 24 * time.Hour
	switch {
	case s <= StatusClean:
		return Step{Next: StatusSuspicious, Action: ActionKick}
	case s == StatusSuspicious:
		return Step{Next: StatusBanned1d, Action: ActionBan, Ban: day}
	case s == StatusBanned1d:
		return Step{Next: StatusBanned3d, Action: ActionBan, Ban: 3 * day}
	case s == StatusBanned3d:
		return Step{Next: StatusBanned7d, Action: ActionBan, Ban: 7 * day}
	case s == StatusBanned7d:
		return Step{Next: StatusMax, Action: ActionBan, Ban: day, NotifyAdmins: true}
	default:
		return Step{Next: StatusMax, Action: ActionKick, NotifyAdmins: true}
	}
}

// DefaultThreshold is the violation level at which a check counts as a
// detection. Every check today flags packets a vanilla client never sends,
// so a few in a row is enough; a timing race can explain one, not three.
// VL decays continuously (0.1/s), so three flags land just under 3.0 —
// 2.5 means "three flags within ~5 s", while two never reach it.
const DefaultThreshold = 2.5

// Thresholds overrides DefaultThreshold per check — for heuristic checks
// that need more evidence than an impossible packet.
var Thresholds = map[string]float64{
	// Reach is a measurement with lag compensation, not an impossible
	// packet: a few borderline hits pass before it counts.
	"combat.reach": 4,
	// Timer measures packet timing over the network and phase relies on
	// the server's block model matching the client's — both are evidence,
	// not proof, until repeated.
	"movement.timer": 5,
	"movement.phase": 5,
	// Speed and flight are physics approximations with budgets for pushes
	// and effects; each violation already costs a setback.
	"movement.speed": 5,
	"movement.fly":   5,
}

// Threshold is the detection level for check.
func Threshold(check string) float64 {
	if t, ok := Thresholds[check]; ok {
		return t
	}
	return DefaultThreshold
}

// Record is one entry of the punishment log: who, when, the status move, the
// action and — always — the reasons.
type Record struct {
	ID       int64     `json:"id"`
	UUID     string    `json:"uuid"` // canonical 8-4-4-4-12 text
	Name     string    `json:"name"` // username at the time
	At       time.Time `json:"at"`
	Instance string    `json:"instance"`
	Before   Status    `json:"before"`
	After    Status    `json:"after"`
	Action   string    `json:"action"`
	// BanUntil is when an ActionBan expires (zero otherwise).
	BanUntil time.Time `json:"banUntil,omitzero"`
	// FlagAdmin marks a record admins were notified about (the move to max
	// and every detection at max).
	FlagAdmin bool `json:"flagAdmin,omitempty"`
	// Reasons are the flags that led here (check, detail, VL, time); for an
	// admin action, one entry with the admin's own reason.
	Reasons []Violation `json:"reasons"`
	// By is "anticheat" or the admin's name.
	By string `json:"by"`
}

// IssuedByAnticheat is Record.By for automatic punishments.
const IssuedByAnticheat = "anticheat"

// Store persists statuses and the punishment log.
type Store interface {
	// Status returns the player's status (StatusClean if never punished).
	Status(ctx context.Context, uuid string) (Status, error)
	// Record appends rec to the log and sets the player's status to
	// rec.After, atomically.
	Record(ctx context.Context, rec Record) error
	// History returns the player's latest records, newest first.
	History(ctx context.Context, uuid string, limit int) ([]Record, error)
	// UUIDByName finds a player by the name on their latest record
	// (case-insensitive), for admin commands about offline players.
	UUIDByName(ctx context.Context, name string) (uuid string, ok bool, err error)
}
