// Package ban defines the player-ban storage contract and its non-database
// implementations. Which backend the server uses is picked in main from the
// STORAGE env var: Postgres (store.BanStore) when STORAGE=postgres, the
// file-backed or pure in-memory store from this package otherwise.
package ban

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Info describes one in-force ban as the login handler and /ban see it.
type Info struct {
	PlayerName string    `json:"playerName"`
	Reason     string    `json:"Reason"`
	IssuedBy   string    `json:"IssuedBy,omitempty"`
	BannedAt   time.Time `json:"BannedAt"`
	ExpiresAt  time.Time `json:"ExpiresAt"`
}

// Store is where player bans live. Implementations must be safe for
// concurrent use: IsBanned runs on every connection's login goroutine while
// /ban and /unban run on the moderator's.
type Store interface {
	// IsBanned returns the player's in-force ban (exists and not yet
	// expired) or nil. A non-nil error means the backend couldn't answer —
	// the caller decides whether that fails open or closed.
	IsBanned(ctx context.Context, playerName string) (*Info, error)
	// Add bans playerName until expiresAt, replacing any existing ban.
	Add(ctx context.Context, playerName, reason, issuedBy string, expiresAt time.Time) error
	// Remove lifts the player's ban. Not an error if they weren't banned.
	Remove(ctx context.Context, playerName string) error
}

// rawEntry is the on-disk JSON form, which uses "YYYY-MM-DD HH:MM:SS"
// time strings rather than RFC 3339.
type rawEntry struct {
	PlayerName string `json:"playerName"`
	Reason     string `json:"Reason"`
	IssuedBy   string `json:"IssuedBy,omitempty"`
	BannedAt   string `json:"BannedAt"`
	ExpiresAt  string `json:"ExpiresAt"`
}

const timeLayout = "2006-01-02 15:04:05"

// MemoryStore keeps bans in a process-local map. Nothing survives a restart.
// Used by tests and by STORAGE=memory with no banlist file configured.
type MemoryStore struct {
	mu   sync.RWMutex
	bans map[string]*Info
}

// NewMemoryStore returns an empty in-memory ban store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{bans: map[string]*Info{}}
}

// IsBanned implements Store. Expired entries are filtered at lookup.
func (s *MemoryStore) IsBanned(_ context.Context, playerName string) (*Info, error) {
	s.mu.RLock()
	info, ok := s.bans[playerName]
	s.mu.RUnlock()
	if !ok || time.Now().After(info.ExpiresAt) {
		return nil, nil
	}
	cp := *info
	return &cp, nil
}

// Add implements Store.
func (s *MemoryStore) Add(_ context.Context, playerName, reason, issuedBy string, expiresAt time.Time) error {
	s.mu.Lock()
	s.bans[playerName] = &Info{
		PlayerName: playerName,
		Reason:     reason,
		IssuedBy:   issuedBy,
		BannedAt:   time.Now(),
		ExpiresAt:  expiresAt,
	}
	s.mu.Unlock()
	return nil
}

// Remove implements Store.
func (s *MemoryStore) Remove(_ context.Context, playerName string) error {
	s.mu.Lock()
	delete(s.bans, playerName)
	s.mu.Unlock()
	return nil
}

// snapshot returns the entries in on-disk form.
func (s *MemoryStore) snapshot() []rawEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]rawEntry, 0, len(s.bans))
	for _, b := range s.bans {
		out = append(out, rawEntry{
			PlayerName: b.PlayerName,
			Reason:     b.Reason,
			IssuedBy:   b.IssuedBy,
			BannedAt:   b.BannedAt.Format(timeLayout),
			ExpiresAt:  b.ExpiresAt.Format(timeLayout),
		})
	}
	return out
}

// replace swaps the whole ban set.
func (s *MemoryStore) replace(next map[string]*Info) {
	s.mu.Lock()
	s.bans = next
	s.mu.Unlock()
}

// FileStore is a MemoryStore mirrored to a JSON file (banlist.json by
// convention): the file is read once at construction and rewritten after
// every Add/Remove, so bans survive restarts without a database.
type FileStore struct {
	mem  *MemoryStore
	path string
	// saveMu serialises Save calls so two concurrent /ban commands can't
	// race on the temp-file + rename.
	saveMu sync.Mutex
}

// NewFileStore loads path and returns a store that persists back to it.
// A missing file is not an error — the server just starts with no bans.
func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{mem: NewMemoryStore(), path: path}
	if err := s.Load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Path returns the backing file.
func (s *FileStore) Path() string { return s.path }

// Load re-reads the file and replaces the in-memory ban set. Safe to call
// again to pick up hand edits.
func (s *FileStore) Load() error {
	entries, err := ReadFile(s.path)
	if err != nil {
		return err
	}
	next := make(map[string]*Info, len(entries))
	for i := range entries {
		next[entries[i].PlayerName] = &entries[i]
	}
	s.mem.replace(next)
	return nil
}

// ReadFile parses a banlist.json. A missing file yields no entries and no
// error. Expired entries are returned as-is; filtering is the caller's job.
func ReadFile(path string) ([]Info, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read ban file: %w", err)
	}
	var raw []rawEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse ban file: %w", err)
	}
	out := make([]Info, 0, len(raw))
	for i, r := range raw {
		bannedAt, err := time.Parse(timeLayout, r.BannedAt)
		if err != nil {
			return nil, fmt.Errorf("entry %d (%s): BannedAt %q: %w", i, r.PlayerName, r.BannedAt, err)
		}
		expiresAt, err := time.Parse(timeLayout, r.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("entry %d (%s): ExpiresAt %q: %w", i, r.PlayerName, r.ExpiresAt, err)
		}
		out = append(out, Info{
			PlayerName: r.PlayerName,
			Reason:     r.Reason,
			IssuedBy:   r.IssuedBy,
			BannedAt:   bannedAt,
			ExpiresAt:  expiresAt,
		})
	}
	return out, nil
}

// ImportResult summarises an Import run.
type ImportResult struct {
	Added   int // entries written to dst
	Expired int // skipped: already past ExpiresAt
	Kept    int // skipped: dst already had an in-force ban for the name
}

// Import copies in-force entries into dst (typically the Postgres store when
// an operator switches STORAGE from memory to postgres). Expired entries are
// dropped, and a name dst already bans is left alone so a newer database
// ban is never clobbered by a stale file. Stops at the first backend error.
func Import(ctx context.Context, dst Store, entries []Info) (ImportResult, error) {
	var res ImportResult
	now := time.Now()
	for _, e := range entries {
		if !now.Before(e.ExpiresAt) {
			res.Expired++
			continue
		}
		existing, err := dst.IsBanned(ctx, e.PlayerName)
		if err != nil {
			return res, fmt.Errorf("checking %q: %w", e.PlayerName, err)
		}
		if existing != nil {
			res.Kept++
			continue
		}
		issuedBy := e.IssuedBy
		if issuedBy == "" {
			issuedBy = "banlist import"
		}
		if err := dst.Add(ctx, e.PlayerName, e.Reason, issuedBy, e.ExpiresAt); err != nil {
			return res, fmt.Errorf("importing %q: %w", e.PlayerName, err)
		}
		res.Added++
	}
	return res, nil
}

// Save writes the current ban set to the file in the same format Load
// expects. Atomic via temp-file + rename.
func (s *FileStore) Save() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	data, err := json.MarshalIndent(s.mem.snapshot(), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// IsBanned implements Store.
func (s *FileStore) IsBanned(ctx context.Context, playerName string) (*Info, error) {
	return s.mem.IsBanned(ctx, playerName)
}

// Add implements Store: updates memory, then persists.
func (s *FileStore) Add(ctx context.Context, playerName, reason, issuedBy string, expiresAt time.Time) error {
	if err := s.mem.Add(ctx, playerName, reason, issuedBy, expiresAt); err != nil {
		return err
	}
	return s.Save()
}

// Remove implements Store: updates memory, then persists.
func (s *FileStore) Remove(ctx context.Context, playerName string) error {
	if err := s.mem.Remove(ctx, playerName); err != nil {
		return err
	}
	return s.Save()
}
