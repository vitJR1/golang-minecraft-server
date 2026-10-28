package anticheat

import "time"

// timer.go catches a client whose game clock runs fast ("timer"): every
// movement it simulates is real, so speed and fly checks pass it, but it
// simulates more than 20 ticks a second.
//
// A vanilla client sends at most one movement packet (Set Position, Set
// Position+Rotation, Set Rotation or the on-ground-only status) per tick,
// so each packet is 50 ms of the client's game time. The balance adds 50 ms
// per packet and subtracts the wall-clock time between packets: it hovers
// around zero for a normal client, sinks while it stands still (no
// packets), and rises steadily under a timer. A lag spike delays packets
// and then delivers them at once — the balance dips first and the burst
// only brings it back. A standing player banks negative balance, so the
// bank is capped: otherwise hours of AFK would pay for minutes of timer.

const (
	// TickDuration is one client tick.
	TickDuration = 50 * time.Millisecond
	// TimerTolerance is how far ahead of real time the client may get
	// before it is flagged — network jitter, not cheating, lives below it.
	TimerTolerance = 150 * time.Millisecond
	// TimerMaxBank caps the negative balance an idle client accumulates.
	TimerMaxBank = time.Second
)

// Timer is the per-connection balance. The zero value is ready; only the
// read loop uses it.
type Timer struct {
	balance time.Duration
	last    time.Time
	started bool
}

// Move counts one movement packet arriving at now and reports whether the
// client is now more than TimerTolerance ahead of real time. A flag resets
// the balance, so a steady timer flags at a steady pace.
func (t *Timer) Move(now time.Time) (ahead time.Duration, flagged bool) {
	if !t.started {
		t.last, t.started = now, true
		return 0, false
	}
	t.balance += TickDuration - now.Sub(t.last)
	t.last = now
	if t.balance < -TimerMaxBank {
		t.balance = -TimerMaxBank
	}
	if t.balance > TimerTolerance {
		ahead = t.balance
		t.balance = 0
		return ahead, true
	}
	return t.balance, false
}

// Credit forgives one packet the protocol itself adds: the vanilla client
// answers every teleport with a Confirm Teleportation and an extra
// position packet in the same tick.
func (t *Timer) Credit() {
	t.balance -= TickDuration
}
