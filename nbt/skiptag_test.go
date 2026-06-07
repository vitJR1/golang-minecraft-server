package nbt

import (
	"bytes"
	"testing"
)

func TestSkipTagNoNBT(t *testing.T) {
	// Slot "no NBT" is a single 0x00; a trailing marker must survive.
	r := bytes.NewReader([]byte{0x00, 0xAA})
	if err := SkipTag(r); err != nil {
		t.Fatal(err)
	}
	if b, _ := r.ReadByte(); b != 0xAA {
		t.Errorf("over/under-read past TagEnd: got 0x%02x", b)
	}
}

func TestSkipTagCompound(t *testing.T) {
	data := Marshal(Compound{"a": Byte(7), "name": String("hi")})
	data = append(data, 0xBB) // trailing marker
	r := bytes.NewReader(data)
	if err := SkipTag(r); err != nil {
		t.Fatal(err)
	}
	if b, _ := r.ReadByte(); b != 0xBB {
		t.Errorf("compound skip misaligned: got 0x%02x", b)
	}
}
