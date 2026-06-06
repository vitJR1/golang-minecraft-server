package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Matches
// ---------------------------------------------------------------------------

// BedwarsMatchRepo manages bedwars_matches rows.
type BedwarsMatchRepo struct{ db *pgxpool.Pool }

func NewBedwarsMatchRepo(db *pgxpool.Pool) *BedwarsMatchRepo { return &BedwarsMatchRepo{db: db} }

const bedwarsMatchCols = `id, map, started_at, ended_at, winner_team`

func scanBedwarsMatch(row pgx.Row) (BedwarsMatch, error) {
	var m BedwarsMatch
	err := row.Scan(&m.ID, &m.Map, &m.StartedAt, &m.EndedAt, &m.WinnerTeam)
	return m, notFound(err)
}

// Create opens a new match (started_at = now, not yet ended). Returns the row.
func (r *BedwarsMatchRepo) Create(ctx context.Context, mapName string) (BedwarsMatch, error) {
	return scanBedwarsMatch(r.db.QueryRow(ctx,
		`INSERT INTO bedwars_matches (map) VALUES ($1) RETURNING `+bedwarsMatchCols, mapName))
}

// Finish stamps ended_at and records the winning team.
func (r *BedwarsMatchRepo) Finish(ctx context.Context, matchID int64, winnerTeam string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE bedwars_matches SET ended_at = now(), winner_team = $2 WHERE id = $1`,
		matchID, winnerTeam)
	return err
}

// Get returns a match by id, or ErrNotFound.
func (r *BedwarsMatchRepo) Get(ctx context.Context, matchID int64) (BedwarsMatch, error) {
	return scanBedwarsMatch(r.db.QueryRow(ctx,
		`SELECT `+bedwarsMatchCols+` FROM bedwars_matches WHERE id = $1`, matchID))
}

// ---------------------------------------------------------------------------
// Per-match participation
// ---------------------------------------------------------------------------

// BedwarsPlayerRepo manages bedwars_players (per-match results).
type BedwarsPlayerRepo struct{ db *pgxpool.Pool }

func NewBedwarsPlayerRepo(db *pgxpool.Pool) *BedwarsPlayerRepo { return &BedwarsPlayerRepo{db: db} }

const bedwarsPlayerCols = `id, match_id, player_id, kills, deaths, final_kills, beds_broken, won`

func scanBedwarsPlayer(row pgx.Row) (BedwarsPlayer, error) {
	var p BedwarsPlayer
	err := row.Scan(&p.ID, &p.MatchID, &p.PlayerID, &p.Kills, &p.Deaths, &p.FinalKills, &p.BedsBroken, &p.Won)
	return p, notFound(err)
}

// Upsert writes one player's result for a match, replacing any existing row
// for the (match, player) pair. Returns the stored row.
func (r *BedwarsPlayerRepo) Upsert(ctx context.Context, p BedwarsPlayer) (BedwarsPlayer, error) {
	const q = `
		INSERT INTO bedwars_players
			(match_id, player_id, kills, deaths, final_kills, beds_broken, won)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (match_id, player_id) DO UPDATE SET
			kills = EXCLUDED.kills, deaths = EXCLUDED.deaths,
			final_kills = EXCLUDED.final_kills, beds_broken = EXCLUDED.beds_broken,
			won = EXCLUDED.won
		RETURNING ` + bedwarsPlayerCols
	return scanBedwarsPlayer(r.db.QueryRow(ctx, q,
		p.MatchID, p.PlayerID, p.Kills, p.Deaths, p.FinalKills, p.BedsBroken, p.Won))
}

// ListByMatch returns every player's result for a match.
func (r *BedwarsPlayerRepo) ListByMatch(ctx context.Context, matchID int64) ([]BedwarsPlayer, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+bedwarsPlayerCols+` FROM bedwars_players WHERE match_id = $1`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BedwarsPlayer
	for rows.Next() {
		p, err := scanBedwarsPlayer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AggregateFromEvents rolls the raw bedwars_events for a match up into
// bedwars_players rows (kills/deaths/final_kills/beds_broken counted by type).
// This is the "after the game, collect everything into statistics" step. It's
// idempotent — re-running recomputes the same counts. The won flag is left to
// the caller (it derives from the match's winning team, not the event log).
func (r *BedwarsPlayerRepo) AggregateFromEvents(ctx context.Context, matchID int64) error {
	const q = `
		INSERT INTO bedwars_players
			(match_id, player_id, kills, deaths, final_kills, beds_broken)
		SELECT
			match_id,
			player_id,
			COUNT(*) FILTER (WHERE type = 'kill')::int,
			COUNT(*) FILTER (WHERE type = 'death')::int,
			COUNT(*) FILTER (WHERE type = 'final_kill')::int,
			COUNT(*) FILTER (WHERE type = 'bed_break')::int
		FROM bedwars_events
		WHERE match_id = $1
		GROUP BY match_id, player_id
		ON CONFLICT (match_id, player_id) DO UPDATE SET
			kills = EXCLUDED.kills, deaths = EXCLUDED.deaths,
			final_kills = EXCLUDED.final_kills, beds_broken = EXCLUDED.beds_broken`
	_, err := r.db.Exec(ctx, q, matchID)
	return err
}

// ---------------------------------------------------------------------------
// Event log (append-only on the hot path)
// ---------------------------------------------------------------------------

// BedwarsEventRepo appends to and reads the bedwars_events log.
type BedwarsEventRepo struct{ db *pgxpool.Pool }

func NewBedwarsEventRepo(db *pgxpool.Pool) *BedwarsEventRepo { return &BedwarsEventRepo{db: db} }

// Insert appends one event and returns its id. ev.Data may be nil (stored as
// an empty JSON object). ev.TargetID is the victim for kills, nil otherwise.
func (r *BedwarsEventRepo) Insert(ctx context.Context, ev BedwarsEvent) (int64, error) {
	data := ev.Data
	if data == nil {
		data = []byte("{}")
	}
	var id int64
	err := r.db.QueryRow(ctx, `
		INSERT INTO bedwars_events (match_id, player_id, type, target_id, data)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		ev.MatchID, ev.PlayerID, string(ev.Type), ev.TargetID, data).Scan(&id)
	return id, err
}

// Kill appends a kill event (killer credited, victim recorded as target).
func (r *BedwarsEventRepo) Kill(ctx context.Context, matchID, killerID, victimID int64) (int64, error) {
	return r.Insert(ctx, BedwarsEvent{MatchID: matchID, PlayerID: killerID, Type: EventKill, TargetID: &victimID})
}

// FinalKill appends a final-kill event (the killing blow after a bed is gone).
func (r *BedwarsEventRepo) FinalKill(ctx context.Context, matchID, killerID, victimID int64) (int64, error) {
	return r.Insert(ctx, BedwarsEvent{MatchID: matchID, PlayerID: killerID, Type: EventFinalKill, TargetID: &victimID})
}

// Death appends a death event for a player.
func (r *BedwarsEventRepo) Death(ctx context.Context, matchID, playerID int64) (int64, error) {
	return r.Insert(ctx, BedwarsEvent{MatchID: matchID, PlayerID: playerID, Type: EventDeath})
}

// BedBreak appends a bed-break event credited to the breaker.
func (r *BedwarsEventRepo) BedBreak(ctx context.Context, matchID, breakerID int64) (int64, error) {
	return r.Insert(ctx, BedwarsEvent{MatchID: matchID, PlayerID: breakerID, Type: EventBedBreak})
}

// ListByMatch returns every event for a match in chronological order.
func (r *BedwarsEventRepo) ListByMatch(ctx context.Context, matchID int64) ([]BedwarsEvent, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, match_id, player_id, type, target_id, data, created_at
		FROM bedwars_events WHERE match_id = $1 ORDER BY id`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BedwarsEvent
	for rows.Next() {
		var e BedwarsEvent
		var typ string
		if err := rows.Scan(&e.ID, &e.MatchID, &e.PlayerID, &typ, &e.TargetID, &e.Data, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Type = BedwarsEventType(typ)
		out = append(out, e)
	}
	return out, rows.Err()
}
