package nbt

// Tag is an NBT tag type id. Spec: https://wiki.vg/NBT
type Tag byte

const (
	TagEnd       Tag = 0
	TagByte      Tag = 1
	TagShort     Tag = 2
	TagInt       Tag = 3
	TagLong      Tag = 4
	TagFloat     Tag = 5
	TagDouble    Tag = 6
	TagByteArray Tag = 7
	TagString    Tag = 8
	TagList      Tag = 9
	TagCompound  Tag = 10
	TagIntArray  Tag = 11
	TagLongArray Tag = 12
)

func (t Tag) String() string {
	switch t {
	case TagEnd:
		return "End"
	case TagByte:
		return "Byte"
	case TagShort:
		return "Short"
	case TagInt:
		return "Int"
	case TagLong:
		return "Long"
	case TagFloat:
		return "Float"
	case TagDouble:
		return "Double"
	case TagByteArray:
		return "ByteArray"
	case TagString:
		return "String"
	case TagList:
		return "List"
	case TagCompound:
		return "Compound"
	case TagIntArray:
		return "IntArray"
	case TagLongArray:
		return "LongArray"
	default:
		return "Unknown"
	}
}
