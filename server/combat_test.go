package server

import (
	"bytes"
	"math"
	"minecraft-server/cfg"
	"minecraft-server/protocol"
	"testing"
)

func TestCooldownTicks(t *testing.T) {
	cases := []struct {
		speed float32
		want  float64
	}{
		{4.0, 5},    // fist
		{1.6, 12.5}, // sword
		{1.0, 20},   // axe
		{0, 0},      // misconfigured → instant
		{-1, 0},
	}
	for _, tc := range cases {
		// float32 attack speeds (1.6) aren't exactly representable, so
		// compare with a small tolerance rather than for bit-equality.
		if got := cooldownTicks(tc.speed); math.Abs(got-tc.want) > 1e-4 {
			t.Errorf("cooldownTicks(%v) = %v, want %v", tc.speed, got, tc.want)
		}
	}
}

func TestAttackStrengthScale(t *testing.T) {
	// Zero cooldown is always fully charged.
	if got := attackStrengthScale(0, 0); got != 1 {
		t.Errorf("zero cooldown: got %v, want 1", got)
	}
	// Long wait saturates to 1.
	if got := attackStrengthScale(100, 12.5); got != 1 {
		t.Errorf("saturated: got %v, want 1", got)
	}
	// Fresh swing (0 ticks since) on a 12.5-tick cooldown ≈ 0.04.
	if got := attackStrengthScale(0, 12.5); got <= 0 || got > 0.1 {
		t.Errorf("fresh swing: got %v, want small positive", got)
	}
	// Monotonic increase with elapsed ticks.
	prev := float32(-1)
	for ticks := uint64(0); ticks <= 13; ticks++ {
		s := attackStrengthScale(ticks, 12.5)
		if s < prev {
			t.Errorf("not monotonic at %d ticks: %v < %v", ticks, s, prev)
		}
		prev = s
	}
}

func TestScaledDamage(t *testing.T) {
	const base float32 = 6
	if got := scaledDamage(base, 1); got != base {
		t.Errorf("full charge: got %v, want %v", got, base)
	}
	if got := scaledDamage(base, 0); math.Abs(float64(got-base*0.2)) > 1e-5 {
		t.Errorf("no charge: got %v, want %v", got, base*0.2)
	}
	// Half charge: base*(0.2 + 0.25*0.8) = base*0.4.
	if got := scaledDamage(base, 0.5); math.Abs(float64(got-base*0.4)) > 1e-5 {
		t.Errorf("half charge: got %v, want %v", got, base*0.4)
	}
}

func TestKnockbackHoriz(t *testing.T) {
	// Victim due east of attacker → push straight along +X.
	vx, vz := knockbackHoriz(0, 0, 5, 0, 0.4)
	if math.Abs(vx-0.4) > 1e-6 || math.Abs(vz) > 1e-6 {
		t.Errorf("east push: got (%v,%v), want (0.4,0)", vx, vz)
	}
	// Diagonal stays at the requested magnitude.
	vx, vz = knockbackHoriz(0, 0, 3, 4, 1)
	if mag := math.Hypot(vx, vz); math.Abs(mag-1) > 1e-6 {
		t.Errorf("diagonal magnitude: got %v, want 1", mag)
	}
	// Same column → degenerate push along +Z.
	vx, vz = knockbackHoriz(2, 2, 2, 2, 0.4)
	if math.Abs(vx) > 1e-6 || math.Abs(vz-0.4) > 1e-6 {
		t.Errorf("degenerate: got (%v,%v), want (0,0.4)", vx, vz)
	}
}

func TestVelocityShortClamp(t *testing.T) {
	if got := velocityShort(0.4); got != 3200 {
		t.Errorf("0.4 blocks/tick: got %d, want 3200", got)
	}
	if got := velocityShort(1000); got != math.MaxInt16 {
		t.Errorf("overflow: got %d, want %d", got, int16(math.MaxInt16))
	}
	if got := velocityShort(-1000); got != math.MinInt16 {
		t.Errorf("underflow: got %d, want %d", got, int16(math.MinInt16))
	}
}

func TestPvPVersionFromCfg(t *testing.T) {
	orig := cfg.PvPVersion
	t.Cleanup(func() { cfg.PvPVersion = orig })

	cfg.PvPVersion = 1
	if got := pvpVersionFromCfg(); got != PvP18 {
		t.Errorf("PVP_VERSION=1: got %v, want PvP18", got)
	}
	cfg.PvPVersion = 2
	if got := pvpVersionFromCfg(); got != PvP19 {
		t.Errorf("PVP_VERSION=2: got %v, want PvP19", got)
	}
	// Anything unrecognized falls back to 1.9.
	cfg.PvPVersion = 99
	if got := pvpVersionFromCfg(); got != PvP19 {
		t.Errorf("PVP_VERSION=99: got %v, want PvP19 (fallback)", got)
	}

	cfg.PvPVersion = 1
	if got := DefaultCombatConfig().Version; got != PvP18 {
		t.Errorf("DefaultCombatConfig().Version: got %v, want PvP18", got)
	}
}

func TestAttackSpeedAttributePayload(t *testing.T) {
	payload := attackSpeedAttributePayload(7, 1.6)
	buf := bytes.NewBuffer(payload)

	eid, err := protocol.ReadVarInt(buf)
	if err != nil || eid != 7 {
		t.Fatalf("entity id: got %d err %v, want 7", eid, err)
	}
	count, err := protocol.ReadVarInt(buf)
	if err != nil || count != 1 {
		t.Fatalf("property count: got %d err %v, want 1", count, err)
	}
	key, err := protocol.ReadStringFromBuf(buf)
	if err != nil || key != "minecraft:generic.attack_speed" {
		t.Fatalf("key: got %q err %v", key, err)
	}
	val, err := protocol.ReadDouble(buf)
	if err != nil || math.Abs(val-1.6) > 1e-9 {
		t.Fatalf("value: got %v err %v, want 1.6", val, err)
	}
	mods, err := protocol.ReadVarInt(buf)
	if err != nil || mods != 0 {
		t.Fatalf("modifier count: got %d err %v, want 0", mods, err)
	}
	if buf.Len() != 0 {
		t.Errorf("trailing bytes: %d left", buf.Len())
	}
}
