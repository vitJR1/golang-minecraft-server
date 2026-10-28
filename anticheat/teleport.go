package anticheat

import "sync"

// maxPendingTeleports bounds the queue. A vanilla client confirms every
// teleport within a round-trip; one that never does just keeps the oldest
// entries dropped instead of growing the slice forever.
const maxPendingTeleports = 64

// Teleport is one server-initiated Synchronize Player Position.
type Teleport struct {
	ID      int32
	X, Y, Z float64
}

// Teleports tracks Synchronize Player Position packets until the client's
// Confirm Teleportation. Vanilla does the same (awaitingPositionFromClient):
// until the latest teleport is confirmed, movement packets were produced
// before the client saw it and must not move the player.
type Teleports struct {
	mu      sync.Mutex
	next    int32
	pending []Teleport // oldest first
}

// Begin registers a teleport to (x, y, z) and returns the id to put on the
// wire. Ids start at 1 and never repeat within a connection (until int32
// wraps, which takes years of teleports).
func (t *Teleports) Begin(x, y, z float64) int32 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.next++
	if t.next <= 0 {
		t.next = 1
	}
	if len(t.pending) >= maxPendingTeleports {
		t.pending = t.pending[1:]
	}
	t.pending = append(t.pending, Teleport{ID: t.next, X: x, Y: y, Z: z})
	return t.next
}

// Confirm handles a Confirm Teleportation. The client confirms teleports in
// the order it received them, so a confirm for id also settles every older
// one. ok is false for an id the server never sent (or already settled) —
// a forged packet.
func (t *Teleports) Confirm(id int32) (tp Teleport, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, p := range t.pending {
		if p.ID == id {
			t.pending = t.pending[i+1:]
			return p, true
		}
	}
	return Teleport{}, false
}

// Awaiting reports whether any teleport is still unconfirmed.
func (t *Teleports) Awaiting() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending) > 0
}
