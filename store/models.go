// Package store holds the persistence entities and their repositories,
// layered on top of the db package's pgx pool. Each table has a model struct
// here and a repo (players.go, bans.go, mutes.go, bedwars.go, skywars.go,
// ffa.go, stats.go) exposing the queries the server needs.
//
// Data model (chosen in design): per-mode MATCH history + per-match
// PARTICIPATION rows are the source of truth for stats; cumulative numbers
// like "total kills" are aggregated on read (see StatsRepo). Bedwars also
// keeps a raw append-only event log (bedwars_events) the server writes during
// a match and aggregates into participation afterwards. Per-mode ELO ratings
// drive rank, which is computed at read time from the rating column.
package store

import "time"

// Player is the identity record. UUID is the canonical hyphenated string
// form (the players.uuid column is a Postgres UUID).
type Player struct {
	ID        int64
	UUID      string
	Username  string
	FirstSeen time.Time
	LastSeen  time.Time
}

// Ban is a moderation record. ExpiresAt is nil for a permanent ban. Active is
// the fast "is this ban in force" flag (an unban flips it false); callers
// should also check ExpiresAt against now for time-based expiry.
type Ban struct {
	ID        int64
	PlayerID  int64
	Reason    string
	IssuedBy  string
	CreatedAt time.Time
	ExpiresAt *time.Time
	Active    bool
}

// Mute mirrors Ban for chat muting.
type Mute struct {
	ID        int64
	PlayerID  int64
	Reason    string
	IssuedBy  string
	CreatedAt time.Time
	ExpiresAt *time.Time
	Active    bool
}

// --- Match history ---------------------------------------------------------

// BedwarsMatch is one finished bedwars game. EndedAt is nil while in progress.
type BedwarsMatch struct {
	ID         int64
	Map        string
	StartedAt  time.Time
	EndedAt    *time.Time
	WinnerTeam string
}

// SkywarsMatch is one finished skywars game.
type SkywarsMatch struct {
	ID             int64
	Map            string
	StartedAt      time.Time
	EndedAt        *time.Time
	WinnerPlayerID *int64
}

// FFAMatch is one finished free-for-all game.
type FFAMatch struct {
	ID             int64
	Map            string
	StartedAt      time.Time
	EndedAt        *time.Time
	WinnerPlayerID *int64
}

// --- Per-match participation ----------------------------------------------

// BedwarsPlayer is one player's aggregated result in one bedwars match.
type BedwarsPlayer struct {
	ID         int64
	MatchID    int64
	PlayerID   int64
	Kills      int
	Deaths     int
	FinalKills int
	BedsBroken int
	Won        bool
}

// SkywarsPlayer is one player's result in one skywars match.
type SkywarsPlayer struct {
	ID       int64
	MatchID  int64
	PlayerID int64
	Kills    int
	Deaths   int
	Won      bool
}

// FFAPlayer is one player's result in one ffa match.
type FFAPlayer struct {
	ID       int64
	MatchID  int64
	PlayerID int64
	Kills    int
	Deaths   int
	Won      bool
}

// --- Bedwars event log -----------------------------------------------------

// BedwarsEventType enumerates the raw events appended during a match.
type BedwarsEventType string

const (
	EventKill      BedwarsEventType = "kill"
	EventDeath     BedwarsEventType = "death"
	EventBedBreak  BedwarsEventType = "bed_break"
	EventFinalKill BedwarsEventType = "final_kill"
)

// BedwarsEvent is one row of the append-only bedwars_events log. TargetID is
// the victim for kill/final_kill events, nil otherwise. Data is an optional
// JSON payload (raw bytes; "{}" when empty).
type BedwarsEvent struct {
	ID        int64
	MatchID   int64
	PlayerID  int64
	Type      BedwarsEventType
	TargetID  *int64
	Data      []byte
	CreatedAt time.Time
}

// --- Ratings / rank --------------------------------------------------------

// Rating is a player's ELO-style rating in one mode. Rank itself is not
// stored — it's computed from rating at read time (see RankedPlayer).
type Rating struct {
	PlayerID  int64
	Rating    int
	Games     int
	UpdatedAt time.Time
}

// RankedPlayer is a leaderboard row: a rating plus its computed 1-based rank
// (dense over the mode, highest rating = rank 1) and the player's username.
type RankedPlayer struct {
	PlayerID int64
	Username string
	Rating   int
	Games    int
	Rank     int
}
