package store

import (
	"context"
	"testing"
	"time"

	"minecraft-server/db"
)

// testStore connects to Postgres from the POSTGRES_* / DATABASE_URL env,
// applies migrations, and truncates the tables so each run starts clean. It
// skips the test when no database is reachable, so the suite stays green
// without docker (run `docker compose up -d` to exercise these).
func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	cfg := db.ConfigFromEnv()

	d, err := db.Connect(ctx, cfg)
	if err != nil {
		t.Skipf("postgres unavailable, skipping store integration tests: %v", err)
	}
	t.Cleanup(d.Close)

	if err := Migrate(cfg.DSN()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Fresh slate. CASCADE clears all dependent rows (bans, participation,
	// events, ratings); matches are listed explicitly (not FK'd to players).
	_, err = d.Pool.Exec(ctx,
		`TRUNCATE players, bedwars_matches, skywars_matches, ffa_matches RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return New(d.Pool), ctx
}

func TestPlayerUpsert(t *testing.T) {
	s, ctx := testStore(t)
	const uuid = "11111111-1111-1111-1111-111111111111"

	p1, err := s.Players.Upsert(ctx, uuid, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.Players.Upsert(ctx, uuid, "AliceRenamed")
	if err != nil {
		t.Fatal(err)
	}
	if p1.ID != p2.ID {
		t.Errorf("upsert created a new row: %d != %d", p1.ID, p2.ID)
	}
	if p2.Username != "AliceRenamed" {
		t.Errorf("username not updated: %q", p2.Username)
	}

	got, err := s.Players.GetByUUID(ctx, uuid)
	if err != nil || got.ID != p1.ID {
		t.Fatalf("GetByUUID: %v / %+v", err, got)
	}
}

func TestPlayerPassword(t *testing.T) {
	s, ctx := testStore(t)
	p, _ := s.Players.Upsert(ctx, "88888888-8888-8888-8888-888888888888", "Secured")

	// No password initially.
	if _, ok, err := s.Players.PasswordHash(ctx, p.ID); err != nil || ok {
		t.Fatalf("fresh player should have no password: ok=%v err=%v", ok, err)
	}

	if err := s.Players.SetPassword(ctx, p.ID, "$2a$10$examplebcrypthash"); err != nil {
		t.Fatal(err)
	}
	hash, ok, err := s.Players.PasswordHash(ctx, p.ID)
	if err != nil || !ok || hash != "$2a$10$examplebcrypthash" {
		t.Fatalf("password not stored: hash=%q ok=%v err=%v", hash, ok, err)
	}

	// Clearing it removes the password.
	if err := s.Players.SetPassword(ctx, p.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Players.PasswordHash(ctx, p.ID); ok {
		t.Error("password should be cleared")
	}

	// Unknown player.
	if err := s.Players.SetPassword(ctx, 999999, "x"); err != ErrNotFound {
		t.Errorf("SetPassword on missing player: got %v, want ErrNotFound", err)
	}
}

func TestBansLifecycle(t *testing.T) {
	s, ctx := testStore(t)
	p, _ := s.Players.Upsert(ctx, "22222222-2222-2222-2222-222222222222", "Banned")

	if _, err := s.Bans.ActiveForPlayer(ctx, p.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound before ban, got %v", err)
	}
	exp := time.Now().Add(time.Hour)
	if _, err := s.Bans.Create(ctx, p.ID, "griefing", "Mod", &exp); err != nil {
		t.Fatal(err)
	}
	b, err := s.Bans.ActiveForPlayer(ctx, p.ID)
	if err != nil || b.Reason != "griefing" {
		t.Fatalf("active ban: %v / %+v", err, b)
	}
	n, err := s.Bans.Deactivate(ctx, p.ID)
	if err != nil || n != 1 {
		t.Fatalf("deactivate: %v / n=%d", err, n)
	}
	if _, err := s.Bans.ActiveForPlayer(ctx, p.ID); err != ErrNotFound {
		t.Errorf("ban still active after deactivate: %v", err)
	}
}

func TestBedwarsEventsAggregate(t *testing.T) {
	s, ctx := testStore(t)
	killer, _ := s.Players.Upsert(ctx, "33333333-3333-3333-3333-333333333333", "Killer")
	victim, _ := s.Players.Upsert(ctx, "44444444-4444-4444-4444-444444444444", "Victim")

	m, err := s.BedwarsMatches.Create(ctx, "Lighthouse")
	if err != nil {
		t.Fatal(err)
	}

	// Raw events during the match.
	for range 3 {
		if _, err := s.BedwarsEvents.Kill(ctx, m.ID, killer.ID, victim.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.BedwarsEvents.Death(ctx, m.ID, victim.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BedwarsEvents.BedBreak(ctx, m.ID, killer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BedwarsEvents.FinalKill(ctx, m.ID, killer.ID, victim.ID); err != nil {
		t.Fatal(err)
	}

	// Roll the event log up into participation.
	if err := s.BedwarsPlayers.AggregateFromEvents(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := s.BedwarsPlayers.ListByMatch(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	byPlayer := map[int64]BedwarsPlayer{}
	for _, r := range rows {
		byPlayer[r.PlayerID] = r
	}
	if k := byPlayer[killer.ID]; k.Kills != 3 || k.BedsBroken != 1 || k.FinalKills != 1 {
		t.Errorf("killer aggregate wrong: %+v", k)
	}
	if v := byPlayer[victim.ID]; v.Deaths != 3 {
		t.Errorf("victim deaths: got %d, want 3", v.Deaths)
	}

	if err := s.BedwarsMatches.Finish(ctx, m.ID, "RED"); err != nil {
		t.Fatal(err)
	}
}

func TestRatingsAndRank(t *testing.T) {
	s, ctx := testStore(t)
	a, _ := s.Players.Upsert(ctx, "55555555-5555-5555-5555-555555555555", "Pro")
	b, _ := s.Players.Upsert(ctx, "66666666-6666-6666-6666-666666666666", "Noob")

	if _, err := s.BedwarsRatings.ApplyResult(ctx, a.ID, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BedwarsRatings.ApplyResult(ctx, b.ID, -30); err != nil {
		t.Fatal(err)
	}

	board, err := s.BedwarsRatings.Leaderboard(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(board) != 2 || board[0].PlayerID != a.ID || board[0].Rank != 1 {
		t.Fatalf("leaderboard order wrong: %+v", board)
	}
	if board[0].Rating != DefaultRating+50 {
		t.Errorf("rating: got %d, want %d", board[0].Rating, DefaultRating+50)
	}

	rk, err := s.BedwarsRatings.RankOf(ctx, b.ID)
	if err != nil || rk.Rank != 2 {
		t.Fatalf("RankOf(b): %v / rank=%d", err, rk.Rank)
	}
}

func TestCrossModeTotals(t *testing.T) {
	s, ctx := testStore(t)
	p, _ := s.Players.Upsert(ctx, "77777777-7777-7777-7777-777777777777", "Grinder")

	// 40 bedwars kills (via participation), 35 skywars, 25 ffa = 100 total.
	bm, _ := s.BedwarsMatches.Create(ctx, "m")
	if _, err := s.BedwarsPlayers.Upsert(ctx, BedwarsPlayer{MatchID: bm.ID, PlayerID: p.ID, Kills: 40, Deaths: 5, Won: true}); err != nil {
		t.Fatal(err)
	}
	sm, _ := s.SkywarsMatches.Create(ctx, "m")
	if _, err := s.SkywarsPlayers.Upsert(ctx, SkywarsPlayer{MatchID: sm.ID, PlayerID: p.ID, Kills: 35, Deaths: 8}); err != nil {
		t.Fatal(err)
	}
	fm, _ := s.FFAMatches.Create(ctx, "m")
	if _, err := s.FFAPlayers.Upsert(ctx, FFAPlayer{MatchID: fm.ID, PlayerID: p.ID, Kills: 25, Deaths: 12, Won: true}); err != nil {
		t.Fatal(err)
	}

	total, err := s.Stats.TotalKills(ctx, p.ID)
	if err != nil || total != 100 {
		t.Fatalf("TotalKills: got %d (err %v), want 100", total, err)
	}
	totals, err := s.Stats.Totals(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if totals.Kills != 100 || totals.Deaths != 25 || totals.Wins != 2 || totals.Games != 3 {
		t.Errorf("Totals wrong: %+v", totals)
	}
}
