package chat

import (
	"bytes"
	"testing"
	"time"

	"minecraft-server/protocol"
)

func TestLineAndPayload(t *testing.T) {
	if Line("Bob", "hi") != "<Bob> hi" || Line("", "server") != "server" {
		t.Error("Line")
	}
	buf := bytes.NewBuffer(SystemPayload(`say "hi"`))
	s, err := protocol.ReadStringFromBuf(buf)
	if err != nil || s != `{"text":"say \"hi\""}` {
		t.Fatalf("component %q %v", s, err)
	}
	if overlay, _ := buf.ReadByte(); overlay != 0 || buf.Len() != 0 {
		t.Fatal("overlay byte")
	}
}

func TestVerdict(t *testing.T) {
	for _, tc := range []struct {
		v    Verdict
		text string
		ok   bool
	}{
		{Verdict{Allow: true}, "hello", true},
		{Verdict{Allow: true, Rewrite: "****"}, "****", true},
		{Verdict{Allow: false, MuteFor: time.Minute}, "", false},
	} {
		if text, ok := tc.v.Apply("hello"); text != tc.text || ok != tc.ok {
			t.Errorf("%+v → %q %v", tc.v, text, ok)
		}
	}
}
