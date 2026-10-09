package worldedit

import (
	"strings"
	"sync"
	"testing"
	"time"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/world"
)

// --- doubles ---------------------------------------------------------------

type fakePlayer struct {
	name     string
	op       bool
	x, y, z  float64
	messages []string
	given    map[string]int
}

func (p *fakePlayer) Name() string    { return p.name }
func (p *fakePlayer) EntityID() int32 { return 1 }
func (p *fakePlayer) Pose() player.Snapshot {
	return player.Snapshot{X: p.x, Y: p.y, Z: p.z}
}
func (p *fakePlayer) Teleport(x, y, z float64)    { p.x, p.y, p.z = x, y, z }
func (p *fakePlayer) SendMessage(text string)     { p.messages = append(p.messages, text) }
func (p *fakePlayer) SetGamemode(player.Gamemode) {}
func (p *fakePlayer) GiveItem(item string, n int) {
	if p.given == nil {
		p.given = map[string]int{}
	}
	p.given[item] += n
}
func (p *fakePlayer) Kick(string)                                      {}
func (p *fakePlayer) IsOp() bool                                       { return p.op }
func (p *fakePlayer) CountItem(string) int                             { return 0 }
func (p *fakePlayer) TakeItem(string, int) bool                        { return false }
func (p *fakePlayer) OpenMenu(string, int, []game.MenuItem, func(int)) {}
func (p *fakePlayer) GiveStack(st game.ItemStack) int                  { p.GiveItem(st.Item, st.Count); return 0 }
func (p *fakePlayer) SetSlot(int, game.ItemStack)                      {}
func (p *fakePlayer) ClearInventory()                                  {}
func (p *fakePlayer) Inventory() []game.ItemStack                      { return nil }
func (p *fakePlayer) Health() float32                                  { return player.MaxHealth }
func (p *fakePlayer) SetHealth(float32)                                {}
func (p *fakePlayer) ApplyEffect(string, int, time.Duration)           {}
func (p *fakePlayer) RemoveEffect(string)                              {}
func (p *fakePlayer) SendTitle(string, string, int, int, int)          {}
func (p *fakePlayer) PlaySound(string, float32, float32)               {}
func (p *fakePlayer) Respawn(float64, float64, float64)                {}
func (p *fakePlayer) Kill()                                            {}

type fakeInstance struct {
	mu     sync.Mutex
	blocks map[world.Position]world.Block
	bulk   int // SetBlocks calls
}

func newFakeInstance() *fakeInstance { return &fakeInstance{blocks: map[world.Position]world.Block{}} }

func (i *fakeInstance) ID() string { return "test" }
func (i *fakeInstance) SetBlock(p world.Position, b world.Block) {
	i.mu.Lock()
	i.blocks[p] = b
	i.mu.Unlock()
}
func (i *fakeInstance) SetBlocks(changes []world.BlockChange) {
	i.mu.Lock()
	i.bulk++
	for _, ch := range changes {
		if ch.Block == world.Air {
			delete(i.blocks, ch.Pos)
		} else {
			i.blocks[ch.Pos] = ch.Block
		}
	}
	i.mu.Unlock()
}
func (i *fakeInstance) GetBlock(p world.Position) world.Block {
	i.mu.Lock()
	defer i.mu.Unlock()
	if b, ok := i.blocks[p]; ok {
		return b
	}
	return world.Air
}
func (i *fakeInstance) BroadcastChat(string, string)                                  {}
func (i *fakeInstance) PlayerCount() int                                              { return 1 }
func (i *fakeInstance) Players() []game.PlayerHandle                                  { return nil }
func (i *fakeInstance) PlayerByName(string) (game.PlayerHandle, bool)                 { return nil, false }
func (i *fakeInstance) EndGame()                                                      {}
func (i *fakeInstance) SetPvP(bool)                                                   {}
func (i *fakeInstance) SetInstantRespawn(bool)                                        {}
func (i *fakeInstance) DropItem(float64, float64, float64, string, int) bool          { return true }
func (i *fakeInstance) DroppedItemsNear(_, _, _, _ float64, _ string) int             { return 0 }
func (i *fakeInstance) SpawnHologram(_, _, _ float64, _ string) game.Hologram         { return nil }
func (i *fakeInstance) SetCustomRespawn(bool)                                         {}
func (i *fakeInstance) SetWeaponDamage(bool)                                          {}
func (i *fakeInstance) SetTNTAutoPrime(bool)                                          {}
func (i *fakeInstance) PlaySound(string, float64, float64, float64, float32, float32) {}
func (i *fakeInstance) ThrowProjectile(game.PlayerHandle, string, float64, game.ProjectileHooks) {
}

func run(t *testing.T, ctx *game.Ctx, pl game.PlayerHandle, line string) {
	t.Helper()
	parts := strings.Fields(line)
	cmd, ok := game.LookupCommand(parts[0])
	if !ok {
		t.Fatalf("command %q not registered", parts[0])
	}
	cmd.Run(ctx, pl, parts[1:])
}

func fresh() (*game.Ctx, *fakeInstance, *fakePlayer) {
	we.mu.Lock()
	we.sessions = map[string]*session{}
	we.mu.Unlock()
	inst := newFakeInstance()
	return &game.Ctx{InstanceID: "test", Instance: inst}, inst, &fakePlayer{name: "Op", op: true, y: 64}
}

// --- tests -----------------------------------------------------------------

func TestWandSelectsAndConsumesClicks(t *testing.T) {
	ctx, _, pl := fresh()
	left := game.BlockInteraction{Pos: world.Position{X: 1, Y: 64, Z: 1}, Action: game.LeftClick, Item: Wand}
	right := game.BlockInteraction{Pos: world.Position{X: 3, Y: 66, Z: 3}, Action: game.RightClick, Item: Wand, Face: 1}
	if we.OnBlockInteract(ctx, pl, left) {
		t.Error("wand left click should be consumed")
	}
	if we.OnBlockInteract(ctx, pl, right) {
		t.Error("wand right click should be consumed")
	}
	lo, hi, ok := we.selection("Op")
	if !ok || lo != (world.Position{X: 1, Y: 64, Z: 1}) || hi != (world.Position{X: 3, Y: 66, Z: 3}) {
		t.Errorf("selection: %v %v %v", lo, hi, ok)
	}
	// Other items and non-ops pass through untouched.
	if !we.OnBlockInteract(ctx, pl, game.BlockInteraction{Pos: lo, Action: game.LeftClick, Item: "minecraft:stone"}) {
		t.Error("non-wand click must not be consumed")
	}
	if !we.OnBlockInteract(ctx, &fakePlayer{name: "Guest"}, left) {
		t.Error("non-op wand click must not be consumed")
	}
}

func TestSetReplaceUndo(t *testing.T) {
	ctx, inst, pl := fresh()
	pl.x, pl.z = 0, 0
	run(t, ctx, pl, "/pos1")
	pl.x, pl.y, pl.z = 2, 65, 2
	run(t, ctx, pl, "/pos2")

	run(t, ctx, pl, "/set stone")
	if inst.bulk != 1 {
		t.Errorf("SetBlocks calls: %d, want 1 (bulk API)", inst.bulk)
	}
	if got := inst.GetBlock(world.Position{X: 1, Y: 65, Z: 1}); got != world.Stone {
		t.Errorf("after //set: %+v", got)
	}
	if len(inst.blocks) != 18 {
		t.Errorf("filled %d blocks, want 3*2*3=18", len(inst.blocks))
	}

	run(t, ctx, pl, "/replace stone oak_planks")
	planks, _ := world.BlockByName("minecraft:oak_planks")
	if got := inst.GetBlock(world.Position{X: 2, Y: 64, Z: 0}); got != planks {
		t.Errorf("after //replace: %+v", got)
	}

	run(t, ctx, pl, "/undo")
	if got := inst.GetBlock(world.Position{X: 2, Y: 64, Z: 0}); got != world.Stone {
		t.Errorf("after first //undo: %+v, want stone", got)
	}
	run(t, ctx, pl, "/undo")
	if got := inst.GetBlock(world.Position{X: 2, Y: 64, Z: 0}); got != world.Air {
		t.Errorf("after second //undo: %+v, want air", got)
	}
	run(t, ctx, pl, "/undo")
	if last := pl.messages[len(pl.messages)-1]; !strings.Contains(last, "Nothing to undo") {
		t.Errorf("third undo: %q", last)
	}
}

func TestCopyPasteRelative(t *testing.T) {
	ctx, inst, pl := fresh()
	inst.SetBlock(world.Position{X: 5, Y: 64, Z: 5}, world.Stone)
	pl.x, pl.y, pl.z = 5, 64, 5
	run(t, ctx, pl, "/pos1")
	run(t, ctx, pl, "/pos2")
	pl.x, pl.z = 4, 4 // stand one block off: copy stores offset (+1,0,+1)
	run(t, ctx, pl, "/copy")
	pl.x, pl.y, pl.z = 10, 70, 10
	run(t, ctx, pl, "/paste")
	if got := inst.GetBlock(world.Position{X: 11, Y: 70, Z: 11}); got != world.Stone {
		t.Errorf("paste landed wrong: %+v (blocks %v)", got, inst.blocks)
	}
}

func TestParseBlock(t *testing.T) {
	if b, err := ParseBlock("stone"); err != nil || b != world.Stone {
		t.Errorf("stone: %+v %v", b, err)
	}
	if b, err := ParseBlock("minecraft:stone"); err != nil || b != world.Stone {
		t.Errorf("namespaced: %+v %v", b, err)
	}
	b, err := ParseBlock("oak_stairs[facing=south,half=top]")
	if err != nil {
		t.Fatal(err)
	}
	want := world.ResolveStateID("minecraft:oak_stairs", map[string]string{"facing": "south", "half": "top"})
	if b.StateID != want {
		t.Errorf("stairs state: %d, want %d", b.StateID, want)
	}
	if _, err := ParseBlock("not_a_block"); err == nil {
		t.Error("unknown block should error")
	}
}

func TestSelectionCap(t *testing.T) {
	ctx, inst, pl := fresh()
	run(t, ctx, pl, "/pos1")
	pl.x, pl.y, pl.z = 100, 100, 100
	run(t, ctx, pl, "/pos2")
	run(t, ctx, pl, "/set stone")
	if inst.bulk != 0 {
		t.Error("oversized selection must not be applied")
	}
	if last := pl.messages[len(pl.messages)-1]; !strings.Contains(last, "too big") {
		t.Errorf("expected size refusal, got %q", last)
	}
}

func TestCommandsRegisteredWithDoubleSlashNames(t *testing.T) {
	for _, name := range []string{"/wand", "/pos1", "/pos2", "/set", "/replace", "/undo", "/copy", "/paste", "/schem", "/size"} {
		if _, ok := game.LookupCommand(name); !ok {
			t.Errorf("%s not registered", name)
		}
	}
	if got := completeBlock(nil, nil, []string{"oak_st"}); len(got) == 0 || got[0] != "oak_stairs" {
		t.Errorf("complete: %v", got)
	}
}
