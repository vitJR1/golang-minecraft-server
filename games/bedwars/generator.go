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

// Generator is one resource-spawn point on the map. TeamID < 0 means a
// neutral generator (the central diamond forge); otherwise it belongs to a
// team's island.
type Generator struct {
	Pos           world.Position
	Resource      Resource
	IntervalTicks uint64
	TeamID        int // -1 for neutral
}

// neutral marks a generator as map-shared rather than team-owned. Used by
// the arena builder for the central diamond forge.
const neutral = -1

// ResourceGranter receives the output of a generator tick. This is the
// Dependency-Inversion seam for the economy: the game loop depends on this
// interface, not on any concrete "give the player N iron" mechanism.
//
// Implementations decide what "granting" means:
//   - noopGranter: nothing (default — keeps the round running cleanly while
//     there is no inventory system to receive items).
//   - a future inventoryGranter: push an item stack to nearby teammates
//     once PlayerHandle exposes item-give.
//
// recipients is the set of players the grant should target (e.g. living
// members of the owning team for a team forge, or everyone for a neutral
// one); the granter is free to filter further (proximity, capacity, …).
type ResourceGranter interface {
	Grant(ctx *game.Ctx, g Generator, recipients []game.PlayerHandle)
}

// noopGranter is the default: generators tick but produce nothing visible.
// Swap in a real granter via WithGranter once item-give exists.
type noopGranter struct{}

func (noopGranter) Grant(*game.Ctx, Generator, []game.PlayerHandle) {}
