package server

import (
	"sync"

	"minecraft-server/chunk"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/net/conn"
	"minecraft-server/server/net/registry"
	"minecraft-server/world"
)

// chunkCache is an instance's baked world: one shared Chunk Data frame per
// streamed column (conn.SharedFrame — framed and compressed once, queued to
// every joiner). A join used to bake and compress the whole map again (on
// the DOTA map ~50 ms and ~45 MB of garbage per player; a 16-player round
// start ~0.8 s). Over a world.ColumnTracker only the columns changed since
// the last bake are rebuilt; any other world is baked afresh every time.
type chunkCache struct {
	mu       sync.Mutex
	built    bool
	version  uint64                       // the world's Version when last baked
	columns  map[colKey]*conn.SharedFrame // every column of the streamed rectangle
	occupied map[colKey]bool              // columns that hold blocks
	order    []*conn.SharedFrame          // the rectangle, x-major, as streamed
}

type colKey struct{ cx, cz int32 }

// frames returns the Chunk Data packets for w in streaming order, baking
// what changed. The frames are immutable once returned.
func (cc *chunkCache) frames(w world.World) []*conn.SharedFrame {
	tracker, ok := w.(world.ColumnTracker)
	if !ok {
		var fresh chunkCache
		return fresh.rebake(w, nil, nil)
	}
	cc.mu.Lock()
	defer cc.mu.Unlock()
	v := tracker.Version()
	if cc.built && v == cc.version {
		return cc.order
	}
	var dirty map[world.Column]bool // nil: everything
	if cc.built {
		if cols, all := tracker.ChangedSince(cc.version); !all {
			dirty = make(map[world.Column]bool, len(cols))
			for _, c := range cols {
				dirty[c] = true
			}
		}
	}
	// Changes landing between Version and the bake are baked too and seen
	// again next time (their version is newer than v): rebuilt twice, never
	// missed.
	order := cc.rebake(w, tracker, dirty)
	cc.version, cc.built = v, true
	return order
}

// rebake rebuilds the dirty columns (all when dirty is nil) and the
// streaming order. tracker is nil for a world that can't say what changed.
func (cc *chunkCache) rebake(w world.World, tracker world.ColumnTracker, dirty map[world.Column]bool) []*conn.SharedFrame {
	if dirty == nil || cc.columns == nil {
		cc.columns = map[colKey]*conn.SharedFrame{}
		cc.occupied = map[colKey]bool{}
		dirty = nil
	}
	sections := map[colKey][][]int32{}
	collect := func(p world.Position, b world.Block) {
		if b.StateID == 0 { // air — sections default to air
			return
		}
		sec := floorDiv(p.Y-chunk.MinY, 16)
		if sec < 0 || sec >= chunk.SectionCount {
			return // outside the dimension's vertical range
		}
		k := colKey{int32(floorDiv(p.X, 16)), int32(floorDiv(p.Z, 16))}
		col := sections[k]
		if col == nil {
			col = make([][]int32, chunk.SectionCount)
			sections[k] = col
		}
		if col[sec] == nil {
			col[sec] = make([]int32, 4096)
		}
		lx := p.X - int(k.cx)*16
		lz := p.Z - int(k.cz)*16
		ly := (p.Y - chunk.MinY) - sec*16
		col[sec][ly*256+lz*16+lx] = b.StateID
	}
	if tracker != nil {
		tracker.RangeColumns(dirty, collect)
	} else {
		w.Range(collect)
	}
	for c := range dirty { // changed columns: rebuilt below, maybe emptied
		k := colKey{c.X, c.Z}
		delete(cc.columns, k)
		delete(cc.occupied, k)
	}
	for k := range sections {
		cc.occupied[k] = true
	}

	// The occupied columns' bounding box (padded by one) unioned with the
	// spawn ring.
	minCx, maxCx, minCz, maxCz := -HubChunkRadius, HubChunkRadius-1, -HubChunkRadius, HubChunkRadius-1
	for k := range cc.occupied {
		minCx, maxCx = min(minCx, k.cx-1), max(maxCx, k.cx+1)
		minCz, maxCz = min(minCz, k.cz-1), max(maxCz, k.cz+1)
	}

	// Block entities (beds, chests, banners, …) by column, so each chunk
	// packet lists the ones it contains — without them the client's
	// BlockEntityRenderer draws nothing and those blocks look missing.
	beByCol := map[colKey][]blockEntityWire{}
	if bep, ok := w.(world.BlockEntityProvider); ok {
		for pos, typeName := range bep.BlockEntities() {
			id, ok := world.BlockEntityTypeID(typeName)
			if !ok {
				continue
			}
			k := colKey{int32(floorDiv(pos.X, 16)), int32(floorDiv(pos.Z, 16))}
			beByCol[k] = append(beByCol[k], blockEntityWire{
				packedXZ: byte((pos.X&15)<<4 | (pos.Z & 15)),
				y:        int16(pos.Y),
				typeID:   id,
			})
		}
	}

	// Biome painted across the whole column, taken from the instance world
	// (set from the schematic), falling back to plains.
	biomeID := registry.BiomeIDOrDefault("")
	if bp, ok := w.(world.BiomeProvider); ok {
		biomeID = registry.BiomeIDOrDefault(bp.Biome())
	}

	var empty []byte
	inRect := make(map[colKey]bool, int(maxCx-minCx+1)*int(maxCz-minCz+1))
	order := make([]*conn.SharedFrame, 0, len(inRect))
	for cx := minCx; cx <= maxCx; cx++ {
		for cz := minCz; cz <= maxCz; cz++ {
			k := colKey{cx, cz}
			inRect[k] = true
			f := cc.columns[k]
			if f == nil {
				var data []byte
				if col := sections[k]; col != nil {
					data = chunk.BuildChunkData(col, biomeID)
				} else {
					if empty == nil {
						empty = chunk.BuildEmptyChunkData(biomeID)
					}
					data = empty
				}
				bes := beByCol[k]
				f = &conn.SharedFrame{ID: packetid.CbPlayChunkData, Build: func() []byte {
					return chunkColumnPayload(cx, cz, data, bes)
				}}
				cc.columns[k] = f
			}
			order = append(order, f)
		}
	}
	for k := range cc.columns { // the rectangle shrank
		if !inRect[k] {
			delete(cc.columns, k)
		}
	}
	cc.order = order
	return order
}
