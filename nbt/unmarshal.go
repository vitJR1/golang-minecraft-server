package nbt

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// Unmarshal parses a root TagCompound out of NBT bytes. The input may be:
//   - raw NBT (first byte = 0x0A, TagCompound)
//   - gzip-compressed (magic 1F 8B)
//   - zlib-compressed (magic 78 followed by 01/9C/DA)
//
// Compression is auto-detected. Returns the root Compound's payload; the
// root's name (always "" in network NBT, sometimes "Schematic" in WorldEdit
// exports) is discarded. Callers that care about the root name should
// parse manually with the lower-level helpers.
func Unmarshal(data []byte) (Compound, error) {
	decoded, err := autoDecompress(data)
	if err != nil {
		return nil, err
	}
	r := bytes.NewReader(decoded)

	tagID, err := r.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("root tag: %w", err)
	}
	if Tag(tagID) != TagCompound {
		return nil, fmt.Errorf("root must be Compound (0x0A), got 0x%02X", tagID)
	}
	if _, err := readUTF(r); err != nil {
		return nil, fmt.Errorf("root name: %w", err)
	}
	return readCompoundPayload(r)
}

// autoDecompress sniffs the magic bytes and routes through the right
// decoder. Returns the original slice when the data looks like raw NBT.
func autoDecompress(data []byte) ([]byte, error) {
	if len(data) < 2 {
		return data, nil
	}
	switch {
	case data[0] == 0x1F && data[1] == 0x8B: // gzip
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		defer gz.Close()
		return io.ReadAll(gz)
	case data[0] == 0x78 && (data[1] == 0x01 || data[1] == 0x9C || data[1] == 0xDA): // zlib
		zr, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("zlib: %w", err)
		}
		defer zr.Close()
		return io.ReadAll(zr)
	}
	return data, nil
}

// readCompoundPayload reads tag-name-payload triples until a TagEnd.
func readCompoundPayload(r *bytes.Reader) (Compound, error) {
	out := Compound{}
	for {
		tagID, err := r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("compound entry tag: %w", err)
		}
		if Tag(tagID) == TagEnd {
			return out, nil
		}
		name, err := readUTF(r)
		if err != nil {
			return nil, fmt.Errorf("compound entry name: %w", err)
		}
		val, err := readPayload(r, Tag(tagID))
		if err != nil {
			return nil, fmt.Errorf("compound %q: %w", name, err)
		}
		out[name] = val
	}
}

// readPayload decodes a value of the given tag type. Tag-specific layouts
// match Marshal/writePayload exactly.
func readPayload(r *bytes.Reader, tag Tag) (Value, error) {
	switch tag {
	case TagByte:
		b, err := r.ReadByte()
		return Byte(int8(b)), err
	case TagShort:
		v, err := readInt16(r)
		return Short(v), err
	case TagInt:
		v, err := readInt32(r)
		return Int(v), err
	case TagLong:
		v, err := readInt64(r)
		return Long(v), err
	case TagFloat:
		bits, err := readUint32(r)
		return Float(math.Float32frombits(bits)), err
	case TagDouble:
		bits, err := readUint64(r)
		return Double(math.Float64frombits(bits)), err
	case TagByteArray:
		n, err := readInt32(r)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, fmt.Errorf("negative byte array length: %d", n)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return ByteArray(buf), nil
	case TagString:
		s, err := readUTF(r)
		return String(s), err
	case TagList:
		elemTagByte, err := r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("list element tag: %w", err)
		}
		n, err := readInt32(r)
		if err != nil {
			return nil, fmt.Errorf("list length: %w", err)
		}
		if n < 0 {
			return nil, fmt.Errorf("negative list length: %d", n)
		}
		elemTag := Tag(elemTagByte)
		// Spec quirk: an empty list may declare elem = TagEnd. Keep what
		// the wire says so a Marshal round-trip stays byte-identical.
		items := make([]Value, n)
		for i := int32(0); i < n; i++ {
			v, err := readPayload(r, elemTag)
			if err != nil {
				return nil, fmt.Errorf("list[%d]: %w", i, err)
			}
			items[i] = v
		}
		return List{ElemTag: elemTag, Items: items}, nil
	case TagCompound:
		return readCompoundPayload(r)
	case TagIntArray:
		n, err := readInt32(r)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, fmt.Errorf("negative int array length: %d", n)
		}
		out := make(IntArray, n)
		for i := int32(0); i < n; i++ {
			v, err := readInt32(r)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case TagLongArray:
		n, err := readInt32(r)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, fmt.Errorf("negative long array length: %d", n)
		}
		out := make(LongArray, n)
		for i := int32(0); i < n; i++ {
			v, err := readInt64(r)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown tag id 0x%02X", tag)
	}
}

// --- Low-level readers (big-endian) ---

func readUTF(r *bytes.Reader) (string, error) {
	n, err := readUint16(r)
	if err != nil {
		return "", fmt.Errorf("utf length: %w", err)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("utf bytes: %w", err)
	}
	return string(buf), nil
}

func readUint16(r *bytes.Reader) (uint16, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b[:]), nil
}

func readInt16(r *bytes.Reader) (int16, error) {
	v, err := readUint16(r)
	return int16(v), err
}

func readUint32(r *bytes.Reader) (uint32, error) {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

func readInt32(r *bytes.Reader) (int32, error) {
	v, err := readUint32(r)
	return int32(v), err
}

func readUint64(r *bytes.Reader) (uint64, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b[:]), nil
}

func readInt64(r *bytes.Reader) (int64, error) {
	v, err := readUint64(r)
	return int64(v), err
}
