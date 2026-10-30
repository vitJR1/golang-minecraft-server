package abilities

import (
	"testing"

	"minecraft-server/player"
)

func TestFlags(t *testing.T) {
	for _, tc := range []struct {
		mode           player.Gamemode
		mayFly, flying bool
		want           byte
	}{
		{player.Survival, false, false, 0},
		{player.Survival, true, false, MayFly},
		{player.Adventure, true, true, MayFly | Flying},
		{player.Creative, false, false, Invulnerable | MayFly | Instabuild},
		{player.Spectator, false, false, Invulnerable | MayFly | Flying},
	} {
		if got := Flags(tc.mode, tc.mayFly, tc.flying); got != tc.want {
			t.Errorf("Flags(%v, %v, %v) = %#x, want %#x", tc.mode, tc.mayFly, tc.flying, got, tc.want)
		}
	}
	if p := Payload(MayFly); len(p) != 9 || p[0] != MayFly {
		t.Errorf("payload % x", p)
	}
}
