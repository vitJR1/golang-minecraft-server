package server

import "testing"

func TestParseOnOff(t *testing.T) {
	truthy := []string{"on", "ON", "true", "yes", "1", "enable", "enabled"}
	for _, s := range truthy {
		if v, ok := parseOnOff(s); !ok || !v {
			t.Errorf("parseOnOff(%q) = (%v,%v), want (true,true)", s, v, ok)
		}
	}
	falsy := []string{"off", "Off", "false", "no", "0", "disable", "disabled"}
	for _, s := range falsy {
		if v, ok := parseOnOff(s); !ok || v {
			t.Errorf("parseOnOff(%q) = (%v,%v), want (false,true)", s, v, ok)
		}
	}
	for _, s := range []string{"", "maybe", "2", "onn"} {
		if _, ok := parseOnOff(s); ok {
			t.Errorf("parseOnOff(%q): expected ok=false", s)
		}
	}
}

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
