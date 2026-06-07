package world

// Entity is a non-block world object. Today only item frames are modelled,
// but the shape is deliberately generic (a namespaced Type plus common
// spatial fields) so paintings / armor stands / etc. can slot in later — a
// type-specific detail pointer (Frame) carries the per-kind state.
//
// Entities live on Template and MemoryWorld alongside blocks; the server
// assigns each a runtime entity ID and spawns it to clients.
type Entity struct {
	Type       string // namespaced entity id, e.g. "minecraft:item_frame"
	X, Y, Z    float64
	Yaw, Pitch float32

	// Frame is non-nil for item_frame / glow_item_frame entities.
	Frame *FrameData
}

// FrameData is the state of an item-frame entity: which block face it's on,
// the item it displays (and that item's rotation), and whether it glows.
type FrameData struct {
	Facing   byte   // 0=down 1=up 2=north 3=south 4=west 5=east (spawn object data)
	Rotation byte   // 0..7, the item's rotation within the frame
	Item     string // namespaced item id displayed, "" = empty frame
	Glowing  bool   // true → glow_item_frame
}

// Entity type registry IDs for protocol 763 (1.20.1), from minecraft-data.
const (
	ItemFrameEntityID     int32 = 56
	GlowItemFrameEntityID int32 = 43
	VillagerEntityID      int32 = 108
)

// entityTypeIDs maps namespaced entity ids to their protocol type id, for the
// entity kinds the server spawns from worlds (item frames, villagers, …).
var entityTypeIDs = map[string]int32{
	"minecraft:item_frame":      ItemFrameEntityID,
	"minecraft:glow_item_frame": GlowItemFrameEntityID,
	"minecraft:villager":        VillagerEntityID,
}

// EntityTypeID returns the protocol type id for a namespaced entity id, and
// whether it's a kind the server knows how to spawn.
func EntityTypeID(name string) (int32, bool) {
	id, ok := entityTypeIDs[name]
	return id, ok
}

// Item-frame facing values (the entity's Facing byte / spawn object data).
const (
	FaceDown  byte = 0
	FaceUp    byte = 1
	FaceNorth byte = 2
	FaceSouth byte = 3
	FaceWest  byte = 4
	FaceEast  byte = 5
)

// itemNames is the reverse of itemIDs (built once at startup), so the server
// can turn the numeric item id in a creative-slot packet back into a name.
var itemNames = func() map[int32]string {
	m := make(map[int32]string, len(itemIDs))
	for name, id := range itemIDs {
		m[id] = name
	}
	return m
}()

// ItemName returns the namespaced item id for a registry id, and whether it's
// known. The inverse of ItemByName.
func ItemName(id int32) (string, bool) {
	n, ok := itemNames[id]
	return n, ok
}

// EntityProvider is implemented by worlds (and exposed by MemoryWorld) that
// carry entities. The server type-asserts to this to discover an instance's
// item frames at construction. Block-only worlds simply don't implement it.
type EntityProvider interface {
	Entities() []Entity
}

// BlockEntityProvider is implemented by worlds that carry block entities
// (beds, chests, banners, skulls, …) — blocks whose visual is drawn by a
// client-side BlockEntityRenderer and which must therefore be listed in the
// Chunk Data packet to show up. Maps each position to its block-entity type
// name ("minecraft:bed").
type BlockEntityProvider interface {
	BlockEntities() map[Position]string
}

// BiomeProvider is implemented by worlds that carry a (uniform) biome, so the
// chunk streamer can paint chunks with the map's biome instead of a default.
type BiomeProvider interface {
	Biome() string
}
