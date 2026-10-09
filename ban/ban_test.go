package ban

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var ctx = context.Background()

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "banlist.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustBanned(t *testing.T, s Store, name string) *Info {
	t.Helper()
	got, err := s.IsBanned(ctx, name)
	if err != nil {
		t.Fatalf("IsBanned(%s): %v", name, err)
	}
	return got
}

func TestFileStoreReadsEntries(t *testing.T) {
	path := writeTemp(t, `[
		{
			"playerName": "Griefer1",
			"Reason": "TNT spam",
			"BannedAt": "2024-06-01 12:00:00",
			"ExpiresAt": "2099-01-01 00:00:00"
		}
	]`)

	s, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := mustBanned(t, s, "Griefer1")
	if got == nil {
		t.Fatal("Griefer1 should be banned")
	}
	if got.Reason != "TNT spam" {
		t.Errorf("reason: got %q, want %q", got.Reason, "TNT spam")
	}
}

func TestFileStoreExpiredEntriesIgnored(t *testing.T) {
	path := writeTemp(t, `[
		{
			"playerName": "OldOffender",
			"Reason": "ancient ban",
			"BannedAt": "2020-01-01 00:00:00",
			"ExpiresAt": "2020-01-02 00:00:00"
		}
	]`)

	s, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustBanned(t, s, "OldOffender"); got != nil {
		t.Errorf("expired ban should be ignored, got %+v", got)
	}
}

func TestFileStoreMissingFileNotError(t *testing.T) {
	s, err := NewFileStore(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if mustBanned(t, s, "anyone") != nil {
		t.Error("nobody should be banned when no file is loaded")
	}
}

func TestFileStoreReloadReplacesPreviousSet(t *testing.T) {
	path := writeTemp(t, `[{"playerName":"A","Reason":"x","BannedAt":"2024-01-01 00:00:00","ExpiresAt":"2099-01-01 00:00:00"}]`)
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if mustBanned(t, s, "A") == nil {
		t.Fatal("A should be banned after first load")
	}

	second := `[{"playerName":"B","Reason":"y","BannedAt":"2024-01-01 00:00:00","ExpiresAt":"2099-01-01 00:00:00"}]`
	if err := os.WriteFile(path, []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if mustBanned(t, s, "A") != nil {
		t.Error("A should be unbanned after reload")
	}
	if mustBanned(t, s, "B") == nil {
		t.Error("B should be banned after reload")
	}
}

func TestFileStoreInvalidJSONErrors(t *testing.T) {
	path := writeTemp(t, "this is not json")
	if _, err := NewFileStore(path); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestFileStoreInvalidDateErrors(t *testing.T) {
	path := writeTemp(t, `[{"playerName":"x","Reason":"y","BannedAt":"yesterday","ExpiresAt":"tomorrow"}]`)
	if _, err := NewFileStore(path); err == nil {
		t.Fatal("expected date parse error")
	}
}

// TestFileStoreAddPersists: Add/Remove rewrite the file so a fresh store
// built from the same path sees the change.
func TestFileStoreAddPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "banlist.json")
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Add(ctx, "Cheater", "x-ray", "Mod", until); err != nil {
		t.Fatal(err)
	}

	var raw []rawEntry
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 || raw[0].PlayerName != "Cheater" || raw[0].IssuedBy != "Mod" ||
		raw[0].ExpiresAt != "2099-01-01 00:00:00" {
		t.Fatalf("on-disk entries: %+v", raw)
	}

	again, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustBanned(t, again, "Cheater"); got == nil || got.Reason != "x-ray" {
		t.Fatalf("reloaded store: %+v", got)
	}

	if err := again.Remove(ctx, "Cheater"); err != nil {
		t.Fatal(err)
	}
	third, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if mustBanned(t, third, "Cheater") != nil {
		t.Error("Remove should have been persisted")
	}
}

func TestMemoryStoreRoundTrip(t *testing.T) {
	s := NewMemoryStore()
	if mustBanned(t, s, "X") != nil {
		t.Fatal("fresh store should be empty")
	}
	if err := s.Add(ctx, "X", "r", "op", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got := mustBanned(t, s, "X")
	if got == nil || got.Reason != "r" || got.IssuedBy != "op" {
		t.Fatalf("after Add: %+v", got)
	}
	// Expired entries are invisible.
	if err := s.Add(ctx, "Y", "r", "op", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if mustBanned(t, s, "Y") != nil {
		t.Error("expired ban should be hidden")
	}
	if err := s.Remove(ctx, "X"); err != nil {
		t.Fatal(err)
	}
	if mustBanned(t, s, "X") != nil {
		t.Error("X should be unbanned")
	}
	// Remove of an unknown name is a no-op.
	if err := s.Remove(ctx, "nobody"); err != nil {
		t.Errorf("Remove unknown: %v", err)
	}
}

// TestImport: in-force entries land in dst, expired ones are dropped, and
// names dst already bans are left untouched.
func TestImport(t *testing.T) {
	dst := NewMemoryStore()
	keep := time.Date(2098, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := dst.Add(ctx, "Already", "db reason", "DbMod", keep); err != nil {
		t.Fatal(err)
	}
	entries := []Info{
		{PlayerName: "Fresh", Reason: "file reason", ExpiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)},
		{PlayerName: "Gone", Reason: "old", ExpiresAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
		{PlayerName: "Already", Reason: "file reason", IssuedBy: "FileMod", ExpiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	res, err := Import(ctx, dst, entries)
	if err != nil {
		t.Fatal(err)
	}
	if res != (ImportResult{Added: 1, Expired: 1, Kept: 1}) {
		t.Fatalf("result: %+v", res)
	}
	if got := mustBanned(t, dst, "Fresh"); got == nil || got.IssuedBy != "banlist import" {
		t.Fatalf("Fresh: %+v", got)
	}
	if mustBanned(t, dst, "Gone") != nil {
		t.Error("expired entry should not be imported")
	}
	if got := mustBanned(t, dst, "Already"); got == nil || got.Reason != "db reason" || !got.ExpiresAt.Equal(keep) {
		t.Fatalf("existing ban clobbered: %+v", got)
	}
}

// Both implementations satisfy the interface.
var (
	_ Store = (*MemoryStore)(nil)
	_ Store = (*FileStore)(nil)
)
