// Package arena tracks the arenas created at runtime (/arena create): name
// → game kind, and the auto names ("bw-1", "bw-1x1-2", …). Building an
// arena and its instance is the server's job.
package arena

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry is the created arenas. Safe for concurrent use; the zero value
// is ready.
type Registry struct {
	mu     sync.RWMutex
	kinds  map[string]string // arena name → kind
	serial atomic.Uint64
}

// Add records name as an arena of kind.
func (r *Registry) Add(name, kind string) {
	r.mu.Lock()
	if r.kinds == nil {
		r.kinds = make(map[string]string)
	}
	r.kinds[name] = kind
	r.mu.Unlock()
}

// Remove forgets name (no-op when it isn't an arena).
func (r *Registry) Remove(name string) {
	r.mu.Lock()
	delete(r.kinds, name)
	r.mu.Unlock()
}

// Is reports whether name is a created arena.
func (r *Registry) Is(name string) bool {
	r.mu.RLock()
	_, ok := r.kinds[name]
	r.mu.RUnlock()
	return ok
}

// OfKind returns the arenas of kind, sorted; kind "" returns every arena.
func (r *Registry) OfKind(kind string) []string {
	r.mu.RLock()
	var out []string
	for name, k := range r.kinds {
		if kind == "" || k == kind {
			out = append(out, name)
		}
	}
	r.mu.RUnlock()
	sort.Strings(out)
	return out
}

// NextName picks the next auto name for kind that taken doesn't report as
// in use: "<prefix>-<n>" with a serial shared by every kind.
func (r *Registry) NextName(kind string, taken func(string) bool) string {
	prefix := Prefix(kind)
	for {
		name := fmt.Sprintf("%s-%d", prefix, r.serial.Add(1))
		if !taken(name) {
			return name
		}
	}
}

// Prefix is the auto-name prefix of a kind: bedwars kinds shorten to "bw"
// ("bedwars" → "bw", "bedwars-1x1" → "bw-1x1"), others keep their name.
func Prefix(kind string) string {
	if strings.HasPrefix(kind, "bedwars") {
		return "bw" + strings.TrimPrefix(kind, "bedwars")
	}
	return kind
}
