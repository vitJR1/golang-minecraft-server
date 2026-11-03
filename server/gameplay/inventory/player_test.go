package inventory

import "testing"

func TestGiveCountTake(t *testing.T) {
	var p Player
	// 70 of a 64-stack item: a full stack in hotbar 0, 6 in hotbar 1.
	left, changed := p.Give(Stack{ID: 1, Count: 64})
	if left != 0 || len(changed) != 1 || changed[0] != HotbarStart {
		t.Fatalf("first give: left %d changed %v", left, changed)
	}
	left, changed = p.Give(Stack{ID: 1, Count: 6})
	if left != 0 || len(changed) != 1 || changed[0] != HotbarStart+1 {
		t.Fatalf("second give: left %d changed %v", left, changed)
	}
	p.Set(MainStart, Stack{ID: 1, Count: 10})
	if n := p.Count(1); n != 80 {
		t.Fatalf("count %d", n)
	}
	// Take drains the main inventory before the hotbar.
	ok, changed := p.Take(1, 12)
	if !ok || p.Get(MainStart).Count != 0 || p.Count(1) != 68 {
		t.Fatalf("take: ok %v main %+v count %d changed %v", ok, p.Get(MainStart), p.Count(1), changed)
	}
	if ok, changed := p.Take(1, 1000); ok || changed != nil || p.Count(1) != 68 {
		t.Fatal("taking more than there is must take nothing")
	}
	if !p.TakeOne(HotbarStart+1) || p.Get(HotbarStart+1).Count != 3 {
		t.Fatalf("take one: %+v", p.Get(HotbarStart+1))
	}
	if p.TakeOne(MainStart) {
		t.Fatal("take one from an empty slot")
	}
	if got := len(p.Mirror()); got != 36 {
		t.Fatalf("mirror has %d slots", got)
	}
}

func TestGiveFillsInventory(t *testing.T) {
	var p Player
	for _, slot := range GiveOrder {
		p.Set(slot, Stack{ID: 2, Count: 64})
	}
	if left, changed := p.Give(Stack{ID: 1, Count: 5}); left != 5 || changed != nil {
		t.Fatalf("full inventory: left %d changed %v", left, changed)
	}
}
