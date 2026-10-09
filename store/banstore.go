package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"minecraft-server/ban"
	"minecraft-server/protocol"
)

// BanStore adapts the players + bans tables to the ban.Store contract the
// server's login handler and /ban command speak. It's the STORAGE=postgres
// backend; ban.MemoryStore / ban.FileStore cover STORAGE=memory.
//
// Bans are keyed by player row, so banning a name nobody has joined under
// yet creates the player record with its offline-mode UUID — the same key
// /register uses, so the row is reused when they first connect.
type BanStore struct {
	players *PlayerRepo
	bans    *BanRepo
}

// NewBanStore wires the adapter over the two repos.
func NewBanStore(players *PlayerRepo, bans *BanRepo) *BanStore {
	return &BanStore{players: players, bans: bans}
}

// permanentExpiry stands in for a NULL expires_at (permanent ban) so the
// ban.Info the login handler formats always carries a real time.
var permanentExpiry = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// IsBanned implements ban.Store. A name with no player row isn't banned.
func (s *BanStore) IsBanned(ctx context.Context, playerName string) (*ban.Info, error) {
	p, err := s.players.GetByUsername(ctx, playerName)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("looking up player %q: %w", playerName, err)
	}
	b, err := s.bans.ActiveForPlayer(ctx, p.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("looking up ban for %q: %w", playerName, err)
	}
	info := &ban.Info{
		PlayerName: p.Username,
		Reason:     b.Reason,
		IssuedBy:   b.IssuedBy,
		BannedAt:   b.CreatedAt,
		ExpiresAt:  permanentExpiry,
	}
	if b.ExpiresAt != nil {
		info.ExpiresAt = *b.ExpiresAt
	}
	return info, nil
}

// Add implements ban.Store. Any earlier active ban is deactivated first so
// ActiveForPlayer's "latest active" is the one just issued.
func (s *BanStore) Add(ctx context.Context, playerName, reason, issuedBy string, expiresAt time.Time) error {
	p, err := s.resolve(ctx, playerName)
	if err != nil {
		return err
	}
	if _, err := s.bans.Deactivate(ctx, p.ID); err != nil {
		return fmt.Errorf("clearing previous bans for %q: %w", playerName, err)
	}
	var exp *time.Time
	if !expiresAt.IsZero() && expiresAt.Before(permanentExpiry) {
		exp = &expiresAt
	}
	if _, err := s.bans.Create(ctx, p.ID, reason, issuedBy, exp); err != nil {
		return fmt.Errorf("creating ban for %q: %w", playerName, err)
	}
	return nil
}

// Remove implements ban.Store. Unknown names are a no-op.
func (s *BanStore) Remove(ctx context.Context, playerName string) error {
	p, err := s.players.GetByUsername(ctx, playerName)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("looking up player %q: %w", playerName, err)
	}
	if _, err := s.bans.Deactivate(ctx, p.ID); err != nil {
		return fmt.Errorf("lifting ban for %q: %w", playerName, err)
	}
	return nil
}

// resolve finds the player by name, creating the row (offline UUID) when
// they've never been seen.
func (s *BanStore) resolve(ctx context.Context, playerName string) (Player, error) {
	p, err := s.players.GetByUsername(ctx, playerName)
	if err == nil {
		return p, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Player{}, fmt.Errorf("looking up player %q: %w", playerName, err)
	}
	p, err = s.players.Upsert(ctx, protocol.OfflineUUID(playerName), playerName)
	if err != nil {
		return Player{}, fmt.Errorf("creating player %q: %w", playerName, err)
	}
	return p, nil
}

var _ ban.Store = (*BanStore)(nil)
