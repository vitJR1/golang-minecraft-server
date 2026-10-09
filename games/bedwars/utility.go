package bedwars

import (
	"math"
	"time"

	"minecraft-server/game"
	"minecraft-server/world"
)

// utility.go implements the shop's custom items. They are ordinary items
// recognised by their display name: the bridge egg and pop-up tower hook
// OnItemUse (right-click in the air), the potions hook OnItemConsume. TNT
// auto-prime, fireballs, golden apples, sponges, pearls and buckets are
// vanilla-shaped and live in the server core.

const (
	bridgeEggSpeed    = 1.2  // blocks/tick
	bridgeEggMaxRange = 30.0 // blocks from the throw point
	bridgeEggDrop     = 2    // blocks below the egg the bridge is laid
	popupTowerHeight  = 5
)

// potionEffects maps shop potion names to the effect they grant.
var potionEffects = map[string]struct {
	effect string
	level  int
	dur    time.Duration
}{
	nameSpeedPot: {"speed", 2, 45 * time.Second},
	nameJumpPot:  {"jump_boost", 5, 45 * time.Second},
	nameInvisPot: {"invisibility", 1, 30 * time.Second},
}

// OnItemConsume applies the shop potions by name. Anything else falls back
// to the server's vanilla defaults (golden apple heal, potion id).
func (g *bedWars) OnItemConsume(_ *game.Ctx, p game.PlayerHandle, st game.ItemStack) bool {
	pe, ok := potionEffects[st.Name]
	if !ok {
		return false
	}
	p.ApplyEffect(pe.effect, pe.level, pe.dur)
	return true
}

// OnItemUse launches a bridge egg or raises a pop-up tower. Both consume one
// unit of the held item and the click.
func (g *bedWars) OnItemUse(ctx *game.Ctx, p game.PlayerHandle, use game.ItemUse) bool {
	switch use.Name {
	case nameBridgeEgg:
		if g.takeHeld(p, use) {
			g.throwBridgeEgg(ctx, p)
		}
		return false
	case namePopupTower:
		ts, _, ok := g.playerState(p)
		if !ok {
			return false
		}
		if g.buildPopupTower(ctx, p, ts.team) {
			g.takeHeld(p, use)
		}
		return false
	}
	return true
}

// takeHeld removes one unit of the used stack from the hotbar slot.
func (g *bedWars) takeHeld(p game.PlayerHandle, use game.ItemUse) bool {
	slot := game.SlotHotbar0 + use.Slot
	for _, st := range p.Inventory() {
		if st.Slot != slot || st.Name != use.Name {
			continue
		}
		st.Count--
		if st.Count <= 0 {
			st = game.ItemStack{}
		}
		p.SetSlot(slot, st)
		return true
	}
	return false
}

// throwBridgeEgg fires an egg that lays a 3-wide path of team wool two
// blocks under its flight, over air only, within bridgeEggMaxRange.
func (g *bedWars) throwBridgeEgg(ctx *game.Ctx, p game.PlayerHandle) {
	team, ok := g.teamOf(p)
	if !ok {
		return
	}
	start := p.Pose()
	var lastX, lastZ float64 = start.X, start.Z
	first := true
	ctx.Instance.ThrowProjectile(p, "minecraft:egg", bridgeEggSpeed, game.ProjectileHooks{
		OnTick: func(x, y, z float64) bool {
			if math.Hypot(x-start.X, z-start.Z) > bridgeEggMaxRange {
				return false
			}
			dx, dz := x-lastX, z-lastZ
			lastX, lastZ = x, z
			if first {
				first = false
				return true // one step's travel is needed for the width direction
			}
			// Perpendicular to travel for the 3-wide deck.
			sx, sz := 0, 1
			if math.Abs(dx) < math.Abs(dz) {
				sx, sz = 1, 0
			}
			base := world.Position{X: floor(x), Y: floor(y) - bridgeEggDrop, Z: floor(z)}
			for _, off := range [][2]int{{0, 0}, {sx, sz}, {-sx, -sz}} {
				g.layBridgeBlock(ctx, world.Position{X: base.X + off[0], Y: base.Y, Z: base.Z + off[1]}, team.Wool)
			}
			return true
		},
	})
}

// layBridgeBlock places a bridge block into air and records it as
// player-placed so it can be broken.
func (g *bedWars) layBridgeBlock(ctx *game.Ctx, pos world.Position, blk world.Block) {
	if pos.Y < 0 || ctx.Instance.GetBlock(pos) != world.Air {
		return
	}
	g.mu.Lock()
	g.placed[pos] = true
	g.mu.Unlock()
	ctx.Instance.SetBlock(pos, blk)
}

// buildPopupTower raises a hollow 3×3 wool tower around the player: a floor
// under them, walls popupTowerHeight high, a ladder column in the middle and
// a standing ring on top, then lifts the player to the top. Refuses (and
// returns false) if any wall block would replace something that isn't air.
func (g *bedWars) buildPopupTower(ctx *game.Ctx, p game.PlayerHandle, team Team) bool {
	pose := p.Pose()
	cx, cz := floor(pose.X), floor(pose.Z)
	baseY := floor(pose.Y)
	ladder := ladderFacing(yawQuadrant(pose.Yaw))

	var changes []world.BlockChange
	wall := func(pos world.Position) bool {
		if ctx.Instance.GetBlock(pos) != world.Air {
			return false
		}
		changes = append(changes, world.BlockChange{Pos: pos, Block: team.Wool})
		return true
	}
	// Floor under the footprint (only where there's air).
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			pos := world.Position{X: cx + dx, Y: baseY - 1, Z: cz + dz}
			if ctx.Instance.GetBlock(pos) == world.Air {
				changes = append(changes, world.BlockChange{Pos: pos, Block: team.Wool})
			}
		}
	}
	// Walls on every level plus the standing ring on top.
	for dy := 0; dy <= popupTowerHeight; dy++ {
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				if dx == 0 && dz == 0 {
					continue
				}
				if !wall(world.Position{X: cx + dx, Y: baseY + dy, Z: cz + dz}) {
					return false
				}
			}
		}
	}
	// Ladder column up the middle (the player climbs out of it).
	for dy := 0; dy < popupTowerHeight; dy++ {
		changes = append(changes, world.BlockChange{Pos: world.Position{X: cx, Y: baseY + dy, Z: cz}, Block: ladder})
	}

	g.mu.Lock()
	for _, ch := range changes {
		g.placed[ch.Pos] = true
	}
	g.mu.Unlock()
	ctx.Instance.SetBlocks(changes)
	p.Teleport(float64(cx)+0.5, float64(baseY+popupTowerHeight), float64(cz)+0.5)
	return true
}

// ladderFacing returns a ladder block state whose rungs face the player's
// look direction (quadrant 0 = +Z … 3 = +X), so the ladder leans on the
// wall behind them.
func ladderFacing(quadrant int) world.Block {
	facing := []string{"north", "east", "south", "west"}[quadrant%4]
	id := world.ResolveStateID(world.Ladder.Name, map[string]string{"facing": facing})
	if id == 0 {
		return world.Ladder
	}
	return world.Block{StateID: id, Name: world.Ladder.Name}
}

// yawQuadrant maps a yaw to the facing: 0 = +Z, 1 = -X, 2 = -Z, 3 = +X.
func yawQuadrant(yaw float32) int {
	y := math.Mod(float64(yaw)+360+45, 360)
	return int(y/90) % 4
}

func floor(v float64) int { return int(math.Floor(v)) }
