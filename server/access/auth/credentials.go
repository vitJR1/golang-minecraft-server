package auth

import (
	"context"
	"strings"
	"sync"

	"minecraft-server/store"
)

// Credentials is where /register and /login persist bcrypt password hashes.
// uuid is the stable per-player key (the offline-mode UUID, which is also the
// players.uuid column); name is carried so the DB impl can populate / refresh
// the players.username column. The production impl writes to the players
// table; the in-memory impl backs tests and the no-database fallback.
type Credentials interface {
	// Hash returns the player's stored bcrypt hash and whether one exists.
	Hash(ctx context.Context, uuid, name string) (string, bool, error)
	// Set stores hash for the player, creating the player record if needed.
	Set(ctx context.Context, uuid, name, hash string) error
}

// DBCredentials keeps credentials in the players table (password_hash column).
type DBCredentials struct{ Players *store.PlayerRepo }

// Hash looks the name up case-insensitively (store.PlayerRepo.
// PasswordHashByName), like MemCredentials: keyed by the exact-case UUID
// alone, "admin" could register beside op "Admin" and pass OpSet.Has, which
// ignores case.
func (d DBCredentials) Hash(ctx context.Context, uuid, name string) (string, bool, error) {
	return d.Players.PasswordHashByName(ctx, uuid, name)
}

func (d DBCredentials) Set(ctx context.Context, uuid, name, hash string) error {
	p, err := d.Players.Upsert(ctx, uuid, name)
	if err != nil {
		return err
	}
	return d.Players.SetPassword(ctx, p.ID, hash)
}

// MemCredentials is the non-persistent fallback used when no database is
// configured (and by the unit tests). Keyed by lower-cased name.
type MemCredentials struct {
	mu sync.RWMutex
	m  map[string]string
}

// NewMemCredentials returns an empty in-memory store.
func NewMemCredentials() *MemCredentials { return &MemCredentials{m: map[string]string{}} }

func (s *MemCredentials) Hash(_ context.Context, _, name string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.m[strings.ToLower(name)]
	return h, ok, nil
}

func (s *MemCredentials) Set(_ context.Context, _, name, hash string) error {
	s.mu.Lock()
	s.m[strings.ToLower(name)] = hash
	s.mu.Unlock()
	return nil
}
