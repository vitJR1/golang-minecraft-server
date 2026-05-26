package nbt

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// Reference layout from https://wiki.vg/NBT for small, hand-encoded cases.

func TestMarshalEmptyCompound(t *testing.T) {
	got := Marshal(Compound{})
	// TAG_Compound(0x0a), name "" (uint16 0x0000), TAG_End(0x00)
	want := []byte{0x0a, 0x00, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestMarshalScalars(t *testing.T) {
	got := Marshal(Compound{
		"b": Byte(-1),
		"i": Int(0x01020304),
		"l": Long(0x0102030405060708),
		"s": String("ab"),
	})
	// Compound header
	expected := "" +
		"0a" + "0000" + // TAG_Compound, name ""
		// keys are sorted alphabetically: b, i, l, s
		"01" + "0001" + "62" + "ff" +
		"03" + "0001" + "69" + "01020304" +
		"04" + "0001" + "6c" + "0102030405060708" +
		"08" + "0001" + "73" + "0002" + "6162" +
		"00" // TAG_End
	want, _ := hex.DecodeString(expected)
	if !bytes.Equal(got, want) {
		t.Fatalf("\ngot  %x\nwant %x", got, want)
	}
}

func TestMarshalNestedCompoundAndList(t *testing.T) {
	got := Marshal(Compound{
		"inner": Compound{
			"x": Int(1),
		},
		"nums": List{
			ElemTag: TagInt,
			Items:   []Value{Int(10), Int(20)},
		},
	})
	expected := "" +
		"0a" + "0000" + // root compound, ""
		// "inner" (sorted before "nums")
		"0a" + "0005" + "696e6e6572" + // TAG_Compound name "inner"
		"03" + "0001" + "78" + "00000001" + // Int x=1
		"00" + // end of inner
		// "nums"
		"09" + "0004" + "6e756d73" + // TAG_List name "nums"
		"03" + "00000002" + "0000000a" + "00000014" + // elemTag=Int, count=2, [10, 20]
		"00" // end of root
	want, _ := hex.DecodeString(expected)
	if !bytes.Equal(got, want) {
		t.Fatalf("\ngot  %x\nwant %x", got, want)
	}
}

func TestEmptyListWritesTagEnd(t *testing.T) {
	got := Marshal(Compound{
		"empty": List{ElemTag: TagInt, Items: nil},
	})
	expected := "" +
		"0a" + "0000" +
		"09" + "0005" + "656d707479" + // TAG_List "empty"
		"00" + "00000000" + // empty list collapses to elem tag = End
		"00"
	want, _ := hex.DecodeString(expected)
	if !bytes.Equal(got, want) {
		t.Fatalf("\ngot  %x\nwant %x", got, want)
	}
}

func TestLongArray(t *testing.T) {
	got := Marshal(Compound{
		"data": LongArray{0x0102030405060708, -1},
	})
	expected := "" +
		"0a" + "0000" +
		"0c" + "0004" + "64617461" + // TAG_LongArray "data"
		"00000002" + "0102030405060708" + "ffffffffffffffff" +
		"00"
	want, _ := hex.DecodeString(expected)
	if !bytes.Equal(got, want) {
		t.Fatalf("\ngot  %x\nwant %x", got, want)
	}
}

func TestListItemTagMismatchPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on tag mismatch")
		}
	}()
	Marshal(Compound{
		"bad": List{ElemTag: TagInt, Items: []Value{String("oops")}},
	})
}
