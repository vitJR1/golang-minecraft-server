package server

import (
	"bytes"
	"testing"
	"time"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

func TestEffectAPIAndActiveList(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := tntConn(inst, "Buffed", player.Survival, 0, 64, 0)
	c.applyEffect(EffectSpeed, 2, time.Minute)
	c.applyEffect(EffectJumpBoost, 1, time.Minute)
	if c.effectLevel(EffectSpeed) != 2 || c.effectLevel(EffectBlindness) != 0 {
		t.Errorf("levels: speed=%d blindness=%d", c.effectLevel(EffectSpeed), c.effectLevel(EffectBlindness))
	}
	active := c.activeEffects()
	if len(active) != 2 || active[0].Effect != EffectJumpBoost || active[1].Effect != EffectSpeed || active[1].Level != 2 {
		t.Errorf("active: %+v", active)
	}
	if active[1].Remaining <= 50*time.Second {
		t.Errorf("remaining should be close to a minute, got %v", active[1].Remaining)
	}
	// Entity Effect went to the client with the vanilla id.
	body := lastPacket(t, c, CbPlayEntityEffect)
	if body == nil {
		t.Fatal("no Entity Effect packet")
	}
	buf := bytes.NewBuffer(body)
	_, _ = protocol.ReadVarInt(buf)
	id, _ := protocol.ReadVarInt(buf)
	if int32(id) != EffectJumpBoost.ID { // last applied
		t.Errorf("effect id %d, want jump_boost %d", id, EffectJumpBoost.ID)
	}

	c.removeEffect(EffectSpeed)
	if c.effectLevel(EffectSpeed) != 0 || lastPacket(t, c, CbPlayRemoveEntityEffect) == nil {
		t.Error("removeEffect should end the effect and tell the client")
	}
	c.clearEffects()
	if len(c.activeEffects()) != 0 {
		t.Error("clearEffects should end everything")
	}
}

func TestRegenerationHealsOnTick(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := tntConn(inst, "Healer", player.Survival, 0, 64, 0)
	c.player.SetHealth(10)
	c.applyEffect(EffectRegeneration, 1, time.Minute)
	for tick := uint64(1); tick <= 100; tick++ {
		inst.effectsTick(tick)
	}
	if h := c.player.Health(); h != 12 {
		t.Errorf("Regen I over 100 ticks: health %v, want 12 (1 HP / 50 ticks)", h)
	}
	c.applyEffect(EffectRegeneration, 2, time.Minute)
	for tick := uint64(101); tick <= 150; tick++ {
		inst.effectsTick(tick)
	}
	if h := c.player.Health(); h != 14 {
		t.Errorf("Regen II over 50 ticks: health %v, want 14 (1 HP / 25 ticks)", h)
	}
	c.player.SetHealth(player.MaxHealth)
	for tick := uint64(151); tick <= 250; tick++ {
		inst.effectsTick(tick)
	}
	if c.player.Health() != player.MaxHealth {
		t.Error("regeneration must not exceed max health")
	}
}

func TestInvisibilityFlagAndExpirySweep(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	ghost := tntConn(inst, "Ghost", player.Survival, 0, 64, 0)
	watcher := tntConn(inst, "Watcher", player.Survival, 2, 64, 0)

	ghost.applyEffect(EffectInvisibility, 1, 30*time.Millisecond)
	meta := lastPacket(t, watcher, CbPlaySetEntityMetadata)
	if meta == nil {
		t.Fatal("watcher should get the invisible flag")
	}
	if flags := meta[len(meta)-2]; flags&0x20 == 0 {
		t.Errorf("flags %#x should have invisible (0x20)", flags)
	}

	time.Sleep(40 * time.Millisecond)
	inst.effectsTick(20) // sweep runs on multiples of 20
	if ghost.effectLevel(EffectInvisibility) != 0 {
		t.Error("expired effect should be gone")
	}
	if lastPacket(t, ghost, CbPlayRemoveEntityEffect) == nil {
		t.Error("expiry should tell the client the effect ended")
	}
	meta = lastPacket(t, watcher, CbPlaySetEntityMetadata)
	if meta == nil || meta[len(meta)-2]&0x20 != 0 {
		t.Errorf("watcher should see the invisible flag cleared, got %v", meta)
	}

	// Fire and invisibility share the flag byte.
	ghost.applyEffect(EffectInvisibility, 1, time.Minute)
	ghost.setFire(100)
	meta = lastPacket(t, watcher, CbPlaySetEntityMetadata)
	if flags := meta[len(meta)-2]; flags != 0x21 {
		t.Errorf("flags %#x, want fire|invisible 0x21", flags)
	}
}

func TestResendEffectsAndDeathClearsThem(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := tntConn(inst, "Resync", player.Survival, 0, 64, 0)
	c.applyEffect(EffectSpeed, 1, time.Minute)
	lastPacket(t, c, CbPlayEntityEffect) // drain
	c.resendEffects()
	if lastPacket(t, c, CbPlayEntityEffect) == nil {
		t.Error("resendEffects should re-send active effects")
	}
	inst.fireDeath(c, nil)
	if len(c.activeEffects()) != 0 {
		t.Error("death should clear effects")
	}
}
