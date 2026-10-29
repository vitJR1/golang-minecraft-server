package inventory

import (
	"bytes"
	"encoding/json"
	"sort"

	"minecraft-server/nbt"
	"minecraft-server/protocol"
)

// Display names of the server-given hub UI items; SbPlayUseItem dispatches
// on them (not on the slot index), and stripNavigatorItems removes them.
const (
	NavigatorName = "Navigator"
	SelectorName  = "Arena selector"
)

// ChestSlots is a single chest's slot count (generic_9x3).
const ChestSlots = 27

// Stack is one inventory slot's contents. Count 0 means the slot is empty
// (ID is then ignored).
type Stack struct {
	ID    int32
	Count byte
	// Name is an optional custom display name (server-given UI items such as
	// the "Navigator" blaze rod).
	Name string
	// Damage is the wear on a tool (NBT "Damage"); the item breaks when it
	// reaches world.ToolDurability.
	Damage int
	// Enchantments maps enchantment id ("minecraft:efficiency") → level,
	// from the item's NBT "Enchantments" list.
	Enchantments map[string]int
	// Lore is the hover text under the name (NBT display.Lore).
	Lore []string
	// Color is a leather-armour dye as 0xRRGGBB (NBT display.color); 0 = none.
	Color int32
	// Potion is the potion id of a minecraft:potion stack (NBT "Potion").
	Potion string
	// Glint forces the enchanted shimmer without a real enchantment (menu
	// icons): written as a hidden dummy enchantment.
	Glint bool
	// NoDrop: the stack can't leave the player's inventory (game.ItemStack.
	// NoDrop). Server-only, never written to the item's NBT.
	NoDrop bool
}

func (s Stack) Empty() bool { return s.Count == 0 }

// Undroppable reports whether the stack must stay in the player's
// inventory: marked NoDrop by a game, or a hub UI item (navigator, arena
// selector).
func (s Stack) Undroppable() bool {
	return !s.Empty() && (s.NoDrop || s.Name == NavigatorName || s.Name == SelectorName)
}

// SameKind reports whether two stacks can merge: same item and identical
// display / enchantment / potion data (count and wear aside).
func (s Stack) SameKind(o Stack) bool {
	if s.ID != o.ID || s.Name != o.Name || s.Color != o.Color || s.Potion != o.Potion || s.Glint != o.Glint || s.NoDrop != o.NoDrop {
		return false
	}
	if s.Damage != o.Damage || len(s.Lore) != len(o.Lore) || len(s.Enchantments) != len(o.Enchantments) {
		return false
	}
	for i := range s.Lore {
		if s.Lore[i] != o.Lore[i] {
			return false
		}
	}
	for k, v := range s.Enchantments {
		if o.Enchantments[k] != v {
			return false
		}
	}
	return true
}

// EnchantLevel returns the level of an enchantment on the stack (0 = none).
func (s Stack) EnchantLevel(id string) int { return s.Enchantments[id] }

// Tag rebuilds the stack's NBT (name, damage, enchantments); nil when plain.
func (s Stack) Tag() nbt.Compound {
	var tag nbt.Compound
	set := func(k string, v nbt.Value) {
		if tag == nil {
			tag = nbt.Compound{}
		}
		tag[k] = v
	}
	if s.Name != "" || len(s.Lore) > 0 || s.Color != 0 {
		display := nbt.Compound{}
		if s.Name != "" {
			display["Name"] = nbt.String(textJSON(s.Name))
		}
		if len(s.Lore) > 0 {
			lore := nbt.List{ElemTag: nbt.TagString}
			for _, line := range s.Lore {
				lore.Items = append(lore.Items, nbt.String(textJSON(line)))
			}
			display["Lore"] = lore
		}
		if s.Color != 0 {
			display["color"] = nbt.Int(s.Color)
		}
		set("display", display)
	}
	if s.Damage > 0 {
		set("Damage", nbt.Int(int32(s.Damage)))
	}
	if s.Potion != "" {
		set("Potion", nbt.String(s.Potion))
	}
	if len(s.Enchantments) > 0 {
		ids := make([]string, 0, len(s.Enchantments))
		for id := range s.Enchantments {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		list := nbt.List{ElemTag: nbt.TagCompound}
		for _, id := range ids {
			list.Items = append(list.Items, nbt.Compound{"id": nbt.String(id), "lvl": nbt.Short(int16(s.Enchantments[id]))})
		}
		set("Enchantments", list)
	} else if s.Glint {
		// A hidden dummy enchantment gives the shimmer without a tooltip line.
		set("Enchantments", nbt.List{ElemTag: nbt.TagCompound, Items: []nbt.Value{
			nbt.Compound{"id": nbt.String("minecraft:unbreaking"), "lvl": nbt.Short(1)}}})
		set("HideFlags", nbt.Int(1))
	}
	return tag
}

// textJSON wraps plain text (§ codes allowed; vanilla renders them) as a
// chat component. Italic is switched off so names/lore look like vanilla
// item text rather than the default italic custom name.
func textJSON(text string) string {
	return `{"text":"` + protocol.EscapeJSON(text) + `","italic":false}`
}

// textFromJSON extracts the plain text of a chat component string; a
// non-JSON value is returned as-is.
func textFromJSON(raw string) string {
	var comp struct {
		Text  string `json:"text"`
		Extra []struct {
			Text string `json:"text"`
		} `json:"extra"`
	}
	if err := json.Unmarshal([]byte(raw), &comp); err != nil {
		var plain string
		if json.Unmarshal([]byte(raw), &plain) == nil {
			return plain
		}
		return raw
	}
	out := comp.Text
	for _, e := range comp.Extra {
		out += e.Text
	}
	return out
}

// WriteSlot appends the wire Slot for st (empty, plain, or tagged).
func WriteSlot(buf *bytes.Buffer, st Stack) {
	if st.Empty() {
		buf.Write(protocol.WriteEmptySlot())
		return
	}
	buf.Write(protocol.WriteSlotTagged(st.ID, st.Count, st.Tag()))
}

// FromTag fills Damage/Enchantments/display/Potion from a slot's NBT
// compound (the client echoes the full tag back in container clicks).
func FromTag(st Stack, tag nbt.Compound) Stack {
	if d, ok := tag["Damage"].(nbt.Int); ok {
		st.Damage = int(d)
	}
	if potion, ok := tag["Potion"].(nbt.String); ok {
		st.Potion = string(potion)
	}
	if display, ok := tag["display"].(nbt.Compound); ok {
		if name, ok := display["Name"].(nbt.String); ok {
			st.Name = textFromJSON(string(name))
		}
		if lore, ok := display["Lore"].(nbt.List); ok {
			for _, line := range lore.Items {
				if sline, ok := line.(nbt.String); ok {
					st.Lore = append(st.Lore, textFromJSON(string(sline)))
				}
			}
		}
		if color, ok := display["color"].(nbt.Int); ok {
			st.Color = int32(color)
		}
	}
	if hide, ok := tag["HideFlags"].(nbt.Int); ok && hide&1 != 0 {
		// Our glint marker: a hidden single dummy enchantment.
		if list, ok := tag["Enchantments"].(nbt.List); ok && len(list.Items) == 1 {
			if ench, ok := list.Items[0].(nbt.Compound); ok {
				if id, _ := ench["id"].(nbt.String); id == "minecraft:unbreaking" {
					st.Glint = true
					return st
				}
			}
		}
	}
	if list, ok := tag["Enchantments"].(nbt.List); ok {
		for _, item := range list.Items {
			ench, ok := item.(nbt.Compound)
			if !ok {
				continue
			}
			id, _ := ench["id"].(nbt.String)
			if id == "" {
				continue
			}
			lvl := 0
			switch v := ench["lvl"].(type) {
			case nbt.Short:
				lvl = int(v)
			case nbt.Int:
				lvl = int(v)
			case nbt.Byte:
				lvl = int(v)
			}
			if lvl > 0 {
				if st.Enchantments == nil {
					st.Enchantments = map[string]int{}
				}
				st.Enchantments[string(id)] = lvl
			}
		}
	}
	return st
}

// ReadSlot reads one wire Slot from buf: present bool, then (item id, count,
// NBT). The NBT is skipped via nbt.SkipTag. Returns the stack (Count 0 when the
// slot is absent) and whether the read succeeded.
func ReadSlot(buf *bytes.Buffer) (Stack, bool) {
	present, err := protocol.ReadBool(buf)
	if err != nil {
		return Stack{}, false
	}
	if !present {
		return Stack{}, true // empty slot
	}
	id, err := protocol.ReadVarInt(buf)
	if err != nil {
		return Stack{}, false
	}
	count, err := buf.ReadByte()
	if err != nil {
		return Stack{}, false
	}
	// Measure the item's optional NBT, then decode it for the fields we
	// model (Damage, Enchantments, display). Client stacks are only ever
	// compared against the model (click.go), never stored, except a
	// creative player's Set Creative Mode Slot.
	r := bytes.NewReader(buf.Bytes())
	before := r.Len()
	if err := nbt.SkipTag(r); err != nil {
		return Stack{}, false
	}
	raw := buf.Next(before - r.Len())
	st := Stack{ID: int32(id), Count: count}
	if len(raw) > 1 {
		if tag, err := nbt.UnmarshalNetwork(raw); err == nil {
			st = FromTag(st, tag)
		}
	}
	return st, true
}
