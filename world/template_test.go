package world

import "testing"

func TestTemplateEmpty(t *testing.T) {
	tmpl := NewTemplate()
	if tmpl.BlockCount() != 0 {
		t.Errorf("empty template count: %d, want 0", tmpl.BlockCount())
	}
	if len(tmpl.SpawnPoints()) != 0 {
		t.Errorf("empty spawn points: %d, want 0", len(tmpl.SpawnPoints()))
	}
}

func TestTemplateSetAndInstantiate(t *testing.T) {
	tmpl := NewTemplate()
	tmpl.SetBlock(Position{0, 0, 0}, Stone)
	tmpl.SetBlock(Position{1, 0, 0}, Dirt)

	w := tmpl.Instantiate()
	if got := w.GetBlock(Position{0, 0, 0}); got != Stone {
		t.Errorf("(0,0,0): got %+v, want Stone", got)
	}
	if got := w.GetBlock(Position{1, 0, 0}); got != Dirt {
		t.Errorf("(1,0,0): got %+v, want Dirt", got)
	}
	if got := w.GetBlock(Position{2, 0, 0}); got != Air {
		t.Errorf("(2,0,0): got %+v, want Air", got)
	}
}

func TestTemplateSetAirRemoves(t *testing.T) {
	tmpl := NewTemplate()
	tmpl.SetBlock(Position{0, 0, 0}, Stone)
	tmpl.SetBlock(Position{0, 0, 0}, Air)
	if tmpl.BlockCount() != 0 {
		t.Errorf("Set(Air) should remove; count=%d", tmpl.BlockCount())
	}
}

func TestTemplateInstantiationIsIndependent(t *testing.T) {
	tmpl := NewTemplate()
	tmpl.SetBlock(Position{5, 5, 5}, Stone)

	w := tmpl.Instantiate()
	// Mutating the world must not affect the template.
	w.SetBlock(Position{5, 5, 5}, Air)
	if got := w.GetBlock(Position{5, 5, 5}); got != Air {
		t.Errorf("world after mutation: got %+v, want Air", got)
	}
	w2 := tmpl.Instantiate()
	if got := w2.GetBlock(Position{5, 5, 5}); got != Stone {
		t.Errorf("template should still have Stone; got %+v", got)
	}
}

func TestTemplateSpawnPointsCopy(t *testing.T) {
	tmpl := NewTemplate()
	tmpl.AddSpawnPoint(SpawnPoint{Position: Position{1, 64, 1}, Yaw: 90})
	tmpl.AddSpawnPoint(SpawnPoint{Position: Position{-1, 64, -1}, Yaw: 270})

	pts := tmpl.SpawnPoints()
	if len(pts) != 2 {
		t.Fatalf("got %d spawn points, want 2", len(pts))
	}
	// Mutating the returned slice must not affect the template.
	pts[0].Yaw = 0
	pts2 := tmpl.SpawnPoints()
	if pts2[0].Yaw != 90 {
		t.Errorf("template spawn yaw leaked: %v", pts2[0].Yaw)
	}
}

func TestCloneFromWorld(t *testing.T) {
	w := NewMemoryWorld()
	w.SetBlock(Position{1, 2, 3}, Stone)
	w.SetBlock(Position{4, 5, 6}, GrassBlock)

	tmpl := CloneFromWorld(w)
	if tmpl.BlockCount() != 2 {
		t.Errorf("cloned template count: %d, want 2", tmpl.BlockCount())
	}
	w2 := tmpl.Instantiate()
	if got := w2.GetBlock(Position{1, 2, 3}); got != Stone {
		t.Errorf("(1,2,3): got %+v, want Stone", got)
	}
	if got := w2.GetBlock(Position{4, 5, 6}); got != GrassBlock {
		t.Errorf("(4,5,6): got %+v, want GrassBlock", got)
	}
}
