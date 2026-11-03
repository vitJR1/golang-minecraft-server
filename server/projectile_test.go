package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/server/gameplay/projectile"
	"minecraft-server/server/net/conn"
	"minecraft-server/world"
)

// bareInstance builds an Instance without starting its tick loop, so tests can
// drive projectileTick deterministically.
func bareInstance(s *Server, w world.World) *Instance {
	return &Instance{Server: s, World: w, Players: NewPlayerList()}
}

func TestProjectileFliesThenExpires(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	p := &projectileEntity{Flight: projectile.Flight{EID: 5, X: 0, Y: 70, Z: 0, VX: 1}, item: "minecraft:snowball"}
	inst.projectiles = []*projectileEntity{p}

	inst.projectileTick(0)
	if len(inst.projectiles) != 1 {
		t.Fatalf("snowball should still be flying, have %d", len(inst.projectiles))
	}
	if p.X <= 0 {
		t.Errorf("snowball didn't move: x=%v", p.X)
	}
	if p.VY >= 0 {
		t.Errorf("gravity should pull vy negative: %v", p.VY)
	}
}

func TestProjectileImpactRemoves(t *testing.T) {
	w := world.NewMemoryWorld()
	w.SetBlock(world.Position{X: 2, Y: 70, Z: 0}, world.Stone)
	inst := bareInstance(New(), w)
	// Starts at x=1.5 moving +x → next step ~2.49 lands in the solid block.
	p := &projectileEntity{Flight: projectile.Flight{EID: 6, X: 1.5, Y: 70.5, Z: 0, VX: 1}, item: "minecraft:egg"}
	inst.projectiles = []*projectileEntity{p}

	inst.projectileTick(0)
	if len(inst.projectiles) != 0 {
		t.Errorf("egg should be removed on block impact, have %d", len(inst.projectiles))
	}
}

func TestEnderPearlTeleportsThrower(t *testing.T) {
	w := world.NewMemoryWorld()
	w.SetBlock(world.Position{X: 2, Y: 70, Z: 0}, world.Stone)
	s := New()
	inst := bareInstance(s, w)
	thrower := &ClientConnection{
		server:   s,
		instance: inst,
		player:   player.New(1, "Thrower", [16]byte{}),
		link:     conn.NewQueued(16),
	}
	p := &projectileEntity{Flight: projectile.Flight{EID: 7, X: 1.5, Y: 70.5, Z: 0, VX: 1}, item: "minecraft:ender_pearl", thrower: thrower}
	inst.projectiles = []*projectileEntity{p}

	inst.projectileTick(0)
	if len(inst.projectiles) != 0 {
		t.Fatalf("pearl should be removed on impact")
	}
	got := thrower.player.Snapshot()
	if got.X != 1.5 || got.Y != 70.5 || got.Z != 0 {
		t.Errorf("thrower not teleported to landing: got (%v,%v,%v)", got.X, got.Y, got.Z)
	}
}

// TestExpiredPearlTeleportsNobody: a pearl that flew past the island and
// timed out over the void doesn't drag its thrower to where it vanished.
func TestExpiredPearlTeleportsNobody(t *testing.T) {
	s := New()
	inst := bareInstance(s, world.NewMemoryWorld())
	thrower := offlineConn(inst, "Thrower", player.Survival, 0, 70, 0)
	p := &projectileEntity{Flight: projectile.Flight{EID: 8, X: 1.5, Y: 70.5, Z: 0, VX: 1, MaxTicks: 1}, item: "minecraft:ender_pearl", thrower: thrower}
	inst.projectiles = []*projectileEntity{p}

	for tick := uint64(0); len(inst.projectiles) > 0 && tick < 10; tick++ {
		inst.projectileTick(tick)
	}
	if len(inst.projectiles) != 0 {
		t.Fatal("pearl never expired")
	}
	if got := thrower.player.Snapshot(); got.X != 0 || got.Y != 70 {
		t.Errorf("thrower moved by an expired pearl to (%v,%v)", got.X, got.Y)
	}
}
