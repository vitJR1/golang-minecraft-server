package nbt

func BuildRegistryNBT() []byte {
	w := New()
	w.WriteRootCompound()

	buildChatTypeNBT(w)
	buildDamageNBT(w)
	buildDimensionTypeNBT(w)
	buildBiomeNBT(w)

	w.EndCompound() // root
	return w.Bytes()
}

func buildChatTypeNBT(w *Writer) {
	w.StartCompound("minecraft:chat_type")
	w.String("type", "minecraft:chat_type")
	w.StartList("value", TagCompound, 1)

	w.StartCompoundPayload()
	w.String("name", "minecraft:chat")
	w.Int("id", 0)

	w.StartCompound("element")

	w.StartCompound("chat")
	w.String("translation_key", "chat.type.text")
	w.StartList("parameters", TagString, 2)
	w.StringPayload("sender")
	w.StringPayload("content")
	w.EndList()
	w.EndCompound()

	w.StartCompound("narration")
	w.String("translation_key", "chat.type.text.narrate")
	w.StartList("parameters", TagString, 2)
	w.StringPayload("sender")
	w.StringPayload("content")
	w.EndList()
	w.EndCompound()

	w.EndCompound()        // element
	w.EndCompoundPayload() // entry payload end

	w.EndList()
	w.EndCompound() // chat_type registry
}

func buildDimensionTypeNBT(w *Writer) {
	// Dimension Type Registry
	w.StartCompound("minecraft:dimension_type")
	w.String("type", "minecraft:dimension_type")
	w.StartList("value", TagCompound, 1)
	w.StartCompoundPayload()
	w.String("name", "minecraft:overworld")
	w.Int("id", 0)
	w.StartCompound("element")
	w.Int("monster_spawn_block_light_limit", 0)

	w.StartCompound("monster_spawn_light_level")
	w.String("type", "minecraft:uniform")
	w.StartCompound("value")
	w.Int("min_inclusive", 0)
	w.Int("max_inclusive", 7)
	w.EndCompound() // value
	w.EndCompound() // monster_spawn_light_level
	w.Bool("piglin_safe", false)
	w.Bool("natural", true)
	w.Float("ambient_light", 0.0)
	w.String("infiniburn", "#minecraft:infiniburn_overworld")
	w.Bool("respawn_anchor_works", false)
	w.Bool("has_skylight", true)
	w.Bool("bed_works", true)
	w.String("effects", "minecraft:overworld")
	w.Bool("has_raids", true)
	w.Int("min_y", -64)
	w.Int("height", 384)
	w.Int("logical_height", 384)
	w.Float("coordinate_scale", 1.0)
	w.Bool("ultrawarm", false)
	w.Bool("has_ceiling", false)
	w.EndCompound() // element
	w.EndCompoundPayload()
	w.EndList()     // value list
	w.EndCompound() // dimension_type registry
}

func buildBiomeNBT(w *Writer) {
	// Biome Registry
	w.StartCompound("minecraft:worldgen/biome")
	w.String("type", "minecraft:worldgen/biome")
	w.StartList("value", TagCompound, 1)
	w.StartCompoundPayload()
	w.String("name", "minecraft:plains")
	w.Int("id", 0)
	w.StartCompound("element")
	w.String("precipitation", "rain")
	w.Float("temperature", 0.8)
	w.Float("downfall", 0.4)
	w.String("temperature_modifier", "none")
	w.StartCompound("effects")
	w.Int("sky_color", 7907327)
	w.Int("water_color", 4159204)
	w.Int("water_fog_color", 329011)
	w.Int("fog_color", 12638463)
	w.EndCompound()        // effects
	w.EndCompound()        // element
	w.EndCompoundPayload() // biome entry
	w.EndList()            // value list
	w.EndCompound()        // biome registry
}

func buildDamageNBT(w *Writer) {
	// Damage Type Registry
	w.StartCompound("minecraft:damage_type")
	w.String("type", "minecraft:damage_type")
	w.StartList("value", TagCompound, 1)

	w.StartCompoundPayload()
	w.String("name", "minecraft:generic")
	w.Int("id", 0)

	w.StartCompound("element")
	w.String("message_id", "generic")
	w.String("scaling", "always")
	w.String("effects", "hurt")
	w.Float("exhaustion", 0.1)
	w.EndCompound() // element

	w.EndCompoundPayload() // entry
	w.EndList()
	w.EndCompound()
}
