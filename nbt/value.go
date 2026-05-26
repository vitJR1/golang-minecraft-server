package nbt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

// Value is any NBT-typed value. The concrete value types — Byte, Short, Int,
// Long, Float, Double, String, ByteArray, IntArray, LongArray, List, Compound
// — correspond to NBT tags 1..12. TagEnd is internal.
type Value interface {
	Tag() Tag
	writePayload(buf *bytes.Buffer)
}

type (
	Byte      int8
	Short     int16
	Int       int32
	Long      int64
	Float     float32
	Double    float64
	String    string
	ByteArray []byte
	IntArray  []int32
	LongArray []int64
	Compound  map[string]Value

	// List is a homogeneous NBT list. ElemTag must match every item's Tag(),
	// or be TagEnd when Items is empty.
	List struct {
		ElemTag Tag
		Items   []Value
	}
)

func (Byte) Tag() Tag      { return TagByte }
func (Short) Tag() Tag     { return TagShort }
func (Int) Tag() Tag       { return TagInt }
func (Long) Tag() Tag      { return TagLong }
func (Float) Tag() Tag     { return TagFloat }
func (Double) Tag() Tag    { return TagDouble }
func (String) Tag() Tag    { return TagString }
func (ByteArray) Tag() Tag { return TagByteArray }
func (IntArray) Tag() Tag  { return TagIntArray }
func (LongArray) Tag() Tag { return TagLongArray }
func (List) Tag() Tag      { return TagList }
func (Compound) Tag() Tag  { return TagCompound }

// Bool returns Byte(0) or Byte(1). NBT has no native boolean — clients read it
// as TagByte and treat any non-zero as true.
func Bool(b bool) Byte {
	if b {
		return 1
	}
	return 0
}

func (v Byte) writePayload(b *bytes.Buffer) { b.WriteByte(byte(v)) }

func (v Short) writePayload(b *bytes.Buffer) {
	var x [2]byte
	binary.BigEndian.PutUint16(x[:], uint16(v))
	b.Write(x[:])
}

func (v Int) writePayload(b *bytes.Buffer) {
	var x [4]byte
	binary.BigEndian.PutUint32(x[:], uint32(v))
	b.Write(x[:])
}

func (v Long) writePayload(b *bytes.Buffer) {
	var x [8]byte
	binary.BigEndian.PutUint64(x[:], uint64(v))
	b.Write(x[:])
}

func (v Float) writePayload(b *bytes.Buffer) {
	var x [4]byte
	binary.BigEndian.PutUint32(x[:], math.Float32bits(float32(v)))
	b.Write(x[:])
}

func (v Double) writePayload(b *bytes.Buffer) {
	var x [8]byte
	binary.BigEndian.PutUint64(x[:], math.Float64bits(float64(v)))
	b.Write(x[:])
}

func (v String) writePayload(b *bytes.Buffer) {
	if len(v) > math.MaxUint16 {
		panic(fmt.Sprintf("nbt: string too long (%d > %d)", len(v), math.MaxUint16))
	}
	var prefix [2]byte
	binary.BigEndian.PutUint16(prefix[:], uint16(len(v)))
	b.Write(prefix[:])
	b.WriteString(string(v))
}

func (v ByteArray) writePayload(b *bytes.Buffer) {
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(v)))
	b.Write(prefix[:])
	b.Write(v)
}

func (v IntArray) writePayload(b *bytes.Buffer) {
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(v)))
	b.Write(prefix[:])
	var tmp [4]byte
	for _, n := range v {
		binary.BigEndian.PutUint32(tmp[:], uint32(n))
		b.Write(tmp[:])
	}
}

func (v LongArray) writePayload(b *bytes.Buffer) {
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(v)))
	b.Write(prefix[:])
	var tmp [8]byte
	for _, n := range v {
		binary.BigEndian.PutUint64(tmp[:], uint64(n))
		b.Write(tmp[:])
	}
}

func (v List) writePayload(b *bytes.Buffer) {
	elem := v.ElemTag
	if len(v.Items) == 0 {
		elem = TagEnd
	}
	b.WriteByte(byte(elem))
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(v.Items)))
	b.Write(prefix[:])
	for i, item := range v.Items {
		if item.Tag() != v.ElemTag {
			panic(fmt.Sprintf("nbt: list item %d has tag %s, list declared %s",
				i, item.Tag(), v.ElemTag))
		}
		item.writePayload(b)
	}
}

func (v Compound) writePayload(b *bytes.Buffer) {
	// Sort keys so output is deterministic (vanilla clients don't require any
	// specific order, but determinism is nice for testing and diffs).
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		val := v[name]
		b.WriteByte(byte(val.Tag()))
		writeName(b, name)
		val.writePayload(b)
	}
	b.WriteByte(byte(TagEnd))
}

func writeName(b *bytes.Buffer, name string) {
	if len(name) > math.MaxUint16 {
		panic(fmt.Sprintf("nbt: tag name too long (%d > %d)", len(name), math.MaxUint16))
	}
	var prefix [2]byte
	binary.BigEndian.PutUint16(prefix[:], uint16(len(name)))
	b.Write(prefix[:])
	b.WriteString(name)
}

// Marshal encodes a Compound as a full NBT document. The root tag is a named
// Compound with an empty name, which is the format the 1.20.1 protocol expects
// for embedded NBT fields (registry codec, heightmaps, item tags).
func Marshal(root Compound) []byte {
	var b bytes.Buffer
	b.WriteByte(byte(TagCompound))
	writeName(&b, "")
	root.writePayload(&b)
	return b.Bytes()
}
