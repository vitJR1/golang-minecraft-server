package nbt

import (
	"bytes"
	"encoding/binary"
	"runtime"
	"strings"
	"testing"
)

// limits_test.go: hostile NBT (client item slots) must fail fast without
// allocating by the declared lengths — before the fix a ~10-byte tag made
// the decoder ask for gigabytes and the Go runtime died with a fatal OOM.

// TestHugeLengthsRejected declares 2³¹−1 elements for every length-prefixed
// tag with no data behind them. Each must error, and the heap must not grow
// by anything near the declared size.
func TestHugeLengthsRejected(t *testing.T) {
	maxLen := []byte{0x7F, 0xFF, 0xFF, 0xFF}
	cases := map[string][]byte{
		"byte array":    append([]byte{byte(TagByteArray), 0, 0}, maxLen...),
		"int array":     append([]byte{byte(TagIntArray), 0, 0}, maxLen...),
		"long array":    append([]byte{byte(TagLongArray), 0, 0}, maxLen...),
		"list of int":   append([]byte{byte(TagList), 0, 0, byte(TagInt)}, maxLen...),
		"list of end":   append([]byte{byte(TagList), 0, 0, byte(TagEnd)}, maxLen...),
		"list of bogus": append([]byte{byte(TagList), 0, 0, 0x7F}, maxLen...),
		"list in compound": append([]byte{byte(TagCompound), 0, 0, byte(TagList), 0, 1, 'x', byte(TagCompound)},
			maxLen...),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			if err := SkipTag(bytes.NewReader(data)); err == nil {
				t.Fatal("SkipTag accepted a length larger than the input")
			}
			runtime.ReadMemStats(&after)
			if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
				t.Errorf("decoder allocated %d bytes for a %d-byte input", grew, len(data))
			}
		})
	}
}

// TestDeepNestingRejected: 10 000 nested lists fit in 50 KB; recursion must
// stop at MaxDepth instead of descending all the way.
func TestDeepNestingRejected(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{byte(TagList), 0, 0})
	for i := 0; i < 10000; i++ {
		buf.Write([]byte{byte(TagList), 0, 0, 0, 1}) // list of 1 list
	}
	buf.Write([]byte{byte(TagEnd), 0, 0, 0, 0})
	err := SkipTag(bytes.NewReader(buf.Bytes()))
	if err == nil || !strings.Contains(err.Error(), "deeper") {
		t.Fatalf("want depth error, got %v", err)
	}
}

// TestDepthAtLimitAccepted: nesting right at MaxDepth still decodes.
func TestDepthAtLimitAccepted(t *testing.T) {
	var v Value = Compound{}
	for i := 1; i < MaxDepth; i++ {
		v = Compound{"c": v}
	}
	data := Marshal(v.(Compound))
	if _, err := UnmarshalNetwork(data); err != nil {
		t.Fatalf("depth %d rejected: %v", MaxDepth, err)
	}
}

// TestNetworkQuota: a list of one million empty compounds is 1 MB on the
// wire but tens of MB in memory; the network quota refuses it, while the
// trusted file decoder still reads it.
func TestNetworkQuota(t *testing.T) {
	const n = 1_000_000
	payload := []byte{byte(TagCompound), 0, 0, byte(TagList), 0, 1, 'l', byte(TagCompound)}
	payload = binary.BigEndian.AppendUint32(payload, n)
	payload = append(payload, bytes.Repeat([]byte{byte(TagEnd)}, n)...)
	payload = append(payload, byte(TagEnd))

	_, err := UnmarshalNetwork(payload)
	if err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("want quota error, got %v", err)
	}
	if err := SkipTag(bytes.NewReader(payload)); err == nil {
		t.Fatal("SkipTag ignored the network quota")
	}
	got, err := Unmarshal(payload)
	if err != nil {
		t.Fatalf("file decoder: %v", err)
	}
	if l := got["l"].(List); len(l.Items) != n {
		t.Errorf("file decoder read %d items, want %d", len(l.Items), n)
	}
}

// TestNetworkItemNBTStillDecodes: a realistic enchanted-sword tag passes.
func TestNetworkItemNBTStillDecodes(t *testing.T) {
	tag := Compound{
		"Damage": Int(12),
		"Enchantments": List{ElemTag: TagCompound, Items: []Value{
			Compound{"id": String("minecraft:sharpness"), "lvl": Short(2)},
		}},
		"display": Compound{"Name": String(`{"text":"Blade"}`)},
	}
	data := Marshal(tag)
	r := bytes.NewReader(data)
	if err := SkipTag(r); err != nil || r.Len() != 0 {
		t.Fatalf("SkipTag: err=%v, %d bytes left", err, r.Len())
	}
	got, err := UnmarshalNetwork(data)
	if err != nil {
		t.Fatal(err)
	}
	if got["Damage"] != Int(12) {
		t.Errorf("Damage = %v", got["Damage"])
	}
}
