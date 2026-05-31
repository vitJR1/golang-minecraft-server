package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScanTemplatesEmpty(t *testing.T) {
	got, err := scanTemplates(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty dir: got %v, want []", got)
	}
}

func TestScanTemplatesMissingDir(t *testing.T) {
	got, err := scanTemplates(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Errorf("missing dir should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing dir: got %v, want []", got)
	}
}

func TestScanTemplatesFindsSchemFiles(t *testing.T) {
	root := t.TempDir()
	// Layout:
	//   root/spawn.schem
	//   root/arenas/skywars.schem
	//   root/arenas/bedwars.SCHEM      (case-insensitive ext)
	//   root/notes.txt                 (ignored)
	mustWrite(t, filepath.Join(root, "spawn.schem"))
	mustWrite(t, filepath.Join(root, "arenas", "skywars.schem"))
	mustWrite(t, filepath.Join(root, "arenas", "bedwars.SCHEM"))
	mustWrite(t, filepath.Join(root, "notes.txt"))

	got, err := scanTemplates(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join("arenas", "bedwars"),
		filepath.Join("arenas", "skywars"),
		"spawn",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}
