package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlayerRepo is the identity table's repository.
type PlayerRepo struct{ db *pgxpool.Pool }

func NewPlayerRepo(db *pgxpool.Pool) *PlayerRepo { return &PlayerRepo{db: db} }

const playerCols = `id, uuid::text, username, first_seen, last_seen`

func scanPlayer(row pgx.Row) (Player, error) {
	var p Player
	err := row.Scan(&p.ID, &p.UUID, &p.Username, &p.FirstSeen, &p.LastSeen)
	return p, notFound(err)
}

// Upsert records a player seen on the server: inserts a new row keyed by UUID,
// or refreshes the username and last_seen if the UUID already exists. Returns
// the resulting row (with its id), the call you make on every join.
func (r *PlayerRepo) Upsert(ctx context.Context, uuid, username string) (Player, error) {
	const q = `
		INSERT INTO players (uuid, username)
		VALUES ($1::uuid, $2)
		ON CONFLICT (uuid) DO UPDATE
			SET username = EXCLUDED.username, last_seen = now()
		RETURNING ` + playerCols
	return scanPlayer(r.db.QueryRow(ctx, q, uuid, username))
}

// GetByUUID looks up a player by UUID. Returns ErrNotFound if absent.
func (r *PlayerRepo) GetByUUID(ctx context.Context, uuid string) (Player, error) {
	return scanPlayer(r.db.QueryRow(ctx,
		`SELECT `+playerCols+` FROM players WHERE uuid = $1::uuid`, uuid))
}

// GetByID looks up a player by primary key. Returns ErrNotFound if absent.
func (r *PlayerRepo) GetByID(ctx context.Context, id int64) (Player, error) {
	return scanPlayer(r.db.QueryRow(ctx,
		`SELECT `+playerCols+` FROM players WHERE id = $1`, id))
}

// GetByUsername looks up the most-recently-seen player with this (case
// insensitive) name. Names aren't unique over time, so this picks the latest.
func (r *PlayerRepo) GetByUsername(ctx context.Context, username string) (Player, error) {
	return scanPlayer(r.db.QueryRow(ctx,
		`SELECT `+playerCols+` FROM players
		 WHERE lower(username) = lower($1)
		 ORDER BY last_seen DESC LIMIT 1`, username))
}

// SetPassword stores the player's password hash (e.g. bcrypt). Pass the
// already-hashed value — never plaintext; hashing/verification stay with the
// caller (see server/auth.go). An empty hash clears the password. Returns
// ErrNotFound if the player doesn't exist. The hash is deliberately kept out
// of the Player struct and the standard SELECTs so general reads can't leak it.
func (r *PlayerRepo) SetPassword(ctx context.Context, playerID int64, hash string) error {
	var val *string
	if hash != "" {
		val = &hash
	}
	tag, err := r.db.Exec(ctx, `UPDATE players SET password_hash = $2 WHERE id = $1`, playerID, val)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PasswordHash returns the player's stored hash and whether one is set. A
// NULL/empty column yields ok=false (player exists but has no password);
// a missing player returns ErrNotFound.
func (r *PlayerRepo) PasswordHash(ctx context.Context, playerID int64) (hash string, ok bool, err error) {
	var h *string
	if err := r.db.QueryRow(ctx,
		`SELECT password_hash FROM players WHERE id = $1`, playerID).Scan(&h); err != nil {
		return "", false, notFound(err)
	}
	if h == nil || *h == "" {
		return "", false, nil
	}
	return *h, true, nil
}
