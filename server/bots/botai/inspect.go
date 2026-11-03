package botai

import (
	"fmt"

	"minecraft-server/anticheat"
	"minecraft-server/game"
	"minecraft-server/world"
)

// inspect.go: a read-only view of the bot's mind for the server's tests and
// for tracing (BOTTRACE). Call it from the tick goroutine (or between ticks):
// the brain isn't locked.

// Pos is where the bot's physics has it; OnGround whether it stands.
func (b *Bot) Pos() anticheat.Vec { return b.phys.Pos }
func (b *Bot) OnGround() bool     { return b.phys.OnGround }

// Role is the bot's part in its team.
func (b *Bot) Role() Role { return b.brain.role }

// Target is the enemy it's fighting (nil: none).
func (b *Bot) Target() Mob { return b.brain.target }

// Plan is the game's last plan for the bot.
func (b *Bot) Plan() game.BotPlan { return b.brain.plan }

// Retreated reports whether it has ever fallen back on low health.
func (b *Bot) Retreated() bool { return b.brain.retreatUntil > 0 }

// Searching reports whether a route search runs off the tick right now.
func (b *Bot) Searching() bool { return b.brain.far != nil && len(b.brain.far) == 0 }

// RouteAt is the tick the current route was searched (false: no route).
func (b *Bot) RouteAt() (uint64, bool) { return b.brain.routeAt, b.brain.route != nil }

// Digging is the block it's digging (false: none).
func (b *Bot) Digging() (world.Position, bool) {
	if d := b.brain.dig; d != nil {
		return d.pos, true
	}
	return world.Position{}, false
}

// FallSpots is how often the bot's team (or the bot) fell from each route cell.
func (b *Bot) FallSpots() map[world.Position]int { return b.fallSpots() }

// TeamTargets are its targets, the team's focus first.
func (b *Bot) TeamTargets() []world.Position { return b.teamTargets() }

// WantsWare is the gear it would buy now (nil: none).
func (b *Bot) WantsWare() *game.BotWare { return b.wantWare(b.body.CountItem(b.brain.plan.BlockItem)) }

// SetHop turns bunny-hopping on or off (comparisons in tests).
func (b *Bot) SetHop(on bool) { b.skill.hop = on }

// Trace is a one-line summary of what the bot is up to at tick now.
func (b *Bot) Trace(now uint64) string {
	br, p, n := &b.brain, b.phys.Pos, b.body.CountItem
	return fmt.Sprintf("pos=(%.1f,%.1f,%.1f) key=%q idx=%d/%d shop=%v save=%v hard=%v dig=%v wool=%d iron=%d gold=%d dia=%d em=%d tnt=%d role=%v ware=%v flee=%v",
		p.X, p.Y, p.Z, br.routeKey, br.routeIdx, len(br.route), br.shopping, now < br.farmUntil, br.hardCover, br.dig,
		n(br.plan.BlockItem), n("minecraft:iron_ingot"), n("minecraft:gold_ingot"), n("minecraft:diamond"), n("minecraft:emerald"), n(itemTNT), br.role,
		b.WantsWare(), now < br.fleeUntil)
}
