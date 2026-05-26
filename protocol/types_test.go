package protocol

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

func TestVarIntKnownEncodings(t *testing.T) {
	// Reference: wiki.vg VarInt examples.
	cases := []struct {
		value int32
		hex   []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{2, []byte{0x02}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{255, []byte{0xff, 0x01}},
		{25565, []byte{0xdd, 0xc7, 0x01}},
		{2097151, []byte{0xff, 0xff, 0x7f}},
		{math.MaxInt32, []byte{0xff, 0xff, 0xff, 0xff, 0x07}},
		{-1, []byte{0xff, 0xff, 0xff, 0xff, 0x0f}},
		{math.MinInt32, []byte{0x80, 0x80, 0x80, 0x80, 0x08}},
	}
	for _, c := range cases {
		got := WriteVarInt32(c.value)
		if !bytes.Equal(got, c.hex) {
			t.Errorf("WriteVarInt32(%d) = %x, want %x", c.value, got, c.hex)
		}
	}
}

func TestVarIntRoundTrip(t *testing.T) {
	values := []int32{
		0, 1, -1, 127, 128, 255, 256, 16383, 16384,
		math.MaxInt32, math.MinInt32, 25565, -100, 2097151, 2097152,
	}
	for _, v := range values {
		encoded := WriteVarInt32(v)
		buf := bytes.NewBuffer(encoded)
		got, err := ReadVarInt(buf)
		if err != nil {
			t.Errorf("VarInt(%d): %x: read err %v", v, encoded, err)
			continue
		}
		if int32(got) != v {
			t.Errorf("VarInt round-trip: %d → %x → %d", v, encoded, got)
		}
		if buf.Len() != 0 {
			t.Errorf("VarInt(%d): %d trailing bytes", v, buf.Len())
		}
	}
}

func TestVarIntFromBytesAgreesWithReader(t *testing.T) {
	for _, v := range []int32{0, 1, 25565, math.MaxInt32, -1, math.MinInt32} {
		encoded := WriteVarInt32(v)
		fromReader, err := ReadVarIntFromReader(bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		fromBuf, err := ReadVarInt(bytes.NewBuffer(encoded))
		if err != nil {
			t.Fatal(err)
		}
		fromBytes, n, err := ReadVarIntFromBytes(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if int32(fromBuf) != fromReader || int32(fromBytes) != fromReader {
			t.Errorf("VarInt(%d) read paths diverged: reader=%d buf=%d bytes=%d",
				v, fromReader, fromBuf, fromBytes)
		}
		if n != len(encoded) {
			t.Errorf("VarInt(%d): consumed %d bytes, encoded len %d", v, n, len(encoded))
		}
	}
}

func TestVarIntTooLong(t *testing.T) {
	// All continue bits set across more than 5 bytes — must error, not loop.
	overlong := bytes.NewBuffer([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	if _, err := ReadVarInt(overlong); err == nil {
		t.Fatal("expected error for over-long VarInt")
	}
}

func TestVarIntEmpty(t *testing.T) {
	if _, err := ReadVarInt(bytes.NewBuffer(nil)); err == nil {
		t.Fatal("expected error on empty buffer")
	}
}

func TestStringRoundTrip(t *testing.T) {
	cases := []string{"", "a", "hello world", "русский текст", "минus日本語",
		strings.Repeat("x", 1000)}
	for _, s := range cases {
		encoded := WriteString(s)
		got, err := ReadStringFromBuf(bytes.NewBuffer(encoded))
		if err != nil {
			t.Errorf("String %q: read err %v", s, err)
			continue
		}
		if got != s {
			t.Errorf("String round-trip: %q → %q", s, got)
		}
	}
}

func TestStringTruncated(t *testing.T) {
	// Length prefix says 10, but only 3 bytes follow.
	buf := bytes.NewBuffer(append(WriteVarInt32(10), []byte("abc")...))
	if _, err := ReadStringFromBuf(buf); err == nil {
		t.Fatal("expected error for truncated string")
	}
}

func TestLongRoundTrip(t *testing.T) {
	for _, v := range []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 1234567890} {
		got, err := ReadLong(bytes.NewBuffer(WriteLong(v)))
		if err != nil {
			t.Fatal(err)
		}
		if got != v {
			t.Errorf("Long round-trip: %d → %d", v, got)
		}
	}
}

func TestDoubleRoundTrip(t *testing.T) {
	for _, v := range []float64{0, 1, -1, math.Pi, math.MaxFloat64, math.SmallestNonzeroFloat64,
		math.Inf(1), math.Inf(-1)} {
		got, err := ReadDouble(bytes.NewBuffer(WriteDouble(v)))
		if err != nil {
			t.Fatal(err)
		}
		if got != v && !(math.IsNaN(v) && math.IsNaN(got)) {
			t.Errorf("Double round-trip: %v → %v", v, got)
		}
	}
}

func TestFloatRoundTrip(t *testing.T) {
	for _, v := range []float32{0, 1, -1, math.MaxFloat32, float32(math.Pi),
		float32(math.Inf(1)), float32(math.Inf(-1))} {
		got, err := ReadFloat(bytes.NewBuffer(WriteFloat(v)))
		if err != nil {
			t.Fatal(err)
		}
		if got != v && !(math.IsNaN(float64(v)) && math.IsNaN(float64(got))) {
			t.Errorf("Float round-trip: %v → %v", v, got)
		}
	}
}

func TestByteArrayTruncated(t *testing.T) {
	// Length prefix says 10, only 3 bytes follow.
	buf := bytes.NewBuffer(append(WriteVarInt32(10), []byte{1, 2, 3}...))
	if _, err := ReadByteArrayFromBuf(buf); err == nil {
		t.Fatal("expected error for truncated byte array")
	}
}

func TestByteArrayNegativeLength(t *testing.T) {
	buf := bytes.NewBuffer(WriteVarInt32(-1))
	if _, err := ReadByteArrayFromBuf(buf); err == nil {
		t.Fatal("expected error for negative byte array length")
	}
}

func TestBoolNonZero(t *testing.T) {
	for _, b := range []byte{0x01, 0x02, 0xff} {
		got, err := ReadBool(bytes.NewBuffer([]byte{b}))
		if err != nil {
			t.Fatal(err)
		}
		if !got {
			t.Errorf("ReadBool(%x) = false, want true (any non-zero is true)", b)
		}
	}
	got, _ := ReadBool(bytes.NewBuffer([]byte{0}))
	if got {
		t.Error("ReadBool(0) = true")
	}
}
