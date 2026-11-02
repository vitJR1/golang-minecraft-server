package commands

import (
	"strings"
	"testing"

	"minecraft-server/server"
	"minecraft-server/world"
)

func TestBwGiveAndKillOnlyInWeaponDamageInstances(t *testing.T) {
	s := server.New()
	s.Ops.Add("QA")
	s.Hub.SetPvP(true)
	_, c := login(t, s, "QA")
	iron, _ := world.ItemByName("minecraft:iron_ingot")

	// Outside an arena: refused.
	s.RunCommand(c, "bwgive iron 10")
	if c.CountItem(iron) != 0 {
		t.Error("bwgive must be refused where weapon damage is off")
	}
	s.RunCommand(c, "bwkill")
	if c.Player().IsDead() {
		t.Error("bwkill must be refused where weapon damage is off")
	}

	s.Hub.SetWeaponDamage(true)
	s.RunCommand(c, "bwgive IRON 10")
	if got := c.CountItem(iron); got != 10 {
		t.Errorf("iron after bwgive: %d, want 10", got)
	}
	s.RunCommand(c, "bwgive emerald")
	emerald, _ := world.ItemByName("minecraft:emerald")
	if got := c.CountItem(emerald); got != 16 {
		t.Errorf("default amount: %d, want 16", got)
	}
	s.RunCommand(c, "bwgive netherite 1")
	s.RunCommand(c, "bwgive gold 0")
	gold, _ := world.ItemByName("minecraft:gold_ingot")
	if c.CountItem(gold) != 0 {
		t.Error("bad amount must give nothing")
	}

	var deaths int
	s.Hub.OnPlayerDeath = func(_, _ *server.ClientConnection) { deaths++ }
	s.RunCommand(c, "bwkill")
	if !c.Player().IsDead() || deaths != 1 {
		t.Errorf("bwkill: dead=%v deaths=%d", c.Player().IsDead(), deaths)
	}
}

func TestFlyCommand(t *testing.T) {
	s := server.New()
	s.Ops.Add("Pilot")
	_, pilot := login(t, s, "Pilot")
	_, plain := login(t, s, "Wannabe")

	s.RunCommand(plain, "fly")
	if plain.CanFly() {
		t.Fatal("a non-op got flight")
	}
	s.RunCommand(pilot, "fly")
	if !pilot.CanFly() {
		t.Fatal("/fly didn't grant flight")
	}
	s.RunCommand(pilot, "fly Wannabe")
	if !plain.CanFly() {
		t.Fatal("/fly <player> didn't grant the target flight")
	}
	s.RunCommand(pilot, "fly")
	if pilot.CanFly() {
		t.Fatal("a second /fly should take flight away")
	}
}

func TestBotSuggestions(t *testing.T) {
	s := server.New()
	s.Ops.Add("Op")
	_, op := login(t, s, "Op")
	cases := map[string][]string{
		"/bot ":         {"add", "clear", "list", "match", "remove"},
		"/bot a":        {"add"},
		"/bot add ":     nil, // the hub runs no game with teams
		"/bot add red ": {"easy", "hard", "normal"},
		"/bot remove ":  nil, // no bots
		"/bot list ":    nil,
	}
	for text, want := range cases {
		_, _, got := s.Suggestions(op, text)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%q → %v, want %v", text, got, want)
		}
	}
}
