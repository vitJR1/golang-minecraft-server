package store

import (
	"testing"
	"time"
)

// TestBanStoreRoundTrip exercises the ban.Store adapter against a real
// Postgres (skipped without one, like the other integration tests).
func TestBanStoreRoundTrip(t *testing.T) {
	s, ctx := testStore(t)
	bs := NewBanStore(s.Players, s.Bans)

	if got, err := bs.IsBanned(ctx, "Nobody"); err != nil || got != nil {
		t.Fatalf("unknown name: %+v / %v", got, err)
	}

	// Banning a never-seen name creates the player row.
	until := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := bs.Add(ctx, "Griefer", "tnt", "Mod", until); err != nil {
		t.Fatal(err)
	}
	got, err := bs.IsBanned(ctx, "griefer") // case-insensitive lookup
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Reason != "tnt" || got.IssuedBy != "Mod" || !got.ExpiresAt.Equal(until) {
		t.Fatalf("after Add: %+v", got)
	}
	if _, err := s.Players.GetByUsername(ctx, "Griefer"); err != nil {
		t.Fatalf("player row should exist: %v", err)
	}

	// Re-banning replaces the active ban rather than stacking.
	if err := bs.Add(ctx, "Griefer", "again", "Mod", until.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := bs.IsBanned(ctx, "Griefer"); got == nil || got.Reason != "again" {
		t.Fatalf("after second Add: %+v", got)
	}

	if err := bs.Remove(ctx, "Griefer"); err != nil {
		t.Fatal(err)
	}
	if got, _ := bs.IsBanned(ctx, "Griefer"); got != nil {
		t.Fatalf("after Remove: %+v", got)
	}
	if err := bs.Remove(ctx, "Nobody"); err != nil {
		t.Errorf("Remove unknown: %v", err)
	}

	// Expired bans are invisible.
	if err := bs.Add(ctx, "Old", "x", "Mod", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ := bs.IsBanned(ctx, "Old"); got != nil {
		t.Fatalf("expired ban visible: %+v", got)
	}
}
