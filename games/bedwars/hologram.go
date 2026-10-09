package bedwars

import (
	"fmt"

	"minecraft-server/game"
)

// hologram.go puts a floating countdown over the shared resource generators
// (diamond / emerald), so players can see how soon the next unit appears.
// Two lines of floating text hover ~3 blocks above the generator block: the
// resource name and the timer. Team iron forges fire every few seconds and
// don't get one.

const (
	// hologramHeight is how far above the generator block the countdown
	// floats. The armor-stand name renders ~0.5 blocks above its position,
	// so the entity itself sits at hologramHeight-0.5.
	hologramHeight = 3.0
	nameTagOffset  = 0.5
	// hologramLineGap is the vertical distance between the title and the
	// timer line.
	hologramLineGap = 0.3
)

// genHologram is the pair of floating-text lines over one generator.
type genHologram struct {
	gen   Generator
	title game.Hologram
	timer game.Hologram
}

// timed reports whether a resource gets a countdown hologram.
func (r Resource) timed() bool { return r == Diamond || r == Emerald }

// title is the coloured first line of the hologram (§-colour codes).
func (r Resource) title() string {
	switch r {
	case Diamond:
		return "§b◆ Diamond"
	case Emerald:
		return "§a◆ Emerald"
	case Gold:
		return "§6◆ Gold"
	default:
		return "§f◆ Iron"
	}
}

// spawnHolograms creates the countdown holograms for every timed generator.
// Called once from OnInstanceStart; holograms are re-streamed to joining
// players by the server, so the game only has to update the text.
func (g *bedWars) spawnHolograms(ctx *game.Ctx) {
	var holos []genHologram
	for _, gen := range g.arena.Generators {
		if !gen.Resource.timed() || gen.IntervalTicks == 0 {
			continue
		}
		x, y, z := gen.dropPoint()
		base := y + hologramHeight - nameTagOffset
		title := ctx.Instance.SpawnHologram(x, base+hologramLineGap, z, gen.Resource.title())
		timer := ctx.Instance.SpawnHologram(x, base, z, timerText(gen, 0, false))
		if title == nil || timer == nil {
			continue
		}
		holos = append(holos, genHologram{gen: gen, title: title, timer: timer})
	}
	g.mu.Lock()
	g.holograms = holos
	g.mu.Unlock()
}

// updateHolograms refreshes every countdown line for the current tick. The
// server skips the broadcast when the text is unchanged, so this is cheap to
// call every tick.
func (g *bedWars) updateHolograms(ctx *game.Ctx, tick uint64) {
	g.mu.Lock()
	holos := g.holograms
	g.mu.Unlock()
	for _, h := range holos {
		x, y, z := h.gen.dropPoint()
		item := resourceItem[h.gen.Resource]
		full := ctx.Instance.DroppedItemsNear(x, y, z, dropCapRadius, item) >= h.gen.maxStack()
		h.timer.SetText(timerText(h.gen, tick, full))
	}
}

// timerText renders the countdown line: seconds until the generator next
// fires (on its IntervalTicks grid), or a "full" notice while the pile at the
// forge is at its cap and the generator idles.
func timerText(gen Generator, tick uint64, full bool) string {
	if full {
		return "§cFull"
	}
	remaining := gen.IntervalTicks - tick%gen.IntervalTicks
	secs := (remaining + 19) / 20
	return fmt.Sprintf("§eSpawns in §f%ds", secs)
}
