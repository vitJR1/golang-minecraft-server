package world

// SpawnPoint is one starting position (and orientation) a game can pick
// from when placing a freshly-joined player. Yaw is in degrees [0..360),
// Pitch in [-90..90].
type SpawnPoint struct {
	Position   Position
	Yaw, Pitch float32
}

// Template is a read-only snapshot of a world plus its spawn points,
// designed to be cloned. A typical mini-game registers a Template once at
// startup and then instantiates it per round — each round gets its own
// editable MemoryWorld with the same starting layout.
//
// Concurrency: Template is immutable after registration. Don't mutate via
// SetBlock / AddSpawnPoint once any goroutine might be calling Instantiate
// — the methods don't synchronize.
type Template struct {
	blocks      map[Position]Block
	spawnPoints []SpawnPoint
}

// NewTemplate creates an empty template with no blocks and no spawn points.
func NewTemplate() *Template {
	return &Template{blocks: make(map[Position]Block)}
}

// SetBlock records a block in the template. Setting Air removes the entry
// (template stays sparse, mirroring MemoryWorld).
func (t *Template) SetBlock(p Position, b Block) {
	if b == Air {
		delete(t.blocks, p)
		return
	}
	t.blocks[p] = b
}

// AddSpawnPoint appends a starting position. Game logic typically picks
// one round-robin or randomly for each joining player.
func (t *Template) AddSpawnPoint(s SpawnPoint) {
	t.spawnPoints = append(t.spawnPoints, s)
}

// SpawnPoints returns a copy of the registered spawn points. Mutations to
// the returned slice don't affect the template.
func (t *Template) SpawnPoints() []SpawnPoint {
	out := make([]SpawnPoint, len(t.spawnPoints))
	copy(out, t.spawnPoints)
	return out
}

// BlockCount reports how many non-Air blocks the template carries. Useful
// for sanity checks and for sizing hints.
func (t *Template) BlockCount() int {
	return len(t.blocks)
}

// Instantiate creates a fresh MemoryWorld populated with every block in
// the template. Subsequent edits to the returned world don't affect the
// template, and vice versa.
func (t *Template) Instantiate() *MemoryWorld {
	w := NewMemoryWorld()
	for p, b := range t.blocks {
		w.SetBlock(p, b)
	}
	return w
}

// CloneFromWorld captures a snapshot of src's non-Air blocks into a fresh
// template. Useful for taking a dev-built world and freezing it as a
// reusable starting state. Spawn points must be added separately.
func CloneFromWorld(src World) *Template {
	t := NewTemplate()
	src.Range(func(p Position, b Block) {
		t.blocks[p] = b
	})
	return t
}
