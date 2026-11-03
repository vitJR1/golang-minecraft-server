package fluid

import (
	"math"

	"minecraft-server/world"
)

// Buckets: which fluid a bucket holds, and the look ray that finds what a
// bucket is used on (the vanilla client sends a plain Use Item for one).

const (
	BucketEmpty = "minecraft:bucket"
	BucketWater = "minecraft:water_bucket"
	BucketLava  = "minecraft:lava_bucket"

	bucketReach = 5.0 // blocks (creative reach; survival's 4.5 is close enough)
	bucketStep  = 0.05
)

// BucketKind maps a filled bucket item to the fluid it holds.
func BucketKind(item string) (*Kind, bool) {
	switch item {
	case BucketWater:
		return Water, true
	case BucketLava:
		return Lava, true
	}
	return nil, false
}

// RayToPlacement walks the ray from (ox,oy,oz) along (dx,dy,dz) through air
// and fluids until it meets a solid block, returning the last block position
// the ray occupied before the hit. false when nothing solid is within reach.
// Only an air or fluid cell is ever returned: a block in the eye cell (a
// ladder, torch or sign the player stands in) is passed through, never
// filled — the fluid would overwrite it without a drop.
func RayToPlacement(at func(world.Position) world.Block, ox, oy, oz, dx, dy, dz float64) (world.Position, bool) {
	eye := world.Position{X: floorF(ox), Y: floorF(oy), Z: floorF(oz)}
	var last world.Position
	haveLast := false
	for d := 0.0; d <= bucketReach; d += bucketStep {
		pos := world.Position{X: floorF(ox + dx*d), Y: floorF(oy + dy*d), Z: floorF(oz + dz*d)}
		b := at(pos)
		if _, _, isFluid := Of(b); b != world.Air && !isFluid {
			if pos == eye {
				continue // the block the eyes are in
			}
			return last, haveLast
		}
		last, haveLast = pos, true
	}
	return world.Position{}, false
}

// RayToSource walks the ray until it meets a fluid source block.
func RayToSource(at func(world.Position) world.Block, ox, oy, oz, dx, dy, dz float64) (world.Position, *Kind, bool) {
	for d := 0.0; d <= bucketReach; d += bucketStep {
		pos := world.Position{X: floorF(ox + dx*d), Y: floorF(oy + dy*d), Z: floorF(oz + dz*d)}
		b := at(pos)
		if b == world.Air {
			continue
		}
		kind, level, isFluid := Of(b)
		if !isFluid {
			return world.Position{}, nil, false // a solid block is in the way
		}
		if level == 0 {
			return pos, kind, true
		}
	}
	return world.Position{}, nil, false
}

// BucketFor is the filled bucket holding kind.
func BucketFor(kind *Kind) string {
	if kind == Lava {
		return BucketLava
	}
	return BucketWater
}

func floorF(v float64) int { return int(math.Floor(v)) }
