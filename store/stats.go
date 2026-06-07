package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StatsRepo answers cross-mode aggregate questions by rolling up the per-match
// participation tables. This is where "how many kills does this player have in
// total" lives — summed across bedwars + skywars + ffa.
type StatsRepo struct{ db *pgxpool.Pool }

func NewStatsRepo(db *pgxpool.Pool) *StatsRepo { return &StatsRepo{db: db} }

// Totals is a player's career numbers summed over every mode.
type Totals struct {
	Kills  int
	Deaths int
	Wins   int
	Games  int
}

// TotalKills returns a player's all-time kill count across every mode — the
// "this user has 100 kills in total" number.
func (r *StatsRepo) TotalKills(ctx context.Context, playerID int64) (int, error) {
	const q = `
		SELECT
			COALESCE((SELECT SUM(kills) FROM bedwars_players WHERE player_id = $1), 0)
		  + COALESCE((SELECT SUM(kills) FROM skywars_players WHERE player_id = $1), 0)
		  + COALESCE((SELECT SUM(kills) FROM ffa_players     WHERE player_id = $1), 0)`
	var total int
	err := r.db.QueryRow(ctx, q, playerID).Scan(&total)
	return total, err
}

// Totals returns a player's career kills/deaths/wins/games across every mode.
// Each per-mode block is a UNION ALL row of (kills, deaths, won, 1) so the
// outer query can sum them uniformly.
func (r *StatsRepo) Totals(ctx context.Context, playerID int64) (Totals, error) {
	const q = `
		SELECT
			COALESCE(SUM(kills), 0)::int,
			COALESCE(SUM(deaths), 0)::int,
			COALESCE(SUM(CASE WHEN won THEN 1 ELSE 0 END), 0)::int,
			COUNT(*)::int
		FROM (
			SELECT kills, deaths, won FROM bedwars_players WHERE player_id = $1
			UNION ALL
			SELECT kills, deaths, won FROM skywars_players WHERE player_id = $1
			UNION ALL
			SELECT kills, deaths, won FROM ffa_players     WHERE player_id = $1
		) all_modes`
	var t Totals
	err := r.db.QueryRow(ctx, q, playerID).Scan(&t.Kills, &t.Deaths, &t.Wins, &t.Games)
	return t, err
}
