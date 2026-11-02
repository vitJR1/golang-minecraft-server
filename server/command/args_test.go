package command

import (
	"bytes"
	"testing"

	"minecraft-server/player"
	"minecraft-server/protocol"
)

func TestParseOnOff(t *testing.T) {
	truthy := []string{"on", "ON", "true", "yes", "1", "enable", "enabled"}
	for _, s := range truthy {
		if v, ok := ParseOnOff(s); !ok || !v {
			t.Errorf("ParseOnOff(%q) = (%v,%v), want (true,true)", s, v, ok)
		}
	}
	falsy := []string{"off", "Off", "false", "no", "0", "disable", "disabled"}
	for _, s := range falsy {
		if v, ok := ParseOnOff(s); !ok || v {
			t.Errorf("ParseOnOff(%q) = (%v,%v), want (false,true)", s, v, ok)
		}
	}
	for _, s := range []string{"", "maybe", "2", "onn"} {
		if _, ok := ParseOnOff(s); ok {
			t.Errorf("ParseOnOff(%q): expected ok=false", s)
		}
	}
	for _, v := range []bool{true, false} {
		if got, ok := ParseOnOff(OnOff(v)); !ok || got != v {
			t.Errorf("OnOff(%v) doesn't read back", v)
		}
	}
}

func TestParseGamemode(t *testing.T) {
	cases := map[string]player.Gamemode{
		"survival": player.Survival, "C": player.Creative, "2": player.Adventure, "sp": player.Spectator,
	}
	for s, want := range cases {
		if got, ok := ParseGamemode(s); !ok || got != want {
			t.Errorf("ParseGamemode(%q) = %v %v, want %v", s, got, ok, want)
		}
	}
	if _, ok := ParseGamemode("hardcore"); ok {
		t.Error("unknown gamemode accepted")
	}
}

func TestParseCoords(t *testing.T) {
	x, y, z, err := ParseCoords([]string{"1.5", "-2", "3"})
	if err != nil || x != 1.5 || y != -2 || z != 3 {
		t.Errorf("ParseCoords = %v %v %v %v", x, y, z, err)
	}
	if _, _, _, err := ParseCoords([]string{"1", "abc", "3"}); err == nil || err.Error() != "Bad Y: abc" {
		t.Errorf("bad axis error %v", err)
	}
}

func TestSuggestionsPayload(t *testing.T) {
	buf := bytes.NewBuffer(SuggestionsPayload(5, 4, 2, []string{"_LD", "_Max"}))
	var got []int
	for i := 0; i < 4; i++ {
		v, _ := protocol.ReadVarInt(buf)
		got = append(got, v)
	}
	m1, _ := protocol.ReadStringFromBuf(buf)
	tip, _ := buf.ReadByte()
	m2, _ := protocol.ReadStringFromBuf(buf)
	tip2, _ := buf.ReadByte()
	if got[0] != 5 || got[1] != 4 || got[2] != 2 || got[3] != 2 || m1 != "_LD" || m2 != "_Max" || tip != 0 || tip2 != 0 || buf.Len() != 0 {
		t.Errorf("payload %v %q %q", got, m1, m2)
	}
}

func TestArgCompleters(t *testing.T) {
	names := ArgFrom(2, func(s string) []string { return []string{s} })
	if got := names("me", []string{"a", "b"}); len(got) != 1 || got[0] != "me" {
		t.Errorf("ArgFrom at its position = %v", got)
	}
	if names("me", []string{"a"}) != nil {
		t.Error("ArgFrom elsewhere should give nothing")
	}
	fixed := FixedArg[string](1, "x", "y")
	if got := fixed("", []string{"p"}); len(got) != 2 {
		t.Errorf("FixedArg = %v", got)
	}
}
