// Package auth is the connection-independent half of the offline-mode
// AuthMe-style gate: password hashing, the credential stores (Postgres
// players.password_hash or an in-memory fallback), and the per-IP
// wrong-password counter + IP bans persisted to a JSON sidecar file.
// The server package owns the rest (the auth instance, the /register
// /login /banip /unbanip commands, the timeout kick and its XP-bar
// countdown) and talks to this package through Service.
package auth

import (
	"context"
	"errors"

	"golang.org/x/crypto/bcrypt"

	"minecraft-server/protocol"
)

// HashCost is the bcrypt cost of new hashes — 10 yields ~100ms per hash on
// a modern laptop, comfortable for a register/login gate that fires at most
// once per connection. Bump to 12 if you ever care about offline
// brute-force resistance more than register latency. Tests lower it
// (bcrypt.MinCost): under the race detector and a full parallel test run a
// cost-10 hash took seconds and timed the auth tests out. Verification
// reads the cost from the hash itself.
var HashCost = 10

// MinPasswordLen is the shortest password /register accepts.
const MinPasswordLen = 4

var (
	// ErrRegistered: the name already has a password (use /login).
	ErrRegistered = errors.New("name already registered")
	// ErrTooShort: the password is shorter than MinPasswordLen.
	ErrTooShort = errors.New("password too short")
	// ErrNoAccount: the name has no password yet (use /register).
	ErrNoAccount = errors.New("no account")
	// ErrWrongPassword: the password doesn't match the stored hash.
	ErrWrongPassword = errors.New("wrong password")
)

// HashPassword wraps bcrypt.GenerateFromPassword and returns the result as
// a string ready for storage. Errors propagate so the caller can show a
// meaningful message — generation only fails on bogus cost values, which
// shouldn't happen with our hardcoded const.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), HashCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CheckPassword reports whether password matches a HashPassword hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Service ties the credential store to the IP guard.
type Service struct {
	Creds Credentials
	IPs   *IPGuard
}

// Registered reports whether name has a stored password. Resolves the
// player's offline-mode UUID from the name (auth is offline-mode only) to key
// the lookup. A storage error is treated as "no account" — the player is asked
// to register, and the real error surfaces (and is logged) on the actual
// attempt.
func (s *Service) Registered(name string) bool {
	_, ok, err := s.Creds.Hash(context.Background(), protocol.OfflineUUID(name), name)
	return err == nil && ok
}

// Register stores a new password for name: ErrTooShort, ErrRegistered, or a
// hashing / storage error.
func (s *Service) Register(ctx context.Context, name, password string) error {
	if len(password) < MinPasswordLen {
		return ErrTooShort
	}
	if s.Registered(name) {
		return ErrRegistered
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return s.Creds.Set(ctx, protocol.OfflineUUID(name), name, hash)
}

// Verify checks name's password: nil on a match, ErrNoAccount,
// ErrWrongPassword, or a storage error.
func (s *Service) Verify(ctx context.Context, name, password string) error {
	hash, ok, err := s.Creds.Hash(ctx, protocol.OfflineUUID(name), name)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoAccount
	}
	if !CheckPassword(hash, password) {
		return ErrWrongPassword
	}
	return nil
}
