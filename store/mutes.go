package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MuteRepo is the mutes table's repository. Mirrors BanRepo.
type MuteRepo struct{ db *pgxpool.Pool }

func NewMuteRepo(db *pgxpool.Pool) *MuteRepo { return &MuteRepo{db: db} }

const muteCols = `id, player_id, reason, issued_by, created_at, expires_at, active`

func scanMute(row pgx.Row) (Mute, error) {
	var m Mute
	err := row.Scan(&m.ID, &m.PlayerID, &m.Reason, &m.IssuedBy, &m.CreatedAt, &m.ExpiresAt, &m.Active)
	return m, notFound(err)
}

// Create issues a mute. expiresAt nil = permanent. Returns the stored row.
func (r *MuteRepo) Create(ctx context.Context, playerID int64, reason, issuedBy string, expiresAt *time.Time) (Mute, error) {
	const q = `
		INSERT INTO mutes (player_id, reason, issued_by, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING ` + muteCols
	return scanMute(r.db.QueryRow(ctx, q, playerID, reason, issuedBy, expiresAt))
}

// ActiveForPlayer returns the player's in-force mute, or ErrNotFound.
func (r *MuteRepo) ActiveForPlayer(ctx context.Context, playerID int64) (Mute, error) {
	const q = `
		SELECT ` + muteCols + ` FROM mutes
		WHERE player_id = $1 AND active
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY created_at DESC LIMIT 1`
	return scanMute(r.db.QueryRow(ctx, q, playerID))
}

// Deactivate lifts every active mute on a player (an unmute). Returns the
// number of mutes cleared.
func (r *MuteRepo) Deactivate(ctx context.Context, playerID int64) (int64, error) {
	tag, err := r.db.Exec(ctx,
		`UPDATE mutes SET active = FALSE WHERE player_id = $1 AND active`, playerID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
