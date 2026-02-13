package nbt

import (
	"bytes"
	"encoding/binary"
)

const (
	TagEnd      = 0
	TagByte     = 1
	TagInt      = 3
	TagFloat    = 5
	TagDouble   = 6
	TagString   = 8
	TagList     = 9
	TagCompound = 10
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

func (w *Writer) writeStringRaw(s string) {
	binary.Write(&w.buf, binary.BigEndian, uint16(len(s)))
	w.buf.WriteString(s)
}

func (w *Writer) writeTagHeader(tag byte, name string) {
	w.buf.WriteByte(tag)
	w.writeStringRaw(name)
}

func (w *Writer) Byte(name string, v byte) {
	w.writeTagHeader(TagByte, name)
	w.buf.WriteByte(v)
}

func (w *Writer) Int(name string, v int32) {
	w.writeTagHeader(TagInt, name)
	binary.Write(&w.buf, binary.BigEndian, v)
}

func (w *Writer) Float(name string, v float32) {
	w.writeTagHeader(TagFloat, name)
	binary.Write(&w.buf, binary.BigEndian, v)
}

func (w *Writer) Double(name string, v float64) {
	w.writeTagHeader(TagDouble, name)
	binary.Write(&w.buf, binary.BigEndian, v)
}

func (w *Writer) String(name string, v string) {
	w.writeTagHeader(TagString, name)
	w.writeStringRaw(v)
}

func (w *Writer) StartCompound(name string) {
	w.writeTagHeader(TagCompound, name)
}

func (w *Writer) EndCompound() {
	w.buf.WriteByte(TagEnd)
}

func (w *Writer) StartList(name string, elementType byte, length int32) {
	w.writeTagHeader(TagList, name)
	w.buf.WriteByte(elementType)
	binary.Write(&w.buf, binary.BigEndian, length)
}
