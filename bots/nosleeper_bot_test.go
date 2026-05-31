package bots

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeJSON dumps a JSON-encoded list into a temp file and returns the
// path. Used to feed LoadBadwords without touching the real
// badwords.json at the repo root.
func writeJSON(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "badwords.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadBadwordsPopulatesSet(t *testing.T) {
	path := writeJSON(t, `["spam", "Scam", " грубое-слово "]`)
	b := &NosleeperBot{badwords: map[string]struct{}{}}
	if err := b.LoadBadwords(path); err != nil {
		t.Fatal(err)
	}
	if !b.isBadWord("spam") {
		t.Error("spam should match")
	}
	if !b.isBadWord("SCAM") {
		t.Error("case-insensitive match should hit")
	}
	if !b.isBadWord("грубое-слово") {
		t.Error("trimmed unicode entry should match")
	}
	if b.isBadWord("hello") {
		t.Error("clean word should not match")
	}
}

func TestLoadBadwordsMissingFile(t *testing.T) {
	b := &NosleeperBot{badwords: map[string]struct{}{}}
	if err := b.LoadBadwords("/nonexistent/path/badwords.json"); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadBadwordsMalformed(t *testing.T) {
	path := writeJSON(t, `not a json array`)
	b := &NosleeperBot{badwords: map[string]struct{}{}}
	if err := b.LoadBadwords(path); err == nil {
		t.Error("expected parse error for malformed JSON")
	}
}

// TestShouldCensorTokenizes covers the rewrite branch (1..CensorMax bad
// words). Punctuation separates tokens; substring matches inside a
// longer token must NOT count.
func TestShouldCensorTokenizes(t *testing.T) {
	path := writeJSON(t, `["spam"]`)
	b := &NosleeperBot{badwords: map[string]struct{}{}}
	_ = b.LoadBadwords(path)
	cases := []struct {
		msg  string
		want bool
	}{
		{"hello world", false},
		{"spam", true},
		{"Hello, spam!", true}, // punctuation separates
		{"SPAMMY", false},      // substring inside a longer word — must NOT match
		{"this is total spam.", true},
	}
	for _, c := range cases {
		if got := b.shouldCensor(c.msg); got != c.want {
			t.Errorf("shouldCensor(%q): got %v, want %v", c.msg, got, c.want)
		}
	}
}

// TestThresholdSwitch covers the policy boundary defined by CensorMax:
//   - 1..CensorMax bad words → censor (rewrite)
//   - CensorMax+1 or more     → mute (drop)
//
// Driven by the constant so changes to CensorMax don't break the test.
func TestThresholdSwitch(t *testing.T) {
	path := writeJSON(t, `["spam"]`)
	b := &NosleeperBot{badwords: map[string]struct{}{}}
	_ = b.LoadBadwords(path)

	// Boundary message: exactly CensorMax bad words → censor only.
	boundary := strings.Repeat("spam ", CensorMax)
	if b.shouldMute(boundary) {
		t.Errorf("%d bad words should NOT trigger mute", CensorMax)
	}
	if !b.shouldCensor(boundary) {
		t.Errorf("%d bad words should trigger censor", CensorMax)
	}

	// Over the line: CensorMax+1 bad words → mute, not censor.
	over := strings.Repeat("spam ", CensorMax+1)
	if !b.shouldMute(over) {
		t.Errorf("%d bad words SHOULD trigger mute", CensorMax+1)
	}
	if b.shouldCensor(over) {
		t.Errorf("%d bad words should NOT also trigger censor (mute wins)",
			CensorMax+1)
	}
}

func TestCountBadwords(t *testing.T) {
	path := writeJSON(t, `["foo","bar"]`)
	b := &NosleeperBot{badwords: map[string]struct{}{}}
	_ = b.LoadBadwords(path)
	if got := b.countBadwords("foo bar foo baz"); got != 3 {
		t.Errorf("count: got %d, want 3", got)
	}
	if got := b.countBadwords("clean message"); got != 0 {
		t.Errorf("clean count: got %d, want 0", got)
	}
}

func TestTokenizeUnicode(t *testing.T) {
	got := tokenize("Привет, мир! Это спам.")
	want := []string{"Привет", "мир", "Это", "спам"}
	if len(got) != len(want) {
		t.Fatalf("tokens: got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReloadReplacesSet(t *testing.T) {
	pathA := writeJSON(t, `["foo"]`)
	pathB := writeJSON(t, `["bar"]`)
	b := &NosleeperBot{badwords: map[string]struct{}{}}
	_ = b.LoadBadwords(pathA)
	if !b.isBadWord("foo") || b.isBadWord("bar") {
		t.Fatal("initial load: foo should match, bar should not")
	}
	_ = b.LoadBadwords(pathB)
	if b.isBadWord("foo") || !b.isBadWord("bar") {
		t.Error("after reload: bar should match, foo should not")
	}
}
