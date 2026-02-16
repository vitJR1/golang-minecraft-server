package nbt

import (
	"bytes"
	"encoding/binary"
)

const (
	TagEnd       = 0
	TagByte      = 1
	TagShort     = 2
	TagInt       = 3
	TagLong      = 4
	TagFloat     = 5
	TagDouble    = 6
	TagByteArray = 7
	TagString    = 8
	TagList      = 9
	TagCompound  = 10
	TagIntArray  = 11
	TagLongArray = 12
)

type Writer struct {
	buf bytes.Buffer
}

func New() *Writer {
	return &Writer{}
}

func (w *Writer) Bytes() []byte {
	return w.buf.Bytes()
}

// Write a string with length prefix (UTF-8)
func (w *Writer) WriteString(s string) {
	binary.Write(&w.buf, binary.BigEndian, uint16(len(s)))
	w.buf.WriteString(s)
}

// Write a string as a tag (with name)
func (w *Writer) String(name string, value string) {
	w.writeTagHeader(TagString, name)
	w.WriteString(value)
}

// Write a byte
func (w *Writer) Byte(name string, value byte) {
	w.writeTagHeader(TagByte, name)
	w.buf.WriteByte(value)
}

// Write a boolean (as byte)
func (w *Writer) Bool(name string, value bool) {
	w.writeTagHeader(TagByte, name)
	if value {
		w.buf.WriteByte(1)
	} else {
		w.buf.WriteByte(0)
	}
}

// Write a short (int16)
func (w *Writer) Short(name string, value int16) {
	w.writeTagHeader(TagShort, name)
	binary.Write(&w.buf, binary.BigEndian, value)
}

// Write an int (int32)
func (w *Writer) Int(name string, value int32) {
	w.writeTagHeader(TagInt, name)
	binary.Write(&w.buf, binary.BigEndian, value)
}

// Write a long (int64)
func (w *Writer) Long(name string, value int64) {
	w.writeTagHeader(TagLong, name)
	binary.Write(&w.buf, binary.BigEndian, value)
}

// Write a float
func (w *Writer) Float(name string, value float32) {
	w.writeTagHeader(TagFloat, name)
	binary.Write(&w.buf, binary.BigEndian, value)
}

// Write a double
func (w *Writer) Double(name string, value float64) {
	w.writeTagHeader(TagDouble, name)
	binary.Write(&w.buf, binary.BigEndian, value)
}

// Write a byte array
func (w *Writer) ByteArray(name string, value []byte) {
	w.writeTagHeader(TagByteArray, name)
	binary.Write(&w.buf, binary.BigEndian, int32(len(value)))
	w.buf.Write(value)
}

// Write an int array
func (w *Writer) IntArray(name string, value []int32) {
	w.writeTagHeader(TagIntArray, name)
	binary.Write(&w.buf, binary.BigEndian, int32(len(value)))
	for _, v := range value {
		binary.Write(&w.buf, binary.BigEndian, v)
	}
}

// Write an int array
func (w *Writer) IntArray64(name string, value []int64) {
	w.writeTagHeader(TagIntArray, name)
	binary.Write(&w.buf, binary.BigEndian, int64(len(value)))
	for _, v := range value {
		binary.Write(&w.buf, binary.BigEndian, v)
	}
}

// Write a long array
func (w *Writer) LongArray(name string, value []int64) {
	w.writeTagHeader(TagLongArray, name)
	binary.Write(&w.buf, binary.BigEndian, int32(len(value)))
	for _, v := range value {
		binary.Write(&w.buf, binary.BigEndian, v)
	}
}

// Start a compound
func (w *Writer) StartCompound(name string) {
	w.writeTagHeader(TagCompound, name)
}

// End a compound (write TagEnd)
func (w *Writer) EndCompound() {
	w.buf.WriteByte(TagEnd)
}

// Start a list
func (w *Writer) StartList(name string, elementType byte, length int32) {
	w.writeTagHeader(TagList, name)
	w.buf.WriteByte(elementType)
	binary.Write(&w.buf, binary.BigEndian, length)
}

// Helper to write tag header
func (w *Writer) writeTagHeader(tag byte, name string) {
	w.buf.WriteByte(tag)
	w.WriteString(name)
}

func (w *Writer) WriteRootCompound() {
	w.buf.WriteByte(TagCompound)
	w.WriteString("") // Empty string for root name
}
