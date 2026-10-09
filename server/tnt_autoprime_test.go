package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

func TestTNTAutoPrimeOnPlacement(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	inst.World.SetBlock(world.Position{X: 0, Y: 63, Z: 0}, world.Stone)
	inst.SetTNTAutoPrime(true)
	c := offlineConn(inst, "Demo", player.Survival, 3.5, 64, 0.5)
	hold(c, "minecraft:tnt")

	placeOn(t, c, world.Position{X: 0, Y: 63, Z: 0}, 1) // top face → (0,64,0)
	inst.tntMu.Lock()
	n := len(inst.tnts)
	inst.tntMu.Unlock()
	if n != 1 {
		t.Fatalf("placed TNT should be primed, %d primed", n)
	}
	if got := inst.World.GetBlock(world.Position{X: 0, Y: 64, Z: 0}); got == world.TNT {
		t.Error("primed TNT should have left the block grid")
	}
}
