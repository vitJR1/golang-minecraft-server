package server

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
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
	_, a, v := duel(t)
	v.applyEffect(EffectSpeed, 1, time.Minute)
	lastPacket(t, v, CbPlayEntityEffect) // drain
	v.resendEffects()
	if lastPacket(t, v, CbPlayEntityEffect) == nil {
		t.Error("resendEffects should re-send active effects")
	}
	// A lethal hit runs die(), which ends every effect.
	v.player.SetHealth(1)
	a.handleAttack(v)
	if !v.player.IsDead() {
		t.Fatal("victim should have died")
	}
	if len(v.activeEffects()) != 0 {
		t.Error("death should clear effects")
	}
}

// readMovementAttribute parses an Update Attributes body and returns the
// generic.movement_speed modifiers as (uuid hex → amount).
func readMovementAttribute(t *testing.T, body []byte) map[string]float64 {
	t.Helper()
	buf := bytes.NewBuffer(body)
	_, _ = protocol.ReadVarInt(buf) // entity id
	if n, _ := protocol.ReadVarInt(buf); n != 1 {
		t.Fatalf("property count %d, want 1", n)
	}
	l, _ := protocol.ReadVarInt(buf)
	key := string(buf.Next(int(l)))
	if key != movementSpeedAttribute {
		t.Fatalf("attribute %q, want %q", key, movementSpeedAttribute)
	}
	var base float64
	_ = binary.Read(buf, binary.BigEndian, &base)
	if base != playerBaseMovementSpeed {
		t.Errorf("base %v, want %v", base, playerBaseMovementSpeed)
	}
	n, _ := protocol.ReadVarInt(buf)
	mods := map[string]float64{}
	for i := 0; i < int(n); i++ {
		uuid := hex.EncodeToString(buf.Next(16))
		var amount float64
		_ = binary.Read(buf, binary.BigEndian, &amount)
		op, _ := buf.ReadByte()
		if op != attributeOpMultiplyTotal {
			t.Errorf("modifier %s: op %d, want multiply_total", uuid, op)
		}
		mods[uuid] = amount
	}
	if buf.Len() != 0 {
		t.Errorf("%d trailing bytes", buf.Len())
	}
	return mods
}

func TestSpeedEffectSyncsMovementAttribute(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := tntConn(inst, "Runner", player.Survival, 0, 64, 0)
	speedUUID := hex.EncodeToString(speedModifierUUID)
	sprintUUID := hex.EncodeToString(sprintModifierUUID)

	// Speed II → one modifier of 0.4 (0.2 × amplifier+1); the icon packet
	// alone leaves the 1.20.1 client at normal speed.
	c.applyEffect(EffectSpeed, 2, time.Minute)
	body := lastPacket(t, c, CbPlayUpdateAttributes)
	if body == nil {
		t.Fatal("Speed must send Update Attributes")
	}
	mods := readMovementAttribute(t, body)
	if len(mods) != 1 || mods[speedUUID] != 0.4 {
		t.Errorf("Speed II modifiers: %v, want {%s: 0.4}", mods, speedUUID)
	}

	// Sprinting players keep their sprint boost in the sync, else the
	// client drops it along with the modifiers it replaces.
	c.sprinting.Store(true)
	c.applyEffect(EffectSlowness, 1, time.Minute)
	mods = readMovementAttribute(t, lastPacket(t, c, CbPlayUpdateAttributes))
	if len(mods) != 3 || mods[hex.EncodeToString(slownessModifierUUID)] != -0.15 || mods[sprintUUID] != sprintBoost {
		t.Errorf("Speed II + Slowness I sprinting: %v", mods)
	}

	// Removal syncs an empty list (plus the sprint boost).
	c.sprinting.Store(false)
	c.removeEffect(EffectSpeed)
	c.removeEffect(EffectSlowness)
	mods = readMovementAttribute(t, lastPacket(t, c, CbPlayUpdateAttributes))
	if len(mods) != 0 {
		t.Errorf("after removal: %v, want none", mods)
	}

	// Unrelated effects don't touch the attribute.
	c.applyEffect(EffectJumpBoost, 1, time.Minute)
	if lastPacket(t, c, CbPlayUpdateAttributes) != nil {
		t.Error("jump boost must not resend movement attributes")
	}

	// Expiry through the sweep syncs too.
	c.applyEffect(EffectSpeed, 1, time.Millisecond)
	_ = lastPacket(t, c, CbPlayUpdateAttributes)
	time.Sleep(5 * time.Millisecond)
	inst.effectsTick(20)
	if lastPacket(t, c, CbPlayUpdateAttributes) == nil {
		t.Error("expired Speed must resync movement attributes")
	}
}
