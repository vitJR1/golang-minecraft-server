package botai

import (
	"math/rand"
	"testing"

	"minecraft-server/anticheat"
	"minecraft-server/server/bots/botnav"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/world"
)

// stubWorld is a World of stone below y=64 plus the blocks set; anything
// else it's asked panics (the embedded nil World).
type stubWorld struct {
	World
	blocks map[world.Position]world.Block
}

func (w *stubWorld) Block(p world.Position) world.Block {
	if b, ok := w.blocks[p]; ok {
		return b
	}
	if p.Y < 64 {
		return world.Stone
	}
	return world.Air
}

func (w *stubWorld) BlockedByPlayer(world.Position) bool { return false }

// stubBody is a bot's body with an empty inventory in w; broken counts the
// blocks it broke.
type stubBody struct {
	Body
	w      *stubWorld
	feet   anticheat.Vec
	broken int
}

func (b *stubBody) World() World                                  { return b.w }
func (b *stubBody) Feet() anticheat.Vec                           { return b.feet }
func (b *stubBody) Eye() anticheat.Vec                            { return anticheat.Eye(b.feet, false) }
func (b *stubBody) Slot(int16) inventory.Stack                    { return inventory.Stack{} }
func (b *stubBody) HeldSlot() int32                               { return 0 }
func (b *stubBody) HeldItem() string                              { return "" }
func (b *stubBody) CountItem(string) int                          { return 0 }
func (b *stubBody) DigContext() world.DigContext                  { return world.DigContext{OnGround: true} }
func (b *stubBody) Swing()                                        {}
func (b *stubBody) Break(p world.Position)                        { b.w.blocks[p] = world.Air; b.broken++ }
func (b *stubBody) SelectSlot(int32)                              {}
func (b *stubBody) SwapSlots(int16, int16)                        {}
func (b *stubBody) Self() Mob                                     { return nil }
func (b *stubBody) SameWorld(Mob) bool                            { return false }
func (b *stubBody) Look(float32, float32, bool)                   {}
func (b *stubBody) SetSprinting(bool)                             {}
func (b *stubBody) Place(world.Position) bool                     { return false }
func (b *stubBody) StartDraw() bool                               { return false }
func (b *stubBody) CancelUse()                                    {}
func (b *stubBody) Release()                                      {}
func (b *stubBody) Closed() bool                                  { return false }
func (b *stubBody) FlightExempt() bool                            { return false }
func (b *stubBody) SpeedLevel() int                               { return 0 }
func (b *stubBody) JumpLevel() int                                { return 0 }
func (b *stubBody) Attack(Mob)                                    {}
func (b *stubBody) RoleChanged()                                  {}
func (b *stubBody) Move(_, _, _ float64, _, _ float32, _, _ bool) {}

// stubBot is a hard bot standing (on the ground) at feet in w.
func stubBot(w *stubWorld, feet anticheat.Vec) (*Bot, *stubBody) {
	body := &stubBody{w: w, feet: feet}
	b := New(body, DifficultyHard, rand.New(rand.NewSource(1)))
	b.phys.Pos, b.phys.OnGround = feet, true
	return b, body
}

// TestDigLosesProgressWhenLeftOff: a dig left off (a fight, the shop) and
// taken up again later starts over — it used to come back long "done" and
// break the block at once.
func TestDigLosesProgressWhenLeftOff(t *testing.T) {
	planks, _ := world.BlockByName("minecraft:oak_planks")
	pos := world.Position{X: 2, Y: 64, Z: 0}
	w := &stubWorld{blocks: map[world.Position]world.Block{pos: planks}}
	b, body := stubBot(w, anticheat.Vec{X: 0.5, Y: 64, Z: 0.5})
	ticks := uint64(world.BreakTicksWith(world.BreakInfoFor(planks.Name), "", body.DigContext()))
	if ticks < 10 {
		t.Fatalf("planks by hand take %d ticks: too quick for this test", ticks)
	}

	b.digBlock(100, pos, pos) // starts, then walks off
	now := 100 + ticks + 50
	b.digBlock(now, pos, pos) // back at it
	if body.broken != 0 {
		t.Fatal("the block broke at once on coming back to it")
	}
	for k := uint64(1); k <= ticks && body.broken == 0; k++ {
		b.digBlock(now+k, pos, pos)
	}
	if body.broken != 1 {
		t.Fatalf("a dig kept up for %d ticks didn't break the block", ticks)
	}
}

// TestResumeBesideTheRoute: knocked off a far route to the cell beside a
// step, the bot walks back onto that step and carries on — it used to drop
// the resumed route at once (the step before wasn't where it stood) and
// search anew.
func TestResumeBesideTheRoute(t *testing.T) {
	w := &stubWorld{blocks: map[world.Position]world.Block{}}
	b, _ := stubBot(w, anticheat.Vec{X: 2.5, Y: 64, Z: 1.5}) // beside step 1
	br := &b.brain
	route := []botnav.Step{
		{Cell: world.Position{X: 1, Y: 64, Z: 0}},
		{Cell: world.Position{X: 2, Y: 64, Z: 0}},
		{Cell: world.Position{X: 3, Y: 64, Z: 0}},
		{Cell: world.Position{X: 4, Y: 64, Z: 0}},
	}
	br.lastRoute, br.lastKey = route, "k"
	goal := func(p world.Position) bool { return p == route[3].Cell }

	in, status := b.goToFar(10, "k", route[3].Cell, goal, 0)
	if status != farReady || br.route == nil {
		t.Fatalf("status %d, route %v: not resumed", status, br.route)
	}
	if br.far != nil {
		t.Fatal("searched anew instead of resuming")
	}
	if in.DirZ >= 0 || in.DirX < 0 {
		t.Fatalf("input %+v: not heading back onto step (2,64,0)", in)
	}
	// Next tick, still beside it: on the route, not knocked off.
	b.goToFar(11, "k", route[3].Cell, goal, 0)
	if br.route == nil {
		t.Fatal("the resumed route was dropped again")
	}
}

// TestHopSkipNeedsTheBlockDown: feet in the cell of a bridge step whose
// block isn't down (the bot on the edge of the block before) isn't landing
// on that step — skipping it left the bridge unbuilt and the bot walked on
// into the void.
func TestHopSkipNeedsTheBlockDown(t *testing.T) {
	gap := world.Position{X: 3, Y: 63, Z: 0}
	w := &stubWorld{blocks: map[world.Position]world.Block{gap: world.Air, gap.Add(1, 0, 0): world.Air}}
	b, _ := stubBot(w, anticheat.Vec{X: 3.1, Y: 64, Z: 0.5}) // hitbox still on (2,63,0)
	br := &b.brain
	br.route = []botnav.Step{
		{Cell: world.Position{X: 1, Y: 64, Z: 0}},
		{Cell: world.Position{X: 2, Y: 64, Z: 0}},
		{Cell: world.Position{X: 3, Y: 64, Z: 0}, Place: []world.Position{gap}},
		{Cell: world.Position{X: 4, Y: 64, Z: 0}, Place: []world.Position{gap.Add(1, 0, 0)}},
	}
	br.routeStart, br.routeKey = world.Position{X: 0, Y: 64, Z: 0}, "k"
	b.follow(10)
	if br.route != nil && br.routeIdx > 2 {
		t.Fatalf("skipped to step %d past the unbuilt step 2", br.routeIdx)
	}
}
