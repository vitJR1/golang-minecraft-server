package templates

import (
	"path/filepath"
	"testing"
)

func TestName(t *testing.T) {
	n, ok := Name(Root, filepath.Join(Root, filepath.FromSlash("bedwars/badwars_dota_map")+".schem"))
	if !ok || n != "bedwars/badwars_dota_map" {
		t.Errorf("Name = %q,%v want bedwars/badwars_dota_map,true", n, ok)
	}
	// Always forward-slash, regardless of the OS separator in the input path.
	if n, _ := Name(Root, filepath.Join(Root, "a", "b", "c.schem")); n != "a/b/c" {
		t.Errorf("nested Name = %q, want a/b/c", n)
	}
	// Non-.schem is rejected.
	if _, ok := Name(Root, filepath.Join(Root, "x.json")); ok {
		t.Error(".json should not yield a template name")
	}
}

func TestSchemAndConfigFile(t *testing.T) {
	wantSchem := filepath.Join("root", filepath.FromSlash("a/b")+".schem")
	if got := SchemFile("root", "a/b"); got != wantSchem {
		t.Errorf("SchemFile = %q, want %q", got, wantSchem)
	}
	wantCfg := filepath.Join("root", filepath.FromSlash("a/b")+".json")
	if got := ConfigFile("root", "a/b"); got != wantCfg {
		t.Errorf("ConfigFile = %q, want %q", got, wantCfg)
	}
}

func TestIsSchem(t *testing.T) {
	if !IsSchem("foo/bar.schem") || !IsSchem("X.SCHEM") {
		t.Error("should detect .schem (case-insensitive)")
	}
	if IsSchem("foo.json") {
		t.Error("json is not schem")
	}
}
