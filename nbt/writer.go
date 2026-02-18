package nbt

import (
	_ "encoding/binary"
	"encoding/json"
	"fmt"
	"math"
)

// ====== JSON -> NBT ======

// Эти ключи в registry codec обычно байты 0/1 (NBT TAG_Byte), а не TAG_Int.
// Можно расширять по мере надобности.
var byte01Keys = map[string]struct{}{
	"bed_works":             {},
	"has_ceiling":           {},
	"has_raids":             {},
	"has_skylight":          {},
	"natural":               {},
	"piglin_safe":           {},
	"respawn_anchor_works":  {},
	"ultrawarm":             {},
	"replace_current_music": {},
	"has_precipitation":     {},
	"italic":                {},
}

// Публичная точка: берёшь JSON bytes и получаешь NBT bytes (Root Compound уже будет закрыт)
func BuildRegistryNBTFromJSON(registryJSON []byte) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(registryJSON, &root); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}

	w := New()
	w.WriteRootCompound()

	// каждый реестр — это compound tag с именем "minecraft:chat_type", etc
	for regName, regVal := range root {
		obj, ok := regVal.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("registry %s is not object", regName)
		}

		w.StartCompound(regName)

		// "type": string
		if t, ok := obj["type"].(string); ok {
			w.String("type", t)
		} else {
			return nil, fmt.Errorf("registry %s missing type", regName)
		}

		// "value": []any (list of compounds)
		valueArr, ok := obj["value"].([]any)
		if !ok {
			return nil, fmt.Errorf("registry %s missing value array", regName)
		}

		w.StartList("value", TagCompound, int32(len(valueArr)))
		for i := 0; i < len(valueArr); i++ {
			entry, ok := valueArr[i].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("registry %s value[%d] not object", regName, i)
			}

			// list<TagCompound> => каждый элемент = compound payload (без заголовка!)
			w.StartCompoundPayload()
			if err := writeCompoundFromJSON(w, entry); err != nil {
				return nil, fmt.Errorf("%s value[%d]: %w", regName, i, err)
			}
			w.EndCompoundPayload()
		}
		w.EndList()

		w.EndCompound() // registry compound
	}

	w.EndCompound() // root
	return w.Bytes(), nil
}

func writeCompoundFromJSON(w *Writer, m map[string]any) error {
	for k, v := range m {
		if err := writeTagFromJSON(w, k, v); err != nil {
			return err
		}
	}
	return nil
}

func writeTagFromJSON(w *Writer, name string, v any) error {
	switch x := v.(type) {
	case string:
		w.String(name, x)
		return nil

	case bool:
		w.Bool(name, x)
		return nil

	case float64:
		// json numbers => float64. Решаем тип.
		// 1) если ключ в byte01Keys и значение 0/1 => TAG_Byte
		if _, ok := byte01Keys[name]; ok {
			if x == 0 || x == 1 {
				w.Byte(name, byte(int(x)))
				return nil
			}
		}

		// 2) целое? => TAG_Int (как минимум безопасно)
		if math.Trunc(x) == x && x >= math.MinInt32 && x <= math.MaxInt32 {
			w.Int(name, int32(x))
			return nil
		}

		// 3) иначе => TAG_Double
		w.Double(name, x)
		return nil

	case []any:
		return writeListFromJSON(w, name, x)

	case map[string]any:
		w.StartCompound(name)
		if err := writeCompoundFromJSON(w, x); err != nil {
			return err
		}
		w.EndCompound()
		return nil

	default:
		return fmt.Errorf("unsupported json type for %q: %T", name, v)
	}
}

func writeListFromJSON(w *Writer, name string, arr []any) error {
	// пустой список — ок, но надо выбрать elementType. В registry codec пустых обычно нет.
	if len(arr) == 0 {
		// safest: list<TagEnd> length 0 (NBT разрешает)
		w.StartList(name, TagEnd, 0)
		w.EndList()
		return nil
	}

	// определяем тип по первому элементу
	switch arr[0].(type) {
	case string:
		w.StartList(name, TagString, int32(len(arr)))
		for _, it := range arr {
			s, ok := it.(string)
			if !ok {
				return fmt.Errorf("list %s: mixed types, expected string", name)
			}
			// В list<TagString> элемент = string payload без tag header
			w.StringPayload(s)
		}
		w.EndList()
		return nil

	case float64:
		// Если это “список чисел” в твоих данных — чаще всего это Int list не встречается.
		// На всякий: делаем list<TagInt> только если все целые и в int32.
		allInt32 := true
		for _, it := range arr {
			n, ok := it.(float64)
			if !ok || math.Trunc(n) != n || n < math.MinInt32 || n > math.MaxInt32 {
				allInt32 = false
				break
			}
		}
		if allInt32 {
			w.StartList(name, TagInt, int32(len(arr)))
			for _, _ = range arr {
				w.writeUnnamedTag(TagInt) // ⚠️ У тебя нет отдельного “payload” для int, поэтому пишем как tags без имени нельзя.
				// ПРОЩЕ: НЕ ИСПОЛЬЗОВАТЬ list<TagInt> здесь.
				// Если реально понадобится — добавь IntPayload.
				return fmt.Errorf("list %s: numeric list needs IntPayload implementation", name)
			}
		}
		return fmt.Errorf("list %s: numeric list not supported safely (add payload funcs)", name)

	case map[string]any:
		w.StartList(name, TagCompound, int32(len(arr)))
		for i, it := range arr {
			obj, ok := it.(map[string]any)
			if !ok {
				return fmt.Errorf("list %s[%d]: expected object", name, i)
			}
			w.StartCompoundPayload()
			if err := writeCompoundFromJSON(w, obj); err != nil {
				return err
			}
			w.EndCompoundPayload()
		}
		w.EndList()
		return nil

	default:
		return fmt.Errorf("list %s: unsupported element type %T", name, arr[0])
	}
}
