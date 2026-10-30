// Package playerlist is a concurrent set of players keyed by entity id, with
// lookups by name and the UUIDs — an instance's player list. It is generic
// over what a member is (the server stores its connections); a member's name
// and UUID are recorded when it is added (they never change).
package playerlist

import "sync"

type entry[M any] struct {
	m    M
	name string
	uuid [16]byte
}

// List is the set. The zero value is ready.
type List[M any] struct {
	mu sync.RWMutex
	// by holds members keyed by entity id. Entity ids are unique per
	// process, so this is collision-free.
	by map[int32]entry[M]
}

// Add registers a member; a later Add of the same entity id replaces it.
func (l *List[M]) Add(entityID int32, name string, uuid [16]byte, m M) {
	l.mu.Lock()
	if l.by == nil {
		l.by = map[int32]entry[M]{}
	}
	l.by[entityID] = entry[M]{m: m, name: name, uuid: uuid}
	l.mu.Unlock()
}

// Remove drops a member. No-op if absent.
func (l *List[M]) Remove(entityID int32) {
	l.mu.Lock()
	delete(l.by, entityID)
	l.mu.Unlock()
}

// Get is the member with entityID.
func (l *List[M]) Get(entityID int32) (M, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	e, ok := l.by[entityID]
	return e.m, ok
}

// ByName is a linear scan — fine for small player counts. Switch to a second
// index if we ever care about lookup performance at >100 players.
func (l *List[M]) ByName(name string) (M, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, e := range l.by {
		if e.name == name {
			return e.m, true
		}
	}
	var zero M
	return zero, false
}

// Count is the number of members.
func (l *List[M]) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.by)
}

// UUIDs returns every member's UUID (a departing client is told which
// tab-list entries to clear).
func (l *List[M]) UUIDs() [][16]byte {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([][16]byte, 0, len(l.by))
	for _, e := range l.by {
		out = append(out, e.uuid)
	}
	return out
}

// Snapshot returns a copy of the members so callers can iterate without
// holding the lock. Order is non-deterministic.
func (l *List[M]) Snapshot() []M {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]M, 0, len(l.by))
	for _, e := range l.by {
		out = append(out, e.m)
	}
	return out
}

// Range visits every member without the lock held, so callbacks may take
// longer-running actions (network writes, further lookups). Order is
// non-deterministic.
func (l *List[M]) Range(fn func(m M)) {
	for _, m := range l.Snapshot() {
		fn(m)
	}
}
