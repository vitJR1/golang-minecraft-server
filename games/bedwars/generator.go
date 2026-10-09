package bedwars

import (
	"minecraft-server/game"
	"minecraft-server/world"
)

// Resource is a generated currency type. The shop economy isn't built yet
// (the server has no plugin-facing inventory-give), so this is the seam:
// generators tick and hand a Resource to a ResourceGranter, and a real
// granter can be plugged in later without touching the game loop.
type Resource int

const (
	Iron Resource = iota
	Gold
	Diamond
	Emerald
)

func (r Resource) String() string {
	switch r {
	case Gold:
		return "Gold"
	case Diamond:
		return "Diamond"
	case Emerald:
		return "Emerald"
	default:
		return "Iron"
	}
}

// resourceItem maps each Resource to the namespaced Minecraft item handed
// to players when a generator fires. Anything missing here is skipped by the
// granter (no item given) rather than guessed.
var resourceItem = map[Resource]string{
	Iron:    "minecraft:iron_ingot",
	Gold:    "minecraft:gold_ingot",
	Diamond: "minecraft:diamond",
	Emerald: "minecraft:emerald",
}

// Generator is one resource-spawn point on the map. TeamID < 0 means a
// neutral generator (the central diamond forge); otherwise it belongs to a
// team's island.
type Generator struct {
	Pos           world.Position
	Resource      Resource
	IntervalTicks uint64
	TeamID        int // -1 for neutral
	// MaxStack caps how many units may lie uncollected at the spawn point;
	// the generator idles while the pile is at the cap. 0 = the per-resource
	// default (defaultGenMaxStack).
	MaxStack int
}

// defaultGenMaxStack is the uncollected-pile cap per resource when the
// config doesn't set one: cheap resources pile high, rare ones barely.
func defaultGenMaxStack(r Resource) int {
	switch r {
	case Gold:
		return 16
	case Diamond:
		return 8
	case Emerald:
		return 4
	default:
		return 48
	}
}

// maxStack resolves the pile cap (config value or resource default).
func (g Generator) maxStack() int {
	if g.MaxStack > 0 {
		return g.MaxStack
	}
	return defaultGenMaxStack(g.Resource)
}

// dropX/dropY/dropZ is where the generator's output appears: the centre of
// its block position, at that block's floor level (the item then falls onto
// whatever is underneath).
func (g Generator) dropPoint() (x, y, z float64) {
	return float64(g.Pos.X) + 0.5, float64(g.Pos.Y), float64(g.Pos.Z) + 0.5
}

// neutral marks a generator as map-shared rather than team-owned. Used by
// the arena builder for the central diamond forge.
const neutral = -1

// ResourceGranter receives the output of a generator tick. This is the
// Dependency-Inversion seam for the economy: the game loop depends on this
// interface, not on any concrete "give the player N iron" mechanism.
//
// Implementations decide what "granting" means:
//   - dropGranter (default): spawn the resource as a dropped item at the
//     generator block, so players collect it at the forge.
//   - inventoryGranter: push the unit straight into recipients' inventories.
//   - noopGranter: nothing (silent arena / tests).
//
// recipients is the set of players the grant should target (e.g. living
// members of the owning team for a team forge, or everyone for a neutral
// one); the granter is free to filter further (proximity, capacity, …).
type ResourceGranter interface {
	Grant(ctx *game.Ctx, g Generator, recipients []game.PlayerHandle)
}

// noopGranter is the inert fallback: generators tick but produce nothing.
// Kept for tests and for arenas that explicitly want a silent economy.
type noopGranter struct{}

func (noopGranter) Grant(*game.Ctx, Generator, []game.PlayerHandle) {}

// dropGranter is the live economy: each generator tick spawns one unit of its
// resource as a dropped-item entity at the generator's block (the "forge"),
// where players have to walk over it — vanilla BedWars. The pile at the forge
// is capped by Generator.MaxStack so an unattended base doesn't flood. This
// is the default granter wired into the BedWars Definition factories; the
// recipients list is ignored (anyone standing at the forge collects).
type dropGranter struct{}

// dropCapRadius is how far around the drop point DroppedItemsNear looks when
// deciding whether the forge pile is full.
const dropCapRadius = 1.5

func (dropGranter) Grant(ctx *game.Ctx, g Generator, _ []game.PlayerHandle) {
	item, ok := resourceItem[g.Resource]
	if !ok {
		return
	}
	x, y, z := g.dropPoint()
	if ctx.Instance.DroppedItemsNear(x, y, z, dropCapRadius, item) >= g.maxStack() {
		return
	}
	ctx.Instance.DropItem(x, y, z, item, 1)
}

// inventoryGranter hands each generator tick straight into every recipient's
// inventory via PlayerHandle.GiveItem — no walking to the forge. Kept as an
// opt-in (WithGranter) for arenas that want the old "auto-collect" economy.
type inventoryGranter struct{}

func (inventoryGranter) Grant(_ *game.Ctx, g Generator, recipients []game.PlayerHandle) {
	item, ok := resourceItem[g.Resource]
	if !ok {
		return
	}
	for _, p := range recipients {
		p.GiveItem(item, 1)
	}
}
