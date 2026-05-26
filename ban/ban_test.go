package ban

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "banlist.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func resetBans(t *testing.T) {
	t.Helper()
	mu.Lock()
	bans = map[string]*Info{}
	mu.Unlock()
}

func TestLoadReadsEntries(t *testing.T) {
	defer resetBans(t)

	path := writeTemp(t, `[
		{
			"playerName": "Griefer1",
			"Reason": "TNT spam",
			"BannedAt": "2024-06-01 12:00:00",
			"ExpiresAt": "2099-01-01 00:00:00"
		}
	]`)

	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	got := IsBanned("Griefer1")
	if got == nil {
		t.Fatal("Griefer1 should be banned")
	}
	if got.Reason != "TNT spam" {
		t.Errorf("reason: got %q, want %q", got.Reason, "TNT spam")
	}
}

func TestLoadExpiredEntriesIgnored(t *testing.T) {
	defer resetBans(t)

	path := writeTemp(t, `[
		{
			"playerName": "OldOffender",
			"Reason": "ancient ban",
			"BannedAt": "2020-01-01 00:00:00",
			"ExpiresAt": "2020-01-02 00:00:00"
		}
	]`)

	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if got := IsBanned("OldOffender"); got != nil {
		t.Errorf("expired ban should be ignored, got %+v", got)
	}
}

func TestLoadMissingFileNotError(t *testing.T) {
	defer resetBans(t)

	if err := Load(filepath.Join(t.TempDir(), "does-not-exist.json")); err != nil {
		t.Errorf("missing file should not error: %v", err)
	}
	if IsBanned("anyone") != nil {
		t.Error("nobody should be banned when no file is loaded")
	}
}

func TestLoadReplacesPreviousSet(t *testing.T) {
	defer resetBans(t)

	first := writeTemp(t, `[{"playerName":"A","Reason":"x","BannedAt":"2024-01-01 00:00:00","ExpiresAt":"2099-01-01 00:00:00"}]`)
	if err := Load(first); err != nil {
		t.Fatal(err)
	}
	if IsBanned("A") == nil {
		t.Fatal("A should be banned after first load")
	}

	second := writeTemp(t, `[{"playerName":"B","Reason":"y","BannedAt":"2024-01-01 00:00:00","ExpiresAt":"2099-01-01 00:00:00"}]`)
	if err := Load(second); err != nil {
		t.Fatal(err)
	}
	if IsBanned("A") != nil {
		t.Error("A should be unbanned after reload")
	}
	if IsBanned("B") == nil {
		t.Error("B should be banned after reload")
	}
}

func TestLoadInvalidJSONErrors(t *testing.T) {
	defer resetBans(t)

	path := writeTemp(t, "this is not json")
	if err := Load(path); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadInvalidDateErrors(t *testing.T) {
	defer resetBans(t)

	path := writeTemp(t, `[{"playerName":"x","Reason":"y","BannedAt":"yesterday","ExpiresAt":"tomorrow"}]`)
	if err := Load(path); err == nil {
		t.Fatal("expected date parse error")
	}
}

// Compile-time sanity: time.Time fields on Info still make sense.
var _ = func() time.Time { var i Info; return i.BannedAt }
