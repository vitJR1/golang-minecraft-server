package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

func TestThrowableEntityID(t *testing.T) {
	cases := map[string]int32{
		"minecraft:egg":         eggEntityID,
		"minecraft:snowball":    snowballEntityID,
		"minecraft:ender_pearl": enderPearlEntityID,
	}
	for item, want := range cases {
		if id, ok := throwableEntityID(item); !ok || id != want {
			t.Errorf("%s: got %d,%v want %d", item, id, ok, want)
		}
	}
	if _, ok := throwableEntityID("minecraft:diamond"); ok {
		t.Error("diamond should not be throwable")
	}
}

// bareInstance builds an Instance without starting its tick loop, so tests can
// drive projectileTick deterministically.
func bareInstance(s *Server, w world.World) *Instance {
	return &Instance{Server: s, World: w, Players: NewPlayerList()}
}

func TestProjectileFliesThenExpires(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	p := &projectile{eid: 5, item: "minecraft:snowball", x: 0, y: 70, z: 0, vx: 1}
	inst.projectiles = []*projectile{p}

	inst.projectileTick(0)
	if len(inst.projectiles) != 1 {
		t.Fatalf("snowball should still be flying, have %d", len(inst.projectiles))
	}
	if p.x <= 0 {
		t.Errorf("snowball didn't move: x=%v", p.x)
	}
	if p.vy >= 0 {
		t.Errorf("gravity should pull vy negative: %v", p.vy)
	}
}

func TestProjectileImpactRemoves(t *testing.T) {
	w := world.NewMemoryWorld()
	w.SetBlock(world.Position{X: 2, Y: 70, Z: 0}, world.Stone)
	inst := bareInstance(New(), w)
	// Starts at x=1.5 moving +x → next step ~2.49 lands in the solid block.
	p := &projectile{eid: 6, item: "minecraft:egg", x: 1.5, y: 70.5, z: 0, vx: 1}
	inst.projectiles = []*projectile{p}

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
		outbound: make(chan outboundMsg, 16),
		done:     make(chan struct{}),
	}
	p := &projectile{eid: 7, item: "minecraft:ender_pearl", x: 1.5, y: 70.5, z: 0, vx: 1, thrower: thrower}
	inst.projectiles = []*projectile{p}

	inst.projectileTick(0)
	if len(inst.projectiles) != 0 {
		t.Fatalf("pearl should be removed on impact")
	}
	got := thrower.player.Snapshot()
	if got.X != 1.5 || got.Y != 70.5 || got.Z != 0 {
		t.Errorf("thrower not teleported to landing: got (%v,%v,%v)", got.X, got.Y, got.Z)
	}
}
