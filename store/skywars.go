package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SkywarsMatchRepo manages skywars_matches rows.
type SkywarsMatchRepo struct{ db *pgxpool.Pool }

func NewSkywarsMatchRepo(db *pgxpool.Pool) *SkywarsMatchRepo { return &SkywarsMatchRepo{db: db} }

const skywarsMatchCols = `id, map, started_at, ended_at, winner_player_id`

func scanSkywarsMatch(row pgx.Row) (SkywarsMatch, error) {
	var m SkywarsMatch
	err := row.Scan(&m.ID, &m.Map, &m.StartedAt, &m.EndedAt, &m.WinnerPlayerID)
	return m, notFound(err)
}

// Create opens a new match. Returns the row.
func (r *SkywarsMatchRepo) Create(ctx context.Context, mapName string) (SkywarsMatch, error) {
	return scanSkywarsMatch(r.db.QueryRow(ctx,
		`INSERT INTO skywars_matches (map) VALUES ($1) RETURNING `+skywarsMatchCols, mapName))
}

// Finish stamps ended_at and records the winner (nil = no winner).
func (r *SkywarsMatchRepo) Finish(ctx context.Context, matchID int64, winnerPlayerID *int64) error {
	_, err := r.db.Exec(ctx,
		`UPDATE skywars_matches SET ended_at = now(), winner_player_id = $2 WHERE id = $1`,
		matchID, winnerPlayerID)
	return err
}

// Get returns a match by id, or ErrNotFound.
func (r *SkywarsMatchRepo) Get(ctx context.Context, matchID int64) (SkywarsMatch, error) {
	return scanSkywarsMatch(r.db.QueryRow(ctx,
		`SELECT `+skywarsMatchCols+` FROM skywars_matches WHERE id = $1`, matchID))
}

// SkywarsPlayerRepo manages skywars_players (per-match results).
type SkywarsPlayerRepo struct{ db *pgxpool.Pool }

func NewSkywarsPlayerRepo(db *pgxpool.Pool) *SkywarsPlayerRepo { return &SkywarsPlayerRepo{db: db} }

const skywarsPlayerCols = `id, match_id, player_id, kills, deaths, won`

func scanSkywarsPlayer(row pgx.Row) (SkywarsPlayer, error) {
	var p SkywarsPlayer
	err := row.Scan(&p.ID, &p.MatchID, &p.PlayerID, &p.Kills, &p.Deaths, &p.Won)
	return p, notFound(err)
}

// Upsert writes one player's result for a match, replacing any existing row
// for the (match, player) pair. Returns the stored row.
func (r *SkywarsPlayerRepo) Upsert(ctx context.Context, p SkywarsPlayer) (SkywarsPlayer, error) {
	const q = `
		INSERT INTO skywars_players (match_id, player_id, kills, deaths, won)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (match_id, player_id) DO UPDATE SET
			kills = EXCLUDED.kills, deaths = EXCLUDED.deaths, won = EXCLUDED.won
		RETURNING ` + skywarsPlayerCols
	return scanSkywarsPlayer(r.db.QueryRow(ctx, q, p.MatchID, p.PlayerID, p.Kills, p.Deaths, p.Won))
}

// ListByMatch returns every player's result for a match.
func (r *SkywarsPlayerRepo) ListByMatch(ctx context.Context, matchID int64) ([]SkywarsPlayer, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+skywarsPlayerCols+` FROM skywars_players WHERE match_id = $1`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SkywarsPlayer
	for rows.Next() {
		p, err := scanSkywarsPlayer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
