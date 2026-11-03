package lobby

import (
	"testing"

	"minecraft-server/world"
)

func TestForGame(t *testing.T) {
	cases := map[string]string{
		"bedwars": BedWars, "bedwars-1x1": BedWars, "bedwars-2x5": BedWars,
		"ffa": FFA, "skywars-solo": SkyWars,
	}
	for game, want := range cases {
		if got, ok := ForGame(game); !ok || got != want {
			t.Errorf("ForGame(%q) = %q %v, want %q", game, got, ok, want)
		}
	}
	for _, game := range []string{"", "bedwarsish", "parkour"} {
		if got, ok := ForGame(game); ok {
			t.Errorf("ForGame(%q) = %q, want none", game, got)
		}
	}
}

func TestCatalogue(t *testing.T) {
	for _, id := range IDs() {
		if TemplateName(id) == "" {
			t.Errorf("lobby %q has no template", id)
		}
		if names, ok := Arenas(id); !ok || len(names) == 0 || !Is(id) {
			t.Errorf("lobby %q has no arena list", id)
		}
	}
	if Is("hub") {
		t.Error("the hub isn't a game lobby")
	}
	if id, ok := MenuTarget("bw"); !ok || id != BedWars {
		t.Errorf("MenuTarget(bw) = %q %v", id, ok)
	}
	ids := IDs()
	ids[0] = "changed"
	if IDs()[0] != FFA {
		t.Error("IDs must return a copy")
	}
}

func TestDefaultWorld(t *testing.T) {
	w := DefaultWorld()
	if w.GetBlock(world.Position{X: 0, Y: 66, Z: 0}).Name != world.Stone.Name {
		t.Error("no stone under the spawn")
	}
	if w.GetBlock(world.Position{X: 6, Y: 66, Z: 0}).Name == world.Stone.Name {
		t.Error("pad should be 11×11")
	}
}
