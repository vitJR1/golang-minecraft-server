package server

import (
	"testing"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/world"
)

// TestGameHookPanicIsContained: a panicking game hook costs the event, not
// the process — vetoes deny, chat is dropped, a consume isn't handled.
func TestGameHookPanicIsContained(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Victim", player.Survival, 0, 64, 0)
	boom := func() { panic("game bug") }
	inst.OnBlockBreak = func(*ClientConnection, world.Position) bool { boom(); return true }
	inst.OnBlockPlace = func(*ClientConnection, world.Position, world.Block) bool { boom(); return true }
	inst.OnPlayerAttack = func(_, _ *ClientConnection) bool { boom(); return true }
	inst.OnItemUse = func(*ClientConnection, game.ItemUse) bool { boom(); return true }
	inst.OnChat = func(*ClientConnection, string) (string, bool) { boom(); return "", true }
	inst.OnItemConsume = func(*ClientConnection, inventory.Stack) bool { boom(); return true }
	inst.OnRewritePlace = func(*ClientConnection, world.Position, world.Block) world.Block { boom(); return world.Air }

	if inst.allowBlockBreak(c, world.Position{}) {
		t.Error("a panicking OnBlockBreak must deny")
	}
	if inst.allowBlockPlace(c, world.Position{}, world.Stone) {
		t.Error("a panicking OnBlockPlace must deny")
	}
	if inst.allowAttack(c, c) {
		t.Error("a panicking OnPlayerAttack must deny")
	}
	if inst.allowItemUse(c, game.ItemUse{}) {
		t.Error("a panicking OnItemUse must deny")
	}
	if _, ok := inst.filterChat(c, "hi"); ok {
		t.Error("a panicking OnChat must drop the line")
	}
	if inst.fireItemConsume(c, inventory.Stack{}) {
		t.Error("a panicking OnItemConsume must not count as handled")
	}
	if got := inst.rewritePlacedBlock(c, world.Position{}, world.Stone); got != world.Stone {
		t.Errorf("a panicking rewrite must keep the block, got %v", got)
	}
}
