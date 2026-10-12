package server

import (
	"bytes"
	"testing"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

func TestDrawForceCurve(t *testing.T) {
	if f := drawForce(5); f < 0.18 || f > 0.19 { // t=0.25 → (0.0625+0.5)/3
		t.Errorf("5 ticks: %v", f)
	}
	if f := drawForce(20); f != 1 {
		t.Errorf("20 ticks: %v, want 1 (full)", f)
	}
	if f := drawForce(100); f != 1 {
		t.Errorf("overdraw must cap at 1, got %v", f)
	}
	if arrowDamage(3, 0, false) != 6 || arrowDamage(3, 1, false) != 9 || arrowDamage(3, 0, true) != 9 {
		t.Errorf("damage: %v %v %v", arrowDamage(3, 0, false), arrowDamage(3, 1, false), arrowDamage(3, 0, true))
	}
}

// archery sets up a shooter looking +Z at a target 6 blocks ahead.
func archery(t *testing.T, arrows int) (*Instance, *ClientConnection, *ClientConnection) {
	t.Helper()
	inst, a, v := duel(t)
	a.player.MoveAndLook(0.5, 64, 0.5, 0, 0, true)
	v.player.MoveTo(0.5, 64, 6.5, true)
	hold(a, "minecraft:bow")
	if arrows > 0 {
		id, _ := world.ItemByName("minecraft:arrow")
		a.inv.set(mainInvStart, itemStack{ID: id, Count: byte(arrows)})
	}
	return inst, a, v
}

func fire(inst *Instance, a *ClientConnection, drawTicks uint64) {
	a.startDraw()
	inst.tickCount.Add(drawTicks)
	a.releaseUse()
	for i := 0; i < 40; i++ {
		inst.projectileTick(uint64(i))
	}
}

func TestFullDrawArrowHitsAndConsumesArrow(t *testing.T) {
	inst, a, v := archery(t, 2)
	fire(inst, a, 25)
	// Full draw: 3 blocks/tick, crit → ceil(≈3×2)×1.5 ≈ 9 before drag; 6
	// blocks of flight shave a little speed so 7.5–9 lands.
	if lost := player.MaxHealth - v.player.Health(); lost < 7 || lost > 9 {
		t.Errorf("full-draw crit arrow dealt %v, want 7..9", lost)
	}
	if st := a.inv.get(mainInvStart); st.Count != 1 {
		t.Errorf("one arrow should be spent, left %+v", st)
	}
	if lastPacket(t, v, CbPlayHurtAnimation) == nil {
		t.Error("victim should flash red")
	}
}

func TestShortDrawIsWeakerAndPowerIsStronger(t *testing.T) {
	inst, a, v := archery(t, 5)
	fire(inst, a, 8) // t=0.4 → f=0.32 → ~1 block/tick; may not even reach
	weak := player.MaxHealth - v.player.Health()
	if weak >= 7 {
		t.Errorf("short draw should hit softly or miss, dealt %v", weak)
	}

	v.player.SetHealth(player.MaxHealth)
	inst.tickCount.Add(100)
	bow, _ := world.ItemByName("minecraft:bow")
	a.inv.set(hotbarStart, itemStack{ID: bow, Count: 1, Enchantments: map[string]int{EnchantPower: 2}})
	fire(inst, a, 25)
	if lost := player.MaxHealth - v.player.Health(); lost < 11 {
		t.Errorf("Power II full draw should deal ≥ 11, dealt %v", lost)
	}
}

func TestNoArrowsNoShot(t *testing.T) {
	inst, a, v := archery(t, 0)
	fire(inst, a, 25)
	if v.player.Health() != player.MaxHealth {
		t.Error("shot without arrows")
	}
	inst.projMu.Lock()
	n := len(inst.projectiles)
	inst.projMu.Unlock()
	if n != 0 {
		t.Error("no projectile should exist")
	}
}

func TestArrowSticksInBlockThenVanishes(t *testing.T) {
	inst, a, _ := archery(t, 1)
	inst.World.SetBlock(world.Position{X: 0, Y: 65, Z: 3}, world.Stone) // wall before the target
	a.startDraw()
	inst.tickCount.Add(25)
	a.releaseUse()
	for i := 0; i < 10; i++ {
		inst.projectileTick(uint64(i))
	}
	inst.projMu.Lock()
	if len(inst.projectiles) != 1 || !inst.projectiles[0].stuck {
		inst.projMu.Unlock()
		t.Fatal("arrow should be stuck in the wall")
	}
	inst.projMu.Unlock()
	for i := 0; i <= arrowStuckTicks; i++ {
		inst.projectileTick(uint64(i))
	}
	inst.projMu.Lock()
	n := len(inst.projectiles)
	inst.projMu.Unlock()
	if n != 0 {
		t.Error("stuck arrow should vanish after its time")
	}
}

func TestArrowRespectsAttackVeto(t *testing.T) {
	inst, a, v := archery(t, 1)
	inst.OnPlayerAttack = func(_, _ *ClientConnection) bool { return false } // teammates
	fire(inst, a, 25)
	if v.player.Health() != player.MaxHealth {
		t.Error("vetoed arrow must not hurt")
	}
}

func TestArrowSpawnCarriesOwnerAndFacing(t *testing.T) {
	inst, a, v := archery(t, 1)
	a.player.MoveAndLook(0.5, 64, 0.5, -90, -30, true) // look +X, 30° up
	_ = lastPacket(t, v, CbPlaySpawnEntity)
	a.startDraw()
	inst.tickCount.Add(25)
	a.releaseUse()

	body := lastPacket(t, v, CbPlaySpawnEntity)
	if body == nil {
		t.Fatal("arrow spawn not broadcast")
	}
	buf := bytes.NewBuffer(body)
	_, _ = protocol.ReadVarInt(buf) // eid
	buf.Next(16)                    // uuid
	if typ, _ := protocol.ReadVarInt(buf); int32(typ) != arrowEntityID {
		t.Errorf("entity type %d, want arrow %d", typ, arrowEntityID)
	}
	buf.Next(24) // x y z
	pitch, _ := buf.ReadByte()
	yaw, _ := buf.ReadByte()
	buf.ReadByte() // head yaw
	data, _ := protocol.ReadVarInt(buf)
	if int32(data) != a.player.EntityID {
		t.Errorf("spawn data %d, want the shooter's id %d (owner; no +1 since 1.17)", data, a.player.EntityID)
	}
	// Vanilla arrow facing: yaw = atan2(vx, vz) → +X is +90°; pitch =
	// atan2(vy, h) → upward is positive.
	if yaw != protocol.AngleToByte(90) {
		t.Errorf("yaw byte %d, want %d (+X)", yaw, protocol.AngleToByte(90))
	}
	if p := int8(pitch); p < 18 || p > 24 { // 30° ≈ 21/256
		t.Errorf("pitch byte %d, want ≈21 (30° up)", p)
	}
}

func TestDrawPoseBroadcastToOthers(t *testing.T) {
	inst, a, v := archery(t, 2) // one shot, one more draw to cancel
	_ = lastPacket(t, v, CbPlaySetEntityMetadata)
	_ = lastPacket(t, a, CbPlaySetEntityMetadata)
	a.startDraw()
	if got := handStateOf(t, lastPacket(t, v, CbPlaySetEntityMetadata)); got != handStateActive {
		t.Errorf("watcher hand state %#x, want active", got)
	}
	if lastPacket(t, a, CbPlaySetEntityMetadata) != nil {
		t.Error("the shooter's own client tracks its draw; no metadata to self")
	}
	inst.tickCount.Add(25)
	a.releaseUse()
	if got := handStateOf(t, lastPacket(t, v, CbPlaySetEntityMetadata)); got != 0 {
		t.Errorf("after release hand state %#x, want idle", got)
	}
	// A slot switch cancels the draw and the pose with it.
	a.startDraw()
	_ = lastPacket(t, v, CbPlaySetEntityMetadata)
	a.cancelUse()
	if got := handStateOf(t, lastPacket(t, v, CbPlaySetEntityMetadata)); got != 0 {
		t.Errorf("after cancel hand state %#x, want idle", got)
	}
	a.cancelUse()
	if lastPacket(t, v, CbPlaySetEntityMetadata) != nil {
		t.Error("cancelling nothing must not spam metadata")
	}
}

// handStateOf parses a hand-state metadata body (index 8, Byte).
func handStateOf(t *testing.T, body []byte) byte {
	t.Helper()
	if body == nil {
		t.Fatal("no metadata packet")
	}
	buf := bytes.NewBuffer(body)
	_, _ = protocol.ReadVarInt(buf)
	if idx, _ := buf.ReadByte(); idx != handStateIndex {
		t.Fatalf("metadata index %d, want %d", idx, handStateIndex)
	}
	if typ, _ := protocol.ReadVarInt(buf); typ != 0 {
		t.Fatalf("metadata type %d, want Byte", typ)
	}
	v, _ := buf.ReadByte()
	return v
}

func TestFullDrawArrowDoesNotTunnelThroughThinWall(t *testing.T) {
	inst, a, _ := archery(t, 1)
	// One-block wall at z=2, nothing behind it. At 3 blocks/tick the old
	// end-point test jumped from z≈0.5 to z≈3.5 and never saw the wall.
	inst.World.SetBlock(world.Position{X: 0, Y: 65, Z: 2}, world.Stone)
	a.startDraw()
	inst.tickCount.Add(25)
	a.releaseUse()
	inst.projectileTick(1)

	inst.projMu.Lock()
	defer inst.projMu.Unlock()
	if len(inst.projectiles) != 1 {
		t.Fatalf("projectiles: %d, want the stuck arrow", len(inst.projectiles))
	}
	p := inst.projectiles[0]
	if !p.stuck {
		t.Fatalf("arrow should be stuck in the wall, at z=%.2f flying", p.z)
	}
	if p.z >= 2 || p.z < 1.8 {
		t.Errorf("arrow parked at z=%.2f, want just short of the wall face (1.8..2)", p.z)
	}
}

// stickArrow fires a full-draw arrow into a wall two blocks ahead and
// returns it, still stuck.
func stickArrow(t *testing.T, inst *Instance, a *ClientConnection) *projectile {
	t.Helper()
	inst.World.SetBlock(world.Position{X: 0, Y: 65, Z: 2}, world.Stone)
	a.startDraw()
	inst.tickCount.Add(25)
	a.releaseUse()
	inst.projectileTick(1)
	inst.projMu.Lock()
	defer inst.projMu.Unlock()
	if len(inst.projectiles) != 1 || !inst.projectiles[0].stuck {
		t.Fatal("arrow should be stuck in the wall")
	}
	return inst.projectiles[0]
}

func projectileCount(inst *Instance) int {
	inst.projMu.Lock()
	defer inst.projMu.Unlock()
	return len(inst.projectiles)
}

func TestStuckArrowIsPickedUpBySurvivalPlayer(t *testing.T) {
	inst, a, v := archery(t, 1)
	p := stickArrow(t, inst, a)
	if p.pickup != pickupAllowed {
		t.Fatalf("survival shot pickup mode %d, want allowed", p.pickup)
	}
	// The victim (far away) can't reach it; nobody is near → it stays.
	inst.projectileTick(2)
	if projectileCount(inst) != 1 {
		t.Fatal("arrow should stay with nobody near it")
	}
	// Walk the other player onto it.
	_ = lastPacket(t, v, CbPlayPickupItem)
	v.player.MoveTo(p.x, 64, p.z, true)
	inst.projectileTick(3)
	if projectileCount(inst) != 0 {
		t.Error("arrow should be collected")
	}
	if v.countItem(arrowItemID()) != 1 {
		t.Errorf("collector has %d arrows, want 1", v.countItem(arrowItemID()))
	}
	if lastPacket(t, v, CbPlayPickupItem) == nil {
		t.Error("pickup fly-in not broadcast")
	}
	if a.countItem(arrowItemID()) != 0 {
		t.Error("the shooter's quiver should still be empty")
	}
}

func TestInfinityBowKeepsArrowAndMakesItCreativeOnly(t *testing.T) {
	inst, a, v := archery(t, 1)
	bow, _ := world.ItemByName("minecraft:bow")
	a.inv.set(hotbarStart, itemStack{ID: bow, Count: 1, Enchantments: map[string]int{EnchantInfinity: 1}})
	p := stickArrow(t, inst, a)
	if a.countItem(arrowItemID()) != 1 {
		t.Errorf("Infinity must not spend the arrow, left %d", a.countItem(arrowItemID()))
	}
	if p.pickup != pickupCreativeOnly {
		t.Fatalf("Infinity shot pickup mode %d, want creative-only", p.pickup)
	}
	// A survival player walking into it gets nothing and it stays.
	v.player.MoveTo(p.x, 64, p.z, true)
	inst.projectileTick(2)
	if projectileCount(inst) != 1 || v.countItem(arrowItemID()) != 0 {
		t.Error("survival player must not collect an Infinity arrow")
	}
	// A creative player clears it without gaining an arrow.
	v.player.SetGamemode(player.Creative)
	inst.projectileTick(3)
	if projectileCount(inst) != 0 {
		t.Error("creative player should clear the arrow")
	}
	if v.countItem(arrowItemID()) != 0 {
		t.Error("creative pickup of an Infinity arrow gives nothing")
	}
	// No arrows at all: Infinity still needs one in the quiver.
	a.inv.set(mainInvStart, itemStack{})
	a.startDraw()
	inst.tickCount.Add(25)
	a.releaseUse()
	if projectileCount(inst) != 0 {
		t.Error("Infinity with an empty quiver must not shoot")
	}
}
