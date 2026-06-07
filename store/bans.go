package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BanRepo is the bans table's repository.
type BanRepo struct{ db *pgxpool.Pool }

func NewBanRepo(db *pgxpool.Pool) *BanRepo { return &BanRepo{db: db} }

const banCols = `id, player_id, reason, issued_by, created_at, expires_at, active`

func scanBan(row pgx.Row) (Ban, error) {
	var b Ban
	err := row.Scan(&b.ID, &b.PlayerID, &b.Reason, &b.IssuedBy, &b.CreatedAt, &b.ExpiresAt, &b.Active)
	return b, notFound(err)
}

// Create issues a ban. expiresAt nil = permanent. Returns the stored row.
func (r *BanRepo) Create(ctx context.Context, playerID int64, reason, issuedBy string, expiresAt *time.Time) (Ban, error) {
	const q = `
		INSERT INTO bans (player_id, reason, issued_by, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING ` + banCols
	return scanBan(r.db.QueryRow(ctx, q, playerID, reason, issuedBy, expiresAt))
}

// ActiveForPlayer returns the player's in-force ban (active flag set and not
// past its expiry), or ErrNotFound if they aren't banned.
func (r *BanRepo) ActiveForPlayer(ctx context.Context, playerID int64) (Ban, error) {
	const q = `
		SELECT ` + banCols + ` FROM bans
		WHERE player_id = $1 AND active
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY created_at DESC LIMIT 1`
	return scanBan(r.db.QueryRow(ctx, q, playerID))
}

// Deactivate lifts every active ban on a player (an unban). Returns the number
// of bans cleared.
func (r *BanRepo) Deactivate(ctx context.Context, playerID int64) (int64, error) {
	tag, err := r.db.Exec(ctx,
		`UPDATE bans SET active = FALSE WHERE player_id = $1 AND active`, playerID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
