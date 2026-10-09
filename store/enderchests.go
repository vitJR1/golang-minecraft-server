package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnderChestRepo persists per-player ender chest contents (ender_chests
// table). The slots payload is opaque JSON produced by the server's chest
// model; the repo only stores and returns it. Satisfies server.EnderChestStore.
type EnderChestRepo struct{ db *pgxpool.Pool }

func NewEnderChestRepo(db *pgxpool.Pool) *EnderChestRepo { return &EnderChestRepo{db: db} }

// LoadEnderChest returns the stored slots JSON for uuid, or nil when the
// player has never saved an ender chest.
func (r *EnderChestRepo) LoadEnderChest(ctx context.Context, uuid string) ([]byte, error) {
	var data []byte
	err := r.db.QueryRow(ctx,
		`SELECT slots FROM ender_chests WHERE uuid = $1::uuid`, uuid).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

// SaveEnderChest upserts the slots JSON for uuid.
func (r *EnderChestRepo) SaveEnderChest(ctx context.Context, uuid string, data []byte) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO ender_chests (uuid, slots) VALUES ($1::uuid, $2::jsonb)
		ON CONFLICT (uuid) DO UPDATE SET slots = EXCLUDED.slots, updated_at = now()`,
		uuid, data)
	return err
}
