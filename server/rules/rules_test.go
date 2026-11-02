package rules

import "testing"

func TestZeroSwitches(t *testing.T) {
	var s Switches
	if s.PvPEnabled() || s.InstantRespawn() || s.CustomRespawn() || s.WeaponDamage() || s.TNTAutoPrime() {
		t.Error("zero Switches should have every rule off")
	}
	if s.ExplosionDamage() != 1 {
		t.Errorf("explosion damage %v, want 1 until set", s.ExplosionDamage())
	}
}

func TestToggles(t *testing.T) {
	var s Switches
	s.SetPvP(true)
	s.SetWeaponDamage(true)
	if !s.PvPEnabled() || !s.WeaponDamage() || s.TNTAutoPrime() {
		t.Error("toggles should be independent")
	}
	s.SetPvP(false)
	if s.PvPEnabled() {
		t.Error("SetPvP(false) didn't stick")
	}
}

func TestExplosionDamage(t *testing.T) {
	var s Switches
	s.SetExplosionDamage(0)
	if s.ExplosionDamage() != 0 {
		t.Errorf("explicit 0 read back as %v", s.ExplosionDamage())
	}
	s.SetExplosionDamage(-3)
	if s.ExplosionDamage() != 0 {
		t.Errorf("negative scale read back as %v, want 0", s.ExplosionDamage())
	}
	s.SetExplosionDamage(0.5)
	if s.ExplosionDamage() != 0.5 {
		t.Errorf("0.5 read back as %v", s.ExplosionDamage())
	}
}
