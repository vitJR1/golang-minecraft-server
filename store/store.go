package store

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned by repo getters when no row matches. Repos translate
// pgx.ErrNoRows into this so callers don't import pgx just to check.
var ErrNotFound = errors.New("store: not found")

// notFound maps pgx's no-rows sentinel to ErrNotFound, passing other errors
// (and nil) through unchanged.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// Store bundles every repository over a single pgx pool. Construct it with New
// once the db pool is up (see main.connectStores) and reach the repos through
// the typed fields.
type Store struct {
	Pool *pgxpool.Pool

	Players *PlayerRepo
	Bans    *BanRepo
	Mutes   *MuteRepo

	BedwarsMatches *BedwarsMatchRepo
	BedwarsPlayers *BedwarsPlayerRepo
	BedwarsEvents  *BedwarsEventRepo
	BedwarsRatings *RatingRepo

	SkywarsMatches *SkywarsMatchRepo
	SkywarsPlayers *SkywarsPlayerRepo
	SkywarsRatings *RatingRepo

	FFAMatches *FFAMatchRepo
	FFAPlayers *FFAPlayerRepo
	FFARatings *RatingRepo

	Stats *StatsRepo
}

// New wires every repo over pool. The rating repos are the same type bound to
// their per-mode table (a trusted constant — never user input).
func New(pool *pgxpool.Pool) *Store {
	return &Store{
		Pool:    pool,
		Players: NewPlayerRepo(pool),
		Bans:    NewBanRepo(pool),
		Mutes:   NewMuteRepo(pool),

		BedwarsMatches: NewBedwarsMatchRepo(pool),
		BedwarsPlayers: NewBedwarsPlayerRepo(pool),
		BedwarsEvents:  NewBedwarsEventRepo(pool),
		BedwarsRatings: NewRatingRepo(pool, "bedwars_ratings"),

		SkywarsMatches: NewSkywarsMatchRepo(pool),
		SkywarsPlayers: NewSkywarsPlayerRepo(pool),
		SkywarsRatings: NewRatingRepo(pool, "skywars_ratings"),

		FFAMatches: NewFFAMatchRepo(pool),
		FFAPlayers: NewFFAPlayerRepo(pool),
		FFARatings: NewRatingRepo(pool, "ffa_ratings"),

		Stats: NewStatsRepo(pool),
	}
}
