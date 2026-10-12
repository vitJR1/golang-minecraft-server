package server

import (
	"math"
	"testing"
	"time"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// absorptionOf parses a Set Entity Metadata body that carries only the
// Player "Additional Hearts" field (index 15, Float) and returns the value.
func absorptionOf(t *testing.T, body []byte) float32 {
	t.Helper()
	if body == nil {
		t.Fatal("no metadata packet")
	}
	_, n, err := protocol.ReadVarIntFromBytes(body) // entity id
	if err != nil {
		t.Fatal(err)
	}
	rest := body[n:]
	if rest[0] != playerMetaAbsorption || rest[1] != 3 {
		t.Fatalf("metadata index/type = %d/%d, want 15/Float", rest[0], rest[1])
	}
	bits := uint32(rest[2])<<24 | uint32(rest[3])<<16 | uint32(rest[4])<<8 | uint32(rest[5])
	if rest[6] != 0xFF {
		t.Fatalf("metadata not terminated: % x", rest)
	}
	return math.Float32frombits(bits)
}

// TestAbsorptionEffectSyncsYellowHearts: granting Absorption fills the
// pool (4 HP per level), pushes it as player metadata to the player's own
// client and to watchers, and expiry empties it again.
func TestAbsorptionEffectSyncsYellowHearts(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := tntConn(inst, "Golden", player.Survival, 0, 64, 0)
	w := tntConn(inst, "Watcher", player.Survival, 2, 64, 0)

	c.applyEffect(EffectAbsorption, 1, 30*time.Millisecond)
	if a := c.player.Absorption(); a != 4 {
		t.Fatalf("absorption after level I: %v, want 4", a)
	}
	if got := absorptionOf(t, lastPacket(t, c, CbPlaySetEntityMetadata)); got != 4 {
		t.Errorf("own client got absorption %v, want 4", got)
	}
	if got := absorptionOf(t, lastPacket(t, w, CbPlaySetEntityMetadata)); got != 4 {
		t.Errorf("watcher got absorption %v, want 4", got)
	}

	// Re-applying at a lower level never shrinks a bigger pool.
	c.player.SetAbsorption(8)
	c.applyEffect(EffectAbsorption, 1, time.Minute)
	if a := c.player.Absorption(); a != 8 {
		t.Errorf("re-apply shrank the pool to %v", a)
	}
	c.removeEffect(EffectAbsorption)
	if a := c.player.Absorption(); a != 0 {
		t.Errorf("removing the effect should empty the pool, got %v", a)
	}
	if got := absorptionOf(t, lastPacket(t, c, CbPlaySetEntityMetadata)); got != 0 {
		t.Errorf("own client after removal: %v, want 0", got)
	}

	// Natural expiry through the sweep.
	c.applyEffect(EffectAbsorption, 2, 30*time.Millisecond)
	if a := c.player.Absorption(); a != 8 {
		t.Fatalf("level II pool: %v, want 8", a)
	}
	drain(c)
	time.Sleep(40 * time.Millisecond)
	inst.effectsTick(20)
	if a := c.player.Absorption(); a != 0 {
		t.Errorf("expiry should empty the pool, got %v", a)
	}
	if got := absorptionOf(t, lastPacket(t, c, CbPlaySetEntityMetadata)); got != 0 {
		t.Errorf("own client after expiry: %v, want 0", got)
	}
}

// TestDamageDrainsAbsorptionAndSyncs: environmental damage eats the yellow
// hearts first, leaves health alone, and the health sync carries the new
// absorption value; resyncView re-pushes it after a Respawn.
func TestDamageDrainsAbsorptionAndSyncs(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	inst.combatEnabled.Store(true)
	c := tntConn(inst, "Burnt", player.Survival, 0, 64, 0)
	c.applyEffect(EffectAbsorption, 1, time.Minute)
	drain(c)

	c.hurtEnvironment(3, 0, 10)
	if h := c.player.Health(); h != player.MaxHealth {
		t.Errorf("health after a hit inside the pool: %v, want full", h)
	}
	if a := c.player.Absorption(); a != 1 {
		t.Errorf("absorption after 3 damage: %v, want 1", a)
	}
	if got := absorptionOf(t, lastPacket(t, c, CbPlaySetEntityMetadata)); got != 1 {
		t.Errorf("client absorption after the hit: %v, want 1", got)
	}

	// Nothing changed → no metadata spam on a plain health sync.
	_ = c.sendSetHealth(c.player.Health())
	if lastPacket(t, c, CbPlaySetEntityMetadata) != nil {
		t.Error("unchanged absorption must not be re-sent")
	}

	// resendEffects (resyncView after Respawn) forces the value out again.
	c.resendEffects()
	if got := absorptionOf(t, lastPacket(t, c, CbPlaySetEntityMetadata)); got != 1 {
		t.Errorf("resend after respawn: %v, want 1", got)
	}

	// Death clears effects and the pool with them.
	c.hurtEnvironment(100, 0, 50)
	if !c.player.IsDead() {
		t.Fatal("100 damage should kill")
	}
	if a := c.player.Absorption(); a != 0 {
		t.Errorf("death should empty the pool, got %v", a)
	}
}
