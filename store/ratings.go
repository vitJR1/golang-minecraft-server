package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RatingRepo is the per-mode ELO ratings repository. One instance is bound to
// one ratings table (bedwars_ratings / skywars_ratings / ffa_ratings); the
// table name comes from trusted constants in Store.New, never user input, so
// interpolating it into the SQL is safe. Rank is computed at read time from
// the rating column via a window function — never stored.
type RatingRepo struct {
	db    *pgxpool.Pool
	table string
}

// NewRatingRepo binds a repo to a ratings table. table must be a trusted
// constant.
func NewRatingRepo(db *pgxpool.Pool, table string) *RatingRepo {
	return &RatingRepo{db: db, table: table}
}

// DefaultRating is the starting ELO a player gets on their first rated game.
const DefaultRating = 1000

// Get returns a player's rating row, or ErrNotFound if they have none yet.
func (r *RatingRepo) Get(ctx context.Context, playerID int64) (Rating, error) {
	q := fmt.Sprintf(
		`SELECT player_id, rating, games, updated_at FROM %s WHERE player_id = $1`, r.table)
	var rt Rating
	err := r.db.QueryRow(ctx, q, playerID).Scan(&rt.PlayerID, &rt.Rating, &rt.Games, &rt.UpdatedAt)
	return rt, notFound(err)
}

// ApplyResult adjusts a player's rating by delta and bumps their games count
// by one, creating the row (starting from DefaultRating) on first use. Returns
// the updated rating. Call once per finished game with the computed ELO delta.
func (r *RatingRepo) ApplyResult(ctx context.Context, playerID int64, delta int) (Rating, error) {
	q := fmt.Sprintf(`
		INSERT INTO %[1]s (player_id, rating, games)
		VALUES ($1, %[2]d + $2, 1)
		ON CONFLICT (player_id) DO UPDATE
			SET rating = %[1]s.rating + $2,
			    games  = %[1]s.games + 1,
			    updated_at = now()
		RETURNING player_id, rating, games, updated_at`, r.table, DefaultRating)
	var rt Rating
	err := r.db.QueryRow(ctx, q, playerID, delta).Scan(&rt.PlayerID, &rt.Rating, &rt.Games, &rt.UpdatedAt)
	return rt, err
}

// Leaderboard returns the top players by rating, each with its 1-based rank
// (highest rating = rank 1; ties share a rank). limit <= 0 returns all rows.
func (r *RatingRepo) Leaderboard(ctx context.Context, limit int) ([]RankedPlayer, error) {
	q := fmt.Sprintf(`
		SELECT t.player_id, p.username, t.rating, t.games,
		       RANK() OVER (ORDER BY t.rating DESC)::int AS rank
		FROM %s t
		JOIN players p ON p.id = t.player_id
		ORDER BY t.rating DESC`, r.table)
	args := []any{}
	if limit > 0 {
		q += ` LIMIT $1`
		args = append(args, limit)
	}
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectRanked(rows)
}

// RankOf returns one player's leaderboard position in this mode (rank computed
// across every rated player), or ErrNotFound if the player has no rating.
func (r *RatingRepo) RankOf(ctx context.Context, playerID int64) (RankedPlayer, error) {
	q := fmt.Sprintf(`
		SELECT player_id, username, rating, games, rank FROM (
			SELECT t.player_id, p.username, t.rating, t.games,
			       RANK() OVER (ORDER BY t.rating DESC)::int AS rank
			FROM %s t
			JOIN players p ON p.id = t.player_id
		) ranked
		WHERE player_id = $1`, r.table)
	var rp RankedPlayer
	err := r.db.QueryRow(ctx, q, playerID).
		Scan(&rp.PlayerID, &rp.Username, &rp.Rating, &rp.Games, &rp.Rank)
	return rp, notFound(err)
}

func collectRanked(rows pgx.Rows) ([]RankedPlayer, error) {
	var out []RankedPlayer
	for rows.Next() {
		var rp RankedPlayer
		if err := rows.Scan(&rp.PlayerID, &rp.Username, &rp.Rating, &rp.Games, &rp.Rank); err != nil {
			return nil, err
		}
		out = append(out, rp)
	}
	return out, rows.Err()
}
