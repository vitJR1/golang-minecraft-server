package effect

import (
	"testing"
	"time"
)

func TestTable(t *testing.T) {
	var tb Table[string]
	if tb.Level("a", Speed) != 0 {
		t.Fatal("empty table")
	}
	tb.Apply("a", Speed, 2, time.Minute)
	tb.Apply("a", Haste, 1, -time.Second) // already over
	if tb.Level("a", Speed) != 2 || tb.Level("b", Speed) != 0 {
		t.Fatal("levels are per player")
	}
	if act := tb.Active("a"); len(act) != 1 || act[0].Effect != Speed || act[0].Level != 2 {
		t.Fatalf("active %+v", act)
	}
	tb.Apply("a", Blindness, 1, -time.Second)
	// Both expired effects (Haste, Blindness) go; Speed stays.
	if exp := tb.Sweep(); len(exp["a"]) != 2 || tb.Level("a", Speed) != 2 {
		t.Fatalf("sweep %v", exp)
	}
	if got := tb.Remove("a", &Speed); len(got) != 1 || tb.Level("a", Speed) != 0 {
		t.Fatalf("remove %v", got)
	}
	tb.Apply("a", Speed, 1, time.Minute)
	tb.Apply("a", Haste, 1, time.Minute)
	if got := tb.Remove("a", nil); len(got) != 2 {
		t.Fatalf("remove all %v", got)
	}
}

func TestRules(t *testing.T) {
	if RegenInterval(1) != 50 || RegenInterval(2) != 25 || RegenInterval(10) != 1 {
		t.Error("RegenInterval")
	}
	if AbsorptionPool(1, 0) != 4 || AbsorptionPool(1, 6) != 6 || AbsorptionPool(2, 3) != 8 {
		t.Error("AbsorptionPool")
	}
	if e, lvl, d, ok := FromPotion("minecraft:strong_swiftness"); !ok || e != Speed || lvl != 2 || d != 90*time.Second {
		t.Error("FromPotion")
	}
	if _, _, _, ok := FromPotion("minecraft:water"); ok {
		t.Error("water is no effect")
	}
	if n := Names(); len(n) != len(Known) || n[0] != "absorption" {
		t.Errorf("names %v", n)
	}
}
