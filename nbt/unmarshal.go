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

// Decoder limits. Every length in NBT comes from the input, so a hostile
// peer can declare a 2³¹-element list in a few bytes; these keep a decode
// bounded by what was actually received.
const (
	// MaxDepth is vanilla's nesting limit (NbtAccounter): deeper
	// compounds/lists are rejected before recursion can run away.
	MaxDepth = 512

	// NetworkQuota is vanilla's budget for NBT read off the wire
	// (FriendlyByteBuf.readNbt: 2 MiB). It is charged per tag with an
	// approximation of the in-memory cost (see the account calls), so a
	// packet of tiny empty compounds can't balloon into gigabytes.
	NetworkQuota = 2 << 20
)

// decoder carries the read position plus the limits of one decode. quota < 0
// means unlimited (trusted files such as schematics); depth and length
// checks always apply.
type decoder struct {
	r     *bytes.Reader
	depth int
	quota int64
}

// account charges n bytes against the quota.
func (d *decoder) account(n int64) error {
	if d.quota < 0 {
		return nil
	}
	d.quota -= n
	if d.quota < 0 {
		return fmt.Errorf("nbt exceeds %d-byte quota", NetworkQuota)
	}
	return nil
}

// enter / leave bracket a nested compound or list.
func (d *decoder) enter() error {
	d.depth++
	if d.depth > MaxDepth {
		return fmt.Errorf("nbt nested deeper than %d", MaxDepth)
	}
	return nil
}

func (d *decoder) leave() { d.depth-- }

// fits reports whether n elements of at least size bytes each can still be
// in the input — checked before any allocation sized by n.
func (d *decoder) fits(n int32, size int64) bool {
	return int64(n)*size <= int64(d.r.Len())
}

// Unmarshal parses a root TagCompound out of NBT bytes. The input may be:
//   - raw NBT (first byte = 0x0A, TagCompound)
//   - gzip-compressed (magic 1F 8B)
//   - zlib-compressed (magic 78 followed by 01/9C/DA)
//
// Compression is auto-detected. Returns the root Compound's payload; the
// root's name (always "" in network NBT, sometimes "Schematic" in WorldEdit
// exports) is discarded. Callers that care about the root name should
// parse manually with the lower-level helpers.
//
// No size quota: meant for trusted files. Use UnmarshalNetwork for bytes
// that came from a client.
func Unmarshal(data []byte) (Compound, error) {
	decoded, err := autoDecompress(data)
	if err != nil {
		return nil, err
	}
	return unmarshalRoot(&decoder{r: bytes.NewReader(decoded), quota: -1})
}

// UnmarshalNetwork is Unmarshal for raw (uncompressed) NBT received from a
// client, decoded under NetworkQuota.
func UnmarshalNetwork(data []byte) (Compound, error) {
	return unmarshalRoot(&decoder{r: bytes.NewReader(data), quota: NetworkQuota})
}

func unmarshalRoot(d *decoder) (Compound, error) {
	tagID, err := d.r.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("root tag: %w", err)
	}
	if Tag(tagID) != TagCompound {
		return nil, fmt.Errorf("root must be Compound (0x0A), got 0x%02X", tagID)
	}
	if _, err := readUTF(d.r); err != nil {
		return nil, fmt.Errorf("root name: %w", err)
	}
	return d.readCompoundPayload()
}

// SkipTag reads and discards one tag from r. Used to consume the optional NBT
// trailing a network "Slot": a single 0x00 byte (TagEnd) means "no NBT" and is
// consumed as-is; any other tag is a type byte + name + payload (the empty-name
// compound items use). Lets a slot reader advance past item NBT it doesn't need.
// The input is client-controlled, so it is decoded under NetworkQuota.
func SkipTag(r *bytes.Reader) error {
	tagID, err := r.ReadByte()
	if err != nil {
		return err
	}
	if Tag(tagID) == TagEnd {
		return nil
	}
	if _, err := readUTF(r); err != nil {
		return err
	}
	d := &decoder{r: r, quota: NetworkQuota}
	_, err = d.readPayload(Tag(tagID))
	return err
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
func (d *decoder) readCompoundPayload() (Compound, error) {
	if err := d.enter(); err != nil {
		return nil, err
	}
	defer d.leave()
	if err := d.account(48); err != nil {
		return nil, err
	}
	out := Compound{}
	for {
		tagID, err := d.r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("compound entry tag: %w", err)
		}
		if Tag(tagID) == TagEnd {
			return out, nil
		}
		name, err := readUTF(d.r)
		if err != nil {
			return nil, fmt.Errorf("compound entry name: %w", err)
		}
		if err := d.account(28 + 2*int64(len(name))); err != nil {
			return nil, err
		}
		val, err := d.readPayload(Tag(tagID))
		if err != nil {
			return nil, fmt.Errorf("compound %q: %w", name, err)
		}
		out[name] = val
	}
}

// minPayloadSize is the fewest input bytes one payload of tag can occupy;
// 0 for tags that can't appear as list elements.
func minPayloadSize(tag Tag) int64 {
	switch tag {
	case TagByte, TagCompound: // a compound is at least its TagEnd
		return 1
	case TagShort, TagString: // string = u16 length
		return 2
	case TagInt, TagFloat, TagByteArray, TagIntArray, TagLongArray: // arrays = i32 length
		return 4
	case TagList: // element tag + i32 length
		return 5
	case TagLong, TagDouble:
		return 8
	}
	return 0
}

// readPayload decodes a value of the given tag type. Tag-specific layouts
// match Marshal/writePayload exactly. Every length read from the input is
// checked against the remaining bytes before it sizes an allocation.
func (d *decoder) readPayload(tag Tag) (Value, error) {
	r := d.r
	switch tag {
	case TagByte:
		if err := d.account(9); err != nil {
			return nil, err
		}
		b, err := r.ReadByte()
		return Byte(int8(b)), err
	case TagShort:
		if err := d.account(10); err != nil {
			return nil, err
		}
		v, err := readInt16(r)
		return Short(v), err
	case TagInt:
		if err := d.account(12); err != nil {
			return nil, err
		}
		v, err := readInt32(r)
		return Int(v), err
	case TagLong:
		if err := d.account(16); err != nil {
			return nil, err
		}
		v, err := readInt64(r)
		return Long(v), err
	case TagFloat:
		if err := d.account(12); err != nil {
			return nil, err
		}
		bits, err := readUint32(r)
		return Float(math.Float32frombits(bits)), err
	case TagDouble:
		if err := d.account(16); err != nil {
			return nil, err
		}
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
		if !d.fits(n, 1) {
			return nil, fmt.Errorf("byte array length %d exceeds input", n)
		}
		if err := d.account(24 + int64(n)); err != nil {
			return nil, err
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return ByteArray(buf), nil
	case TagString:
		s, err := readUTF(r)
		if err != nil {
			return nil, err
		}
		if err := d.account(36 + 2*int64(len(s))); err != nil {
			return nil, err
		}
		return String(s), nil
	case TagList:
		if err := d.enter(); err != nil {
			return nil, err
		}
		defer d.leave()
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
		// A non-empty list needs a real element type, and room for n of them.
		if n > 0 {
			size := minPayloadSize(elemTag)
			if size == 0 {
				return nil, fmt.Errorf("list of %d elements with tag 0x%02X", n, elemTag)
			}
			if !d.fits(n, size) {
				return nil, fmt.Errorf("list length %d exceeds input", n)
			}
		}
		if err := d.account(37 + 4*int64(n)); err != nil {
			return nil, err
		}
		items := make([]Value, n)
		for i := int32(0); i < n; i++ {
			v, err := d.readPayload(elemTag)
			if err != nil {
				return nil, fmt.Errorf("list[%d]: %w", i, err)
			}
			items[i] = v
		}
		return List{ElemTag: elemTag, Items: items}, nil
	case TagCompound:
		return d.readCompoundPayload()
	case TagIntArray:
		n, err := readInt32(r)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, fmt.Errorf("negative int array length: %d", n)
		}
		if !d.fits(n, 4) {
			return nil, fmt.Errorf("int array length %d exceeds input", n)
		}
		if err := d.account(24 + 4*int64(n)); err != nil {
			return nil, err
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
		if !d.fits(n, 8) {
			return nil, fmt.Errorf("long array length %d exceeds input", n)
		}
		if err := d.account(24 + 8*int64(n)); err != nil {
			return nil, err
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
