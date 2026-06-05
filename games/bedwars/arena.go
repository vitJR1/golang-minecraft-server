package bedwars

import (
	"math"

	"minecraft-server/world"
)

// Arena is the built map plus the per-team layout the logic needs at
// runtime: where to (re)spawn players, which blocks are beds, and where
// resource generators sit. It is produced once per mode by buildArena and
// is then read-only, so it's safe to share across every instance.
//
// Separating "build the world" (here) from "run the rules" (bedwars.go)
// keeps each side independently testable and lets a future map be loaded
// from a schematic instead of generated — the logic only depends on the
// Arena shape, not on how it was produced (Dependency Inversion).
type Arena struct {
	Template *world.Template

	Spawns     []world.SpawnPoint     // Spawns[i] = team i's respawn point
	BedBlocks  [][]world.Position     // BedBlocks[i] = team i's two bed blocks
	Generators []Generator            // team forges + central forge
	bedOwner   map[world.Position]int // bed block → team ID (O(1) lookup)
}

// buildArena lays out one island per team around a circle, so the same code
// produces a 2-team duel or an 8-team free-for-all just by changing the
// team count. Each island is an end-stone platform clad in the team's wool,
// with a two-block bed, a spawn, and an iron generator; a central island
// carries a shared diamond generator.
func buildArena(teams []Team) *Arena {
	t := world.NewTemplate()
	a := &Arena{
		Template:  t,
		Spawns:    make([]world.SpawnPoint, len(teams)),
		BedBlocks: make([][]world.Position, len(teams)),
		bedOwner:  make(map[world.Position]int),
	}

	// Central island + shared diamond forge.
	fillPlatform(t, 0, 0, centerRadius, world.EndStone)
	a.Generators = append(a.Generators, Generator{
		Pos:           world.Position{X: 0, Y: baseY + 1, Z: 0},
		Resource:      Diamond,
		IntervalTicks: 600, // ~30s at 20 TPS
		TeamID:        neutral,
	})

	radius := ringRadius(len(teams))
	for _, tm := range teams {
		cx, cz, in := islandPlacement(tm.ID, len(teams), radius)

		// Platform: end-stone core with a wool rim for colour at a glance.
		fillPlatform(t, cx, cz, islandRadius, world.EndStone)
		cladRim(t, cx, cz, islandRadius, tm.Wool)

		// Bed: head at island centre, foot one block toward the map centre
		// (always on the platform). Two blocks tracked as "the bed".
		head := world.Position{X: cx, Y: baseY + 1, Z: cz}
		foot := world.Position{X: cx + in.dx, Y: baseY + 1, Z: cz + in.dz}
		t.SetBlock(head, tm.Bed)
		t.SetBlock(foot, tm.Bed)
		a.BedBlocks[tm.ID] = []world.Position{head, foot}
		a.bedOwner[head] = tm.ID
		a.bedOwner[foot] = tm.ID

		// Spawn: pulled to the outward edge (away from the bed's inward
		// foot), facing the map centre.
		sp := world.Position{X: cx - in.dx*2, Y: baseY + 1, Z: cz - in.dz*2}
		a.Spawns[tm.ID] = world.SpawnPoint{Position: sp, Yaw: yawFacing(in)}

		// Iron forge: marker block + generator on the inward edge.
		gen := world.Position{X: cx + in.dx*2, Y: baseY + 1, Z: cz + in.dz*2}
		t.SetBlock(world.Position{X: gen.X, Y: baseY, Z: gen.Z}, world.IronBlock)
		a.Generators = append(a.Generators, Generator{
			Pos:           gen,
			Resource:      Iron,
			IntervalTicks: 60, // ~3s at 20 TPS
			TeamID:        tm.ID,
		})
	}
	return a
}

// step is an integer cardinal direction (each component in {-1,0,1}).
type step struct{ dx, dz int }

// islandPlacement returns the centre (cx,cz) of team i's island and the
// cardinal step pointing inward (toward the map centre), for n teams evenly
// spaced on a circle of the given radius.
func islandPlacement(i, n, radius int) (cx, cz int, inward step) {
	theta := 2 * math.Pi * float64(i) / float64(n)
	ox, oz := math.Cos(theta), math.Sin(theta) // outward unit vector
	cx = int(math.Round(ox * float64(radius)))
	cz = int(math.Round(oz * float64(radius)))
	return cx, cz, cardinal(-ox, -oz)
}

// ringRadius spaces islands so they don't overlap: the circle's
// circumference must fit n islands of width ~(2*islandRadius)+gap. Never
// smaller than islandDistance so small modes still feel roomy.
func ringRadius(n int) int {
	const gap = 4
	needed := int(math.Ceil(float64(n) * float64(2*islandRadius+gap) / (2 * math.Pi)))
	if needed < islandDistance {
		return islandDistance
	}
	return needed
}

// cardinal collapses a float direction to the nearest 4-way cardinal step,
// so bed/spawn/generator offsets stay axis-aligned and on the platform.
func cardinal(dx, dz float64) step {
	if math.Abs(dx) >= math.Abs(dz) {
		return step{dx: sign(dx)}
	}
	return step{dz: sign(dz)}
}

func sign(v float64) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	default:
		return 0
	}
}

// yawFacing returns the yaw (degrees) that looks along the inward step, so a
// freshly-spawned player faces the centre of the map.
func yawFacing(in step) float32 {
	switch {
	case in.dx > 0:
		return 270 // facing +X
	case in.dx < 0:
		return 90 // facing -X
	case in.dz > 0:
		return 0 // facing +Z
	default:
		return 180 // facing -Z
	}
}

// bedTeam returns the team that owns the bed block at pos, if any.
func (a *Arena) bedTeam(pos world.Position) (int, bool) {
	id, ok := a.bedOwner[pos]
	return id, ok
}

// fillPlatform lays a solid (2r+1)² square of blk centred on (cx,cz) at baseY.
func fillPlatform(t *world.Template, cx, cz, r int, blk world.Block) {
	for x := cx - r; x <= cx+r; x++ {
		for z := cz - r; z <= cz+r; z++ {
			t.SetBlock(world.Position{X: x, Y: baseY, Z: z}, blk)
		}
	}
}

// cladRim replaces the outer ring of a platform with blk (the team colour).
func cladRim(t *world.Template, cx, cz, r int, blk world.Block) {
	for x := cx - r; x <= cx+r; x++ {
		for z := cz - r; z <= cz+r; z++ {
			if x == cx-r || x == cx+r || z == cz-r || z == cz+r {
				t.SetBlock(world.Position{X: x, Y: baseY, Z: z}, blk)
			}
		}
	}
}
