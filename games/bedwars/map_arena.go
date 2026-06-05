package bedwars

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"minecraft-server/schem"
	"minecraft-server/world"
)

// spawnInset is how many blocks a spawn is pulled back from the bed, away
// from the map centre, so players land deep on their own base.
const spawnInset = 3

// buildSchemArena produces an Arena from a real .schem map. The schematic
// only carries blocks, so the team layout is recovered from the map itself:
// every bed is detected, clustered into the two-block beds, grouped into one
// island per team, recoloured to the team's colour, and used to derive a
// spawn and an iron forge. Central emerald markers (and a centre diamond)
// become neutral generators.
//
// It returns an error (rather than panicking) when the map can't be loaded
// or its bed count doesn't match the team count, so the caller can fall back
// to the generated arena.
func buildSchemArena(path string, teams []Team) (*Arena, error) {
	s, err := schem.LoadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	// Centre the map on the world origin, same convention as main.go's
	// global template loader, so coordinates stay small and symmetric.
	ox, oy, oz := -int(s.Width)/2, baseY, -int(s.Length)/2
	tmpl := s.ToTemplateAt(ox, oy, oz)

	beds := detectBedClusters(s, ox, oy, oz)
	if len(beds) != len(teams) {
		return nil, fmt.Errorf("map %s has %d beds, mode needs %d teams", path, len(beds), len(teams))
	}

	// Map centre = centroid of all beds; teams are ordered by angle around
	// it so team→island assignment is deterministic across runs.
	mx, mz := centroid(beds)
	sort.Slice(beds, func(i, j int) bool {
		return math.Atan2(beds[i].cz-mz, beds[i].cx-mx) <
			math.Atan2(beds[j].cz-mz, beds[j].cx-mx)
	})

	a := &Arena{
		Template:  tmpl,
		Spawns:    make([]world.SpawnPoint, len(teams)),
		BedBlocks: make([][]world.Position, len(teams)),
		bedOwner:  make(map[world.Position]int),
	}

	for i := range teams {
		bc := beds[i]
		// Recolour both halves to the team's bed and register ownership.
		for _, p := range bc.positions {
			tmpl.SetBlock(p, teams[i].Bed)
			a.bedOwner[p] = i
		}
		a.BedBlocks[i] = bc.positions

		// Spawn: pulled back from the bed, away from centre, facing centre.
		sx, sz := pushAway(bc.cx, bc.cz, mx, mz, spawnInset)
		spawn := world.Position{X: round(sx), Y: bc.y, Z: round(sz)}
		a.Spawns[i] = world.SpawnPoint{Position: spawn, Yaw: yawToward(mx-bc.cx, mz-bc.cz)}

		// Iron forge on the base (at the spawn spot — granter is a seam).
		a.Generators = append(a.Generators, Generator{
			Pos: spawn, Resource: Iron, IntervalTicks: 60, TeamID: i,
		})
	}

	// Neutral generators: emerald markers found on the map + a centre diamond.
	for _, p := range detectMarkers(s, ox, oy, oz, "emerald_block") {
		a.Generators = append(a.Generators, Generator{
			Pos: p, Resource: Emerald, IntervalTicks: 900, TeamID: neutral,
		})
	}
	a.Generators = append(a.Generators, Generator{
		Pos:           world.Position{X: round(mx), Y: beds[0].y, Z: round(mz)},
		Resource:      Diamond,
		IntervalTicks: 600,
		TeamID:        neutral,
	})

	return a, nil
}

// bedCluster is one detected bed: its block positions plus the cluster's
// horizontal centre and floor (feet) Y in world coordinates.
type bedCluster struct {
	positions []world.Position
	cx, cz    float64
	y         int
}

// detectBedClusters scans the schematic for every "*_bed" block, converts to
// world coords, and unions adjacent blocks into clusters (the two halves of
// each bed). Returns one bedCluster per physical bed.
func detectBedClusters(s *schem.Schematic, ox, oy, oz int) []bedCluster {
	var beds []world.Position
	forEachBlock(s, func(x, y, z int, base string) {
		if strings.HasSuffix(base, "_bed") {
			beds = append(beds, world.Position{X: ox + x, Y: oy + y, Z: oz + z})
		}
	})

	// Union-find over bed blocks: adjacent (manhattan distance 1) blocks are
	// the two halves of the same bed.
	parent := make([]int, len(beds))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i := range beds {
		for j := i + 1; j < len(beds); j++ {
			if manhattan(beds[i], beds[j]) == 1 {
				parent[find(i)] = find(j)
			}
		}
	}

	byRoot := map[int][]world.Position{}
	for i, p := range beds {
		r := find(i)
		byRoot[r] = append(byRoot[r], p)
	}
	out := make([]bedCluster, 0, len(byRoot))
	for _, ps := range byRoot {
		var sx, sz float64
		for _, p := range ps {
			sx += float64(p.X)
			sz += float64(p.Z)
		}
		out = append(out, bedCluster{
			positions: ps,
			cx:        sx / float64(len(ps)),
			cz:        sz / float64(len(ps)),
			y:         ps[0].Y,
		})
	}
	return out
}

// detectMarkers returns world positions of every block whose base name
// equals "minecraft:"+name.
func detectMarkers(s *schem.Schematic, ox, oy, oz int, name string) []world.Position {
	target := "minecraft:" + name
	var out []world.Position
	forEachBlock(s, func(x, y, z int, base string) {
		if base == target {
			out = append(out, world.Position{X: ox + x, Y: oy + y, Z: oz + z})
		}
	})
	return out
}

// forEachBlock walks the schematic in storage order, handing each non-empty
// cell's coordinates and property-stripped block name to fn.
func forEachBlock(s *schem.Schematic, fn func(x, y, z int, base string)) {
	idx := 0
	for y := 0; y < int(s.Height); y++ {
		for z := 0; z < int(s.Length); z++ {
			for x := 0; x < int(s.Width); x++ {
				pid := s.Blocks[idx]
				idx++
				if int(pid) >= len(s.Palette) {
					continue
				}
				name := s.Palette[pid]
				if i := strings.IndexByte(name, '['); i >= 0 {
					name = name[:i]
				}
				fn(x, y, z, name)
			}
		}
	}
}

func centroid(beds []bedCluster) (mx, mz float64) {
	for _, b := range beds {
		mx += b.cx
		mz += b.cz
	}
	n := float64(len(beds))
	return mx / n, mz / n
}

// pushAway returns (cx,cz) moved `dist` blocks directly away from (mx,mz).
func pushAway(cx, cz, mx, mz float64, dist float64) (float64, float64) {
	dx, dz := cx-mx, cz-mz
	d := math.Hypot(dx, dz)
	if d == 0 {
		return cx, cz
	}
	return cx + dx/d*dist, cz + dz/d*dist
}

// yawToward converts a horizontal direction (dx,dz) into a Minecraft yaw
// (degrees) facing along it, so a spawned player looks that way.
func yawToward(dx, dz float64) float32 {
	return float32(math.Atan2(-dx, dz) * 180 / math.Pi)
}

func manhattan(a, b world.Position) int {
	return abs(a.X-b.X) + abs(a.Y-b.Y) + abs(a.Z-b.Z)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func round(v float64) int { return int(math.Round(v)) }
