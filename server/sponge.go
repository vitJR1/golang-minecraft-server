package server

import "minecraft-server/world"

// sponge.go: a placed sponge soaks up water like vanilla — every water
// block reachable through water within spongeRadius (Chebyshev) of it, up
// to spongeMaxBlocks, turns to air and the sponge becomes a wet sponge.
// Lava is left alone.

const (
	spongeRadius    = 7
	spongeMaxBlocks = 65
)

// absorbWater runs the soak for the sponge at pos. Returns how many water
// blocks were removed.
func (i *Instance) absorbWater(pos world.Position) int {
	visited := map[world.Position]bool{pos: true}
	queue := []world.Position{pos}
	var removed []world.BlockChange
	for len(queue) > 0 && len(removed) < spongeMaxBlocks {
		cur := queue[0]
		queue = queue[1:]
		for _, n := range neighbours6(cur) {
			if visited[n] {
				continue
			}
			if abs(n.X-pos.X) > spongeRadius || abs(n.Y-pos.Y) > spongeRadius || abs(n.Z-pos.Z) > spongeRadius {
				continue
			}
			visited[n] = true
			if i.World.GetBlock(n).Name != world.Water.Name {
				continue
			}
			removed = append(removed, world.BlockChange{Pos: n, Block: world.Air})
			queue = append(queue, n)
			if len(removed) >= spongeMaxBlocks {
				break
			}
		}
	}
	if len(removed) == 0 {
		return 0
	}
	removed = append(removed, world.BlockChange{Pos: pos, Block: world.WetSponge})
	i.SetBlocks(removed)
	return len(removed) - 1
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
