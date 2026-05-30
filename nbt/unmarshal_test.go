package nbt

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"reflect"
	"testing"
)

// TestRoundTripAllTags marshals a Compound with one of every supported tag
// type, unmarshals it back, and checks every field survives.
func TestRoundTripAllTags(t *testing.T) {
	original := Compound{
		"byte":   Byte(-7),
		"short":  Short(1234),
		"int":    Int(0x01020304),
		"long":   Long(0x0102030405060708),
		"float":  Float(3.14159),
		"double": Double(2.71828),
		"string": String("hello мир 🌍"),
		"bytes":  ByteArray{1, 2, 3, 4, 5},
		"ints":   IntArray{10, 20, 30},
		"longs":  LongArray{100, 200, 300},
		"nested": Compound{
			"x": Int(42),
			"y": String("inside"),
		},
		"list": List{
			ElemTag: TagInt,
			Items:   []Value{Int(1), Int(2), Int(3)},
		},
	}

	data := Marshal(original)
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(map[string]Value(got), map[string]Value(original)) {
		t.Errorf("round trip mismatch:\noriginal: %#v\ngot:      %#v", original, got)
	}
}

func TestUnmarshalEmptyCompound(t *testing.T) {
	original := Compound{}
	data := Marshal(original)
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty compound: got %d entries, want 0", len(got))
	}
}

func TestUnmarshalNestedLists(t *testing.T) {
	original := Compound{
		"compounds": List{
			ElemTag: TagCompound,
			Items: []Value{
				Compound{"a": Int(1)},
				Compound{"a": Int(2)},
			},
		},
	}
	data := Marshal(original)
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	lst, ok := got["compounds"].(List)
	if !ok {
		t.Fatalf("compounds not a List: %T", got["compounds"])
	}
	if lst.ElemTag != TagCompound {
		t.Errorf("elem tag: got %s, want Compound", lst.ElemTag)
	}
	if len(lst.Items) != 2 {
		t.Fatalf("list length: got %d, want 2", len(lst.Items))
	}
	for i, item := range lst.Items {
		c, ok := item.(Compound)
		if !ok {
			t.Errorf("items[%d] not a Compound: %T", i, item)
			continue
		}
		if got, want := c["a"], Int(int32(i+1)); got != want {
			t.Errorf("items[%d].a: got %v, want %v", i, got, want)
		}
	}
}

func TestUnmarshalGzip(t *testing.T) {
	original := Compound{"msg": String("compressed")}
	raw := Marshal(original)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := Unmarshal(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got["msg"] != String("compressed") {
		t.Errorf("after gzip round-trip: got %v", got["msg"])
	}
}

func TestUnmarshalZlib(t *testing.T) {
	original := Compound{"msg": String("zlibbed")}
	raw := Marshal(original)

	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := Unmarshal(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got["msg"] != String("zlibbed") {
		t.Errorf("after zlib round-trip: got %v", got["msg"])
	}
}

func TestUnmarshalRejectsNonCompoundRoot(t *testing.T) {
	// Root is a TagInt (3), not a TagCompound (10) — Marshal would never
	// produce this, but a malformed file might.
	bad := []byte{0x03, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04}
	if _, err := Unmarshal(bad); err == nil {
		t.Error("expected error on non-Compound root")
	}
}

func TestUnmarshalRejectsTruncated(t *testing.T) {
	original := Compound{"big": String("not so big really")}
	data := Marshal(original)
	if _, err := Unmarshal(data[:len(data)-3]); err == nil {
		t.Error("expected error on truncated input")
	}
}

func TestUnmarshalEmptyList(t *testing.T) {
	// Marshal of empty List declares ElemTag = End on the wire.
	original := Compound{
		"empty": List{ElemTag: TagInt, Items: nil},
	}
	data := Marshal(original)
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	lst, ok := got["empty"].(List)
	if !ok {
		t.Fatalf("not a List: %T", got["empty"])
	}
	if len(lst.Items) != 0 {
		t.Errorf("expected empty list, got %d items", len(lst.Items))
	}
	if lst.ElemTag != TagEnd {
		t.Errorf("empty list elem tag: got %s, want End", lst.ElemTag)
	}
}
