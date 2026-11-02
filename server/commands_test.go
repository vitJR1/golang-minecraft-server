package server

import "testing"

func TestInstancePvPToggle(t *testing.T) {
	i := NewInstance("t", nil, nil)
	defer i.Stop()
	if !i.PvPEnabled() {
		t.Error("new instance should default to PvP enabled")
	}
	i.SetPvP(false)
	if i.PvPEnabled() {
		t.Error("SetPvP(false) did not disable PvP")
	}
	i.SetPvP(true)
	if !i.PvPEnabled() {
		t.Error("SetPvP(true) did not enable PvP")
	}

	if i.InstantRespawn() {
		t.Error("instant respawn should default off")
	}
	i.SetInstantRespawn(true)
	if !i.InstantRespawn() {
		t.Error("SetInstantRespawn(true) did not take effect")
	}
}
