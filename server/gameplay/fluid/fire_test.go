package fluid

import (
	"testing"

	"minecraft-server/world"
)

func TestBurnStep(t *testing.T) {
	air, lava, water := world.Air, world.Lava, world.Water
	if b := BurnStep(lava, air, 0, 10); !b.Ignite || b.Fire != fireFromLavaTicks || b.Damage != lavaDamage {
		t.Errorf("in lava on a check tick: %+v", b)
	}
	if b := BurnStep(air, lava, 0, 11); b.Damage != 0 || !b.Ignite {
		t.Errorf("head in lava between checks: %+v", b)
	}
	if b := BurnStep(water, air, 200, 0); b.Fire != 0 || b.Damage != 0 {
		t.Errorf("water puts it out: %+v", b)
	}
	if b := BurnStep(air, air, 40, 0); b.Fire != 39 || b.Damage != fireDamage || b.Ignite {
		t.Errorf("burning, damage tick: %+v", b)
	}
	if b := BurnStep(air, air, 41, 0); b.Fire != 40 || b.Damage != 0 {
		t.Errorf("burning, no damage: %+v", b)
	}
	if b := BurnStep(air, air, 1, 0); b.Fire != 0 {
		t.Errorf("the last fire tick puts it out: %+v", b)
	}
	if b := BurnStep(air, air, 0, 0); b != (Burn{}) {
		t.Errorf("not burning: %+v", b)
	}
}
