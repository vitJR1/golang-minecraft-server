// Package templates centralizes schematic-template locations and the canonical
// template names. It exists so path handling lives in one place instead of
// scattered string literals, and so names are OS-independent: template names
// are always forward-slash ("bedwars/badwars_dota_map") while on-disk paths are
// built with the OS separator via filepath — which is what broke template
// lookup on Windows (filepath.Rel there yields backslash names).
package templates

import (
	"path/filepath"
	"strings"
)

// Root is the default directory (relative to the working dir) holding map
// .schem files and their sibling .json arena configs.
const Root = "schem/templates"

// Canonical template names — forward-slash, OS-independent. Each maps to
// <Root>/<name>.schem (plus an optional <name>.json arena config). Reference
// these constants instead of hardcoding the path strings.
const (
	Spawn          = "spawn"                    // hub world
	FFALobby       = "ffa/ffa_lobby"            // FFA lobby world
	BedwarsLobby   = "bedwars/bedwars_lobby"    // BedWars lobby world
	SkywarsLobby   = "skywars/skywars_lobby"    // SkyWars lobby world
	BedwarsDotaMap = "bedwars/badwars_dota_map" // shipped 4-team BedWars map
)

const (
	schemExt  = ".schem"
	configExt = ".json"
)

// SchemFile returns the on-disk path of a template's .schem file under root.
func SchemFile(root, name string) string {
	return filepath.Join(root, filepath.FromSlash(name)+schemExt)
}

// ConfigFile returns the on-disk path of a template's sibling .json config.
func ConfigFile(root, name string) string {
	return filepath.Join(root, filepath.FromSlash(name)+configExt)
}

// IsSchem reports whether path has the .schem extension (case-insensitive).
func IsSchem(path string) bool {
	return strings.EqualFold(filepath.Ext(path), schemExt)
}

// Name derives the canonical (forward-slash) template name from a .schem file
// path relative to root: "schem/templates/bedwars/x.schem" → "bedwars/x". The
// bool is false when path isn't under root or isn't a .schem.
func Name(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	ext := filepath.Ext(rel)
	if !strings.EqualFold(ext, schemExt) {
		return "", false
	}
	return filepath.ToSlash(rel[:len(rel)-len(ext)]), true
}
