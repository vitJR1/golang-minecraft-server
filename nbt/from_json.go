package nbt

import (
	"encoding/json"
	"fmt"
	"math"
)

// TypeHints resolves JSON's number-type ambiguity for NBT output. JSON has a
// single number type, but NBT distinguishes Byte/Int/Long/Float/Double — the
// "correct" choice depends on what the consuming client expects per field.
//
// Lookups are by tag NAME (the compound key the number lives under). For
// numbers inside lists the list's own name is used.
//
// Precedence: ByteKeys (only when value is 0 or 1) > LongKeys > FloatKeys >
// DoubleKeys > automatic (Int for whole numbers in int32 range, Long for
// larger whole numbers, Double for fractional).
type TypeHints struct {
	ByteKeys   map[string]bool
	FloatKeys  map[string]bool
	DoubleKeys map[string]bool
	LongKeys   map[string]bool
}

// FromJSONBytes parses a JSON document and converts it to an NBT root Compound.
func FromJSONBytes(data []byte, hints TypeHints) (Compound, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	return convertCompound(root, hints)
}

func convertValue(name string, v any, hints TypeHints) (Value, error) {
	switch x := v.(type) {
	case string:
		return String(x), nil
	case bool:
		return Bool(x), nil
	case float64:
		return convertNumber(name, x, hints), nil
	case []any:
		return convertList(name, x, hints)
	case map[string]any:
		return convertCompound(x, hints)
	case nil:
		return nil, fmt.Errorf("null value for key %q (NBT has no null tag)", name)
	default:
		return nil, fmt.Errorf("unsupported JSON type for %q: %T", name, v)
	}
}

func convertNumber(name string, x float64, hints TypeHints) Value {
	if hints.ByteKeys[name] && (x == 0 || x == 1) {
		return Byte(int8(x))
	}
	if hints.LongKeys[name] {
		return Long(int64(x))
	}
	if hints.FloatKeys[name] {
		return Float(x)
	}
	if hints.DoubleKeys[name] {
		return Double(x)
	}
	if math.Trunc(x) == x {
		if x >= math.MinInt32 && x <= math.MaxInt32 {
			return Int(int32(x))
		}
		return Long(int64(x))
	}
	return Double(x)
}

func convertCompound(m map[string]any, hints TypeHints) (Compound, error) {
	out := make(Compound, len(m))
	for k, v := range m {
		val, err := convertValue(k, v, hints)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = val
	}
	return out, nil
}

func convertList(name string, arr []any, hints TypeHints) (Value, error) {
	if len(arr) == 0 {
		return List{ElemTag: TagEnd, Items: nil}, nil
	}
	items := make([]Value, len(arr))
	for i, raw := range arr {
		v, err := convertValue(name, raw, hints)
		if err != nil {
			return nil, fmt.Errorf("[%d]: %w", i, err)
		}
		items[i] = v
	}
	elemTag := items[0].Tag()
	for i, it := range items {
		if it.Tag() != elemTag {
			return nil, fmt.Errorf("list %q has mixed types: items[0]=%s items[%d]=%s",
				name, elemTag, i, it.Tag())
		}
	}
	return List{ElemTag: elemTag, Items: items}, nil
}
