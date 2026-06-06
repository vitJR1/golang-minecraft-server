package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FFAMatchRepo manages ffa_matches rows.
type FFAMatchRepo struct{ db *pgxpool.Pool }

func NewFFAMatchRepo(db *pgxpool.Pool) *FFAMatchRepo { return &FFAMatchRepo{db: db} }

const ffaMatchCols = `id, map, started_at, ended_at, winner_player_id`

func scanFFAMatch(row pgx.Row) (FFAMatch, error) {
	var m FFAMatch
	err := row.Scan(&m.ID, &m.Map, &m.StartedAt, &m.EndedAt, &m.WinnerPlayerID)
	return m, notFound(err)
}

// Create opens a new match. Returns the row.
func (r *FFAMatchRepo) Create(ctx context.Context, mapName string) (FFAMatch, error) {
	return scanFFAMatch(r.db.QueryRow(ctx,
		`INSERT INTO ffa_matches (map) VALUES ($1) RETURNING `+ffaMatchCols, mapName))
}

// Finish stamps ended_at and records the winner (nil = no winner).
func (r *FFAMatchRepo) Finish(ctx context.Context, matchID int64, winnerPlayerID *int64) error {
	_, err := r.db.Exec(ctx,
		`UPDATE ffa_matches SET ended_at = now(), winner_player_id = $2 WHERE id = $1`,
		matchID, winnerPlayerID)
	return err
}

// Get returns a match by id, or ErrNotFound.
func (r *FFAMatchRepo) Get(ctx context.Context, matchID int64) (FFAMatch, error) {
	return scanFFAMatch(r.db.QueryRow(ctx,
		`SELECT `+ffaMatchCols+` FROM ffa_matches WHERE id = $1`, matchID))
}

// FFAPlayerRepo manages ffa_players (per-match results).
type FFAPlayerRepo struct{ db *pgxpool.Pool }

func NewFFAPlayerRepo(db *pgxpool.Pool) *FFAPlayerRepo { return &FFAPlayerRepo{db: db} }

const ffaPlayerCols = `id, match_id, player_id, kills, deaths, won`

func scanFFAPlayer(row pgx.Row) (FFAPlayer, error) {
	var p FFAPlayer
	err := row.Scan(&p.ID, &p.MatchID, &p.PlayerID, &p.Kills, &p.Deaths, &p.Won)
	return p, notFound(err)
}

// Upsert writes one player's result for a match, replacing any existing row
// for the (match, player) pair. Returns the stored row.
func (r *FFAPlayerRepo) Upsert(ctx context.Context, p FFAPlayer) (FFAPlayer, error) {
	const q = `
		INSERT INTO ffa_players (match_id, player_id, kills, deaths, won)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (match_id, player_id) DO UPDATE SET
			kills = EXCLUDED.kills, deaths = EXCLUDED.deaths, won = EXCLUDED.won
		RETURNING ` + ffaPlayerCols
	return scanFFAPlayer(r.db.QueryRow(ctx, q, p.MatchID, p.PlayerID, p.Kills, p.Deaths, p.Won))
}

// ListByMatch returns every player's result for a match.
func (r *FFAPlayerRepo) ListByMatch(ctx context.Context, matchID int64) ([]FFAPlayer, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+ffaPlayerCols+` FROM ffa_players WHERE match_id = $1`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FFAPlayer
	for rows.Next() {
		p, err := scanFFAPlayer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
