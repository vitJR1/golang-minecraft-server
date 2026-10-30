package server

// Limits the movement / knockback tests drive the checks with (the
// anticheat package's own, restated so the tests pin them).
const (
	knockMoves = 5     // moves after a confirmed push before it's judged
	maxWorldXZ = 3.0e7 // vanilla world border
)
