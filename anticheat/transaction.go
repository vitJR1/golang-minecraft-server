package anticheat

import (
	"errors"
	"sync"
	"time"
)

// MaxOutstanding is how many Pings may await their Pong. At one per tick
// that is 10 s — far beyond a real connection's lag (keep-alive kicks at
// 30 s of silence anyway). A client that answers keep-alives but sits on
// Pings is hiding which server state it has seen, which is what lag
// compensation and velocity checks rely on.
const MaxOutstanding = 200

var (
	// ErrUnknownTransaction: a Pong for an id the server never sent.
	ErrUnknownTransaction = errors.New("pong for an unknown transaction")
	// ErrTransactionOrder: a Pong that skips an older outstanding Ping. The
	// vanilla client answers Pings in arrival order on its main thread.
	ErrTransactionOrder = errors.New("pong out of order")
)

// Txn is one Ping the server sent.
type Txn struct {
	ID    int32
	Tick  uint64 // instance tick the Ping was sent on
	Epoch uint32 // instance epoch (see Transactions.NewEpoch)
	Sent  time.Time

	onConfirm func()
}

// Transactions numbers server events with Ping (Cb 0x32) / Pong (Sb 0x20).
// The client handles packets in order and answers a Ping immediately, so its
// Pong for id N proves it has processed everything the server sent before
// Ping N — the server learns which tick of its own state the client was
// looking at, and when an event (knockback, teleport, effect) took effect
// client-side.
type Transactions struct {
	mu        sync.Mutex
	next      int32
	epoch     uint32
	pending   []Txn // oldest first
	confirmed Txn
	hasConf   bool
	rtt       time.Duration // smoothed round-trip time
}

// Begin registers a Ping sent at instance tick, returning the id to put on
// the wire. onConfirm (may be nil) runs when the matching Pong arrives. ok
// is false when MaxOutstanding Pings are already unanswered; the caller
// should drop the client rather than send more.
func (t *Transactions) Begin(tick uint64, now time.Time, onConfirm func()) (id int32, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.pending) >= MaxOutstanding {
		return 0, false
	}
	t.next++
	t.pending = append(t.pending, Txn{ID: t.next, Tick: tick, Epoch: t.epoch, Sent: now, onConfirm: onConfirm})
	return t.next, true
}

// Confirm handles a Pong. It returns the callback of the confirmed Ping (nil
// if none) for the caller to run outside the lock, or an error for a Pong
// that is unknown or skips an older Ping.
func (t *Transactions) Confirm(id int32, now time.Time) (func(), error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.pending) == 0 {
		return nil, ErrUnknownTransaction
	}
	if head := t.pending[0]; head.ID != id {
		for _, p := range t.pending[1:] {
			if p.ID == id {
				return nil, ErrTransactionOrder
			}
		}
		return nil, ErrUnknownTransaction
	}
	txn := t.pending[0]
	t.pending = t.pending[1:]
	t.confirmed, t.hasConf = txn, true

	// Exponential moving average, 1/8 weight like TCP's SRTT.
	sample := now.Sub(txn.Sent)
	if t.rtt == 0 {
		t.rtt = sample
	} else {
		t.rtt += (sample - t.rtt) / 8
	}
	return txn.onConfirm, nil
}

// LastConfirmedTick is the instance tick of the newest Ping the client has
// answered in the current epoch: everything the server sent up to that tick
// is known to be on the client's screen. ok is false before the first Pong
// of the epoch.
func (t *Transactions) LastConfirmedTick() (tick uint64, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.hasConf || t.confirmed.Epoch != t.epoch {
		return 0, false
	}
	return t.confirmed.Tick, true
}

// RTT is the smoothed Ping→Pong round-trip time (0 before the first Pong).
func (t *Transactions) RTT() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rtt
}

// Outstanding is the number of Pings still awaiting their Pong.
func (t *Transactions) Outstanding() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}

// NewEpoch starts a new tick timeline (the player moved to another instance).
// Pings already in flight are still confirmed in order, but their ticks
// belong to the old instance and no longer count for LastConfirmedTick.
func (t *Transactions) NewEpoch() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.epoch++
}
