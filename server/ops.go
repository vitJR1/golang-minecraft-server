package server

import "sync"

// OpSet tracks which player names have operator privileges. Concurrent-safe
// via RWMutex; lookups are cheap (Has is the hot path for command perms).
//
// Storage is by Name (case-insensitive) for now. Real servers key on UUID
// to survive name changes; we'll switch when we care about that.
type OpSet struct {
	mu  sync.RWMutex
	ops map[string]struct{}
}

func NewOpSet(initial []string) *OpSet {
	s := &OpSet{ops: make(map[string]struct{}, len(initial))}
	for _, name := range initial {
		s.ops[normalizeOpName(name)] = struct{}{}
	}
	return s
}

func (s *OpSet) Has(name string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	_, ok := s.ops[normalizeOpName(name)]
	s.mu.RUnlock()
	return ok
}

func (s *OpSet) Add(name string) {
	s.mu.Lock()
	s.ops[normalizeOpName(name)] = struct{}{}
	s.mu.Unlock()
}

func (s *OpSet) Remove(name string) {
	s.mu.Lock()
	delete(s.ops, normalizeOpName(name))
	s.mu.Unlock()
}

func (s *OpSet) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.ops)
}

// normalizeOpName lowercases for case-insensitive matching. Minecraft names
// are technically case-preserving but lookups should be case-insensitive
// so "Notch" matches "/op notch".
func normalizeOpName(name string) string {
	// Cheap ASCII lowercase — MC names are limited to a-zA-Z0-9_.
	out := make([]byte, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
