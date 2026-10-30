package world

import (
	"math"
	"strings"
)

// Beds are two blocks (foot + head) sharing a facing, not two identical foot
// blocks. The foot lands where the player clicked; the head extends one block
// in the direction the player is facing.
//
// Every bed colour has the same state layout (from blocks.json), so we don't
// need a per-colour variant table — just the block's default state id:
//
//	properties: facing[north,south,west,east], occupied[true,false], part[head,foot]
//	strides:    facing=4, occupied=2, part=1
//	defaultState = min + facing(north=0)*4 + occupied(false=1)*2 + part(foot=1) = min + 3
//
// so min = default - 3, and for a given facing index:
//
//	foot (occupied=false, part=foot) = min + facing*4 + 3
//	head (occupied=false, part=head) = min + facing*4 + 2
const (
	BedDefaultOffset = 3 // defaultState - minStateID for any bed
	BedFootOffset    = 3 // within a facing group: occupied=false, part=foot
	BedHeadOffset    = 2 // within a facing group: occupied=false, part=head
)

// BedFromItem returns the bed block for a held item id ("minecraft:red_bed"),
// and whether the item is a bed at all.
func BedFromItem(item string) (Block, bool) {
	if !strings.HasSuffix(item, "_bed") {
		return Block{}, false
	}
	return BlockByName(item)
}

// BedFacing maps a player yaw to the bed's facing index (matching the
// facing[north,south,west,east] order) and the (dx,dz) step from foot to head
// — the head extends in the direction the player is looking.
func BedFacing(yaw float32) (facingIdx int32, dx, dz int) {
	bucket := (int(math.Floor(float64(yaw)/90+0.5))%4 + 4) % 4
	switch bucket {
	case 0: // looking south (+Z)
		return 1, 0, 1
	case 1: // looking west (-X)
		return 2, -1, 0
	case 2: // looking north (-Z)
		return 0, 0, -1
	default: // looking east (+X)
		return 3, 1, 0
	}
}

// BedPartner returns the other half of a bed block, derived from its state
// (facing + part), and whether blk is a bed at all.
func BedPartner(pos Position, blk Block) (Position, bool) {
	base, isBed := BedFromItem(blk.Name)
	if !isBed {
		return pos, false
	}
	offset := blk.StateID - (base.StateID - BedDefaultOffset)
	facing, part := offset/4, offset%4
	var dx, dz int
	switch facing {
	case 0:
		dz = -1 // north
	case 1:
		dz = 1 // south
	case 2:
		dx = -1 // west
	case 3:
		dx = 1 // east
	default:
		return pos, false
	}
	if part == BedHeadOffset || part == 0 { // head (occupied or not)
		dx, dz = -dx, -dz
	}
	return Position{X: pos.X + dx, Y: pos.Y, Z: pos.Z + dz}, true
}
