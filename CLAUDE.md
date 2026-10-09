# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

A from-scratch Minecraft Java Edition server in Go, targeting protocol **763 (1.20.1)**. The author's stated goal is to avoid third-party libraries — prefer writing protocol/NBT/encryption code directly over pulling in `go-mc` or similar. If you need to add a dependency, ask first.

Module name: `minecraft-server` (Go 1.24).

## Build, run, test

```sh
go build ./...           # build everything
go run .                 # build+run, listens on :25565
go test ./...            # tests live in nbt/ and server/ (registry codec)
go vet ./...             # static checks
```

There's no CI, no linter config, no external deps (`go.sum` is empty).

Online-mode auth is toggled via `cfg.OnlineMode` (a `var`) in `cfg/cfg.go`. Online mode hits `sessionserver.mojang.com` (see `mojang/mojang.go`).

## Package layout

```
protocol/      Wire format: VarInt, fixed-width numerics, strings,
               byte arrays, packet framing (with optional zlib
               compression), UUID helpers.
nbt/           NBT tag types + Marshal (typed Compound/List values) +
               FromJSONBytes (with explicit TypeHints for Byte/Float/
               Double/Long disambiguation).
world/         Block (StateID + namespaced name) and World interface
               with a sparse hash-map MemoryWorld implementation. Also a
               generic Entity layer (item frames today: type + pos +
               FrameData{facing,rotation,item,glow}) carried on
               Template/MemoryWorld, plus item-registry IDs (item_ids.go).
player/        Player gameplay entity (EntityID, Name, UUID, position,
               gamemode). Pure data type; no wire knowledge.
chunk/         Chunk-level data builders (empty chunk sections,
               heightmaps NBT).
encryption/    AES-128 CFB8 cipher + net.Conn wrapper.
mojang/        sessionserver.mojang.com client (hasJoined endpoint).
ban/           Ban list loader (reads banlist.json with reload support).
cfg/           Runtime config vars (ServerId, OnlineMode).
templates/     Schematic-template locations + canonical names. The single
               source of truth for the templates Root, the name constants
               (Spawn, BedwarsDotaMap, *Lobby), and OS-independent path
               helpers (SchemFile/ConfigFile/Name) — names are forward-slash,
               disk paths use the OS separator (fixes Windows lookups).
db/            PostgreSQL connection layer (pgx/v5 pool; config from env,
               ping, graceful close).
redisc/        Redis connection layer (go-redis/v9; config from env, ping,
               graceful close).
store/         Persistence entities + repositories over db's pgx pool:
               players, bans, mutes, per-mode match history +
               participation (bedwars/skywars/ffa), bedwars event log,
               per-mode ELO ratings (rank computed at read time), and
               cross-mode stats. Schema via golang-migrate (embedded SQL in
               store/migrations/, applied at startup by store.Migrate).
server/        Server struct (world + entity-ID counter), per-connection
               state machine, handlers per state, packet IDs, play-state
               senders, registry codec loader.
game/          Plugin API: Definition/Logic (one mini-game in one instance)
               plus the server-wide half in plugin.go — RegisterListener
               (a Logic attached to every instance), RegisterCommand,
               BlockInteraction. Never imports server/.
plugins/       Server-wide plugins (blank-imported from main.go like games).
               worldedit/ is the reference: wand selection, //set //replace
               //undo //copy //paste, //schem save.
```

Postgres + Redis backends are deployed via `docker-compose.yml`
(`docker compose up -d`) and connected at startup (`connectStores` in
`main.go`). **Postgres is a hard dependency** — accounts and auth password
hashes live there; a failed connect or migration aborts startup. Redis is
best-effort: a down Redis logs a warning and the server boots without it.
Handles live on `Server.DB` / `Server.Redis`.

## Architecture

### Connection lifecycle

`main.go` constructs a `*server.Server` (holds the world + entity-ID counter) and spawns `srv.HandleConn` per accepted TCP connection. Each connection has its own `ClientConnection` (in `server/server.go`) referencing the Server, holding the wire conn, state, write mutex, and a `done` channel. After login completes, the connection gains a `*player.Player` (nil before then; only valid in StatePlay).

State machine: `StateHandshake` → `StateStatus` *or* `StateLogin` → `StatePlay`. Defined as a typed enum in `server/state.go`. Dispatch in `processPacket` routes to one of:
- `handler_handshake.go` (`handshake.go`)
- `handler_status.go`
- `handler_login.go`
- `handler_play.go`

Two goroutines per client:
1. **readLoop** (`server.go`) — reads packets with a 30s deadline, dispatches.
2. **keepAlive** (`keep_alive.go`) — every 20s in play state, sends a clientbound KeepAlive.

Both write through `safeWrite`, which is mutex-guarded (`c.writeMu`). The same mutex protects the `c.conn` swap that happens when encryption turns on, since CFB8 stream cipher state would desync under concurrent writes. Cleanup is idempotent (`atomic.CompareAndSwapInt32` on `c.closed`).

### Packet framing

Wire format: `VarInt(length) + VarInt(packetID) + payload`. Helpers in `protocol/`:
- `ReadPacket(conn)` returns the post-length `*bytes.Buffer`.
- `ReadPacketSplit(conn)` returns ID + remaining bytes (used in `handleLogin` for the Encryption Response, which is read directly without going through readLoop).
- `WritePacket(conn, id, payload)` frames and writes; `DebugPackets` gates per-packet logging.
- Three VarInt variants exist because reads happen from `io.Reader` (connection), `*bytes.Buffer` (parsed body), and `[]byte` (raw slice after split).

**Compression is implemented.** `enableCompression` (handler_login.go) sends Set Compression with a 256-byte threshold during login; framing helpers take a `compressionThreshold` parameter (`protocol.CompressionDisabled` before that). Read-side frames are capped at `protocol.MaxPacketLength` (2 MiB) and declared uncompressed sizes at `MaxUncompressedLength` (8 MiB).

### Encryption (online mode)

Flow in `handler_login.go`:
1. `sendEncryptionRequest` — per-connection 4-byte verify token + global RSA public key.
2. `recvAndVerifyEncryptionResponse` — reads response via `ReadPacketSplit`, RSA-decrypts shared secret + verify token, compares token bytes.
3. `mojang.VerifyWithMojang` — POSTs to sessionserver. Mojang requires the "negative bigint hex" hash format (see `mojang/mojang.go`).
4. `enableEncryption` — wraps `c.conn` with `encryption.WrapEncryptedConn` (AES-128 CFB8). Shared secret doubles as AES key and IV. The swap holds `c.writeMu`.
5. `LoginSuccess` sent over the encrypted connection. Properties array count (VarInt 0) is required for both online and offline.

The RSA keypair is process-global, generated in `server.init()` via `NewEncryptionRequest`. Errors panic immediately (not silently dropped).

CFB8 in `encryption/cfb8.go` is a hand-optimized ring-buffer variant. The wrapping `Write` in `encryption/encrypt_connection.go` chunks at 4 KiB and guarantees full writes — **do not collapse to a single `XORKeyStream + Write`**; a partial write desyncs the cipher permanently.

### Games, arenas, matchmaker

Games register a `game.Definition` (ID, Min/Max players, `Template`, `New` Logic factory) in `init()`. `/play <game>` queues via `Matchmaker`; when MinPlayers is reached, `Server.StartGame` clones the Definition's template into a fresh Instance and attaches the Logic.

**Named arenas** are created at runtime: `/arena create <game> <template> [name]` (op). Each game kind that supports arenas registers a `game.ArenaBuilder` (`game/arena.go`); bedwars' is in `games/bedwars/arena_config.go`. The builder takes the loaded map template + the bytes of a sibling JSON config (`<TemplateDir>/<template>.json`, default `schem/templates/...`) and returns a Definition. `CreateArena` then **spins up a running Instance immediately** (id = arena name, auto-named `bw-<n>` for bedwars) via `NewInstance` + `AttachLogic` — one shared world per arena. `Server.arenas` tracks name→kind (cleared in `RemoveInstance`). `/play <game> <arena>` (and `/instance join <arena>`) `MovePlayer`s the caller into that instance from their own readLoop, so everyone shares one world and is mutually visible. Bedwars' win-check is armed only after ≥2 teams have had members (`engaged`), so a freshly-created empty arena doesn't end itself. (Matchmaker is still used for `/play <game>` with no arena — its `startGame` moves players **sequentially**, since overlapping moves let one player's Respawn wipe another's already-broadcast Spawn.) The arena JSON defines the layout in world coordinates — the map supplies only blocks. Each team carries its own spawn, bed blocks, and shop villager NPCs (`teams[].villagers`); generators are a top-level list (per-team or neutral). See `schem/templates/bedwars/badwars_dota_map.json`; regenerate a starter from auto-detect with `GENCFG=1 go test ./games/bedwars -run TestGenSampleConfig`. The BedWars lobby's ender-pearl menu is a live DOTA arena browser (`openBedwarsArenaMenu`/`bedwarsArenaEntries` in `hub_menu.go`): the first slots are one "+ New …" button per mode in `bedwarsModes` (4×4 team game, 1×1 duel; key `create:<kind>` → CreateArena from `templates.BedwarsDotaMap` + join), then one compass per running bedwars arena of either kind that has players, its stack size = the online count.

**BedWars modes.** Bedwars registers two arena kinds (`KindFull`=`bedwars`, `KindDuel`=`bedwars-1x1`, in `games/bedwars/arena_config.go`) over the *same* map + JSON config. The duel builder (`buildBedwarsDuelArenaDef` → `duelConfig`/`selectTeams`) keeps two bases (config indices 0 and n/2 — opposite islands on a ring-ordered 4-team config — or both of a 2-team config), remaps generator team indices, drops the other bases' generators/villagers, and leaves their beds as unowned map blocks; capacity is 2 teams × 1 player. Auto-names are `bw-<n>` / `bw-1x1-<n>`. The JSON config also accepts optional top-level `teamSize` (default 4) and `minPlayers` (default 2) for the full kind. Matchmaker presets live in `defaultModes` (`mode.go`): `bedwars` (4×4 on the DOTA map) and `bedwars-1x1` (2×1 on the generated two-island arena, since the shipped map has 4 beds and `buildSchemArena` requires an exact bed match).

### Plugins (server-wide)

`game/plugin.go` is the second half of the plugin API. A mini-game's `Logic` is bound to one instance; a **listener** (`game.RegisterListener(name, logic)`, from `init()`) is a `Logic` the server attaches to *every* instance — hub, lobbies, arenas — with that instance's `Ctx`. Each `Instance` snapshots `game.Listeners()` in `NewInstance` (so register before `server.New()`); the core never calls the `Instance.On*` hook fields directly any more but goes through `fireJoin`/`allowBlockBreak`/`filterChat`/… in `server/plugins.go`, which run listeners first (registration order) and the instance's own hook last. Veto hooks (`OnBlockBreak/Place/Interact`, `OnPlayerAttack`) short-circuit on the first `false`; `OnChat` composes rewrites. A panicking listener is logged and counts as "allow".

- **`OnBlockInteract(ctx, p, game.BlockInteraction) bool`** is a new `Logic` hook (NoopLogic allows): every left click (`SbPlayPlayerAction` action 0) and right click (`SbPlayUseItemOnBlock`) on a block, with face + held item, *before* the core treats it as dig / placement / chest open. `false` consumes the click and rolls back the client's prediction; for a consumed survival dig the connection remembers the position (`consumedDig`) so the matching "finished digging" is swallowed too.
- **Plugin commands**: `game.RegisterCommand(&game.Command{Name, Aliases, NeedsOp, Help, Run(ctx, player, args), Complete})`. `lookupCommand` (plugins.go) resolves built-ins first, then plugins (shadowing is logged at boot); plugin commands appear in `/help`, the brigadier tree and tab-complete (`Complete` candidates are prefix-filtered by the server). A `Name` starting with `/` gives a WorldEdit-style double-slash command: the vanilla client sends `//set x` as `/set x`, so `handler_play.go` no longer strips the leading slash and `RunCommand` retries without it for clients that do include one.
- **Bulk blocks**: `game.Instance.SetBlocks([]world.BlockChange)` → `Instance.SetBlocks` (`server/block_bulk.go`) applies to the world and broadcasts one Update Section Blocks (Cb 0x43, VarLong `state<<12 | x<<8|z<<4|y` per entry) per 16³ section, instead of a Block Update per block.
- **`PlayerHandle.IsOp()`** for permission checks inside hooks (e.g. the wand).
- **Schematic writing**: `schem.FromWorld(world, cornerA, cornerB)` + `(*Schematic).Marshal()`/`schem.SaveFile` emit Sponge v2 gzip NBT; block states round-trip via `world.PaletteName`/`world.StateProperties` (inverse of `ResolveStateID`).

`plugins/worldedit` exercises all of it and is the template for new plugins: one package, `init()` registers a listener + commands, blank import in `main.go`.

### Packet IDs

`server/packet_ids.go` is split into state-scoped const blocks (`Sb*` serverbound, `Cb*` clientbound, with `Handshake`/`Status`/`Login`/`Play` prefixes). Many IDs collide at `0x00` across states; that's correct because dispatch is state-keyed. Source of truth: wiki.vg for protocol 763.

### Play-state join sequence

After LoginSuccess + state transition to play, `sendPlayPackets` (in `server.go`) fires in order:
1. `sendLoginPlay` (Cb 0x28) — includes the registry codec (NBT). Codec is built once via `RegistryCodec()` in `registry.go`, which embeds `registry-codec.json` and converts to NBT through `nbt.FromJSONBytes` with `registryHints`. The hints map encodes per-key NBT type overrides for the 1.20.1 codec; **if the client kicks complaining about a type mismatch, add the offending key to the relevant hint set.**
2. `sendWorldChunks` (Cb 0x24 per column) — bakes the instance world into real chunk data. Every non-air block is bucketed by (chunkX, chunkZ) column + 16-tall section and packed into paletted sections by `chunk.BuildChunkData` (single-valued / indirect 4–8-bit palette / direct 15-bit, 1.16+ non-spanning long packing). Streams the occupied-column bounding box (plus a one-chunk pad) unioned with the spawn ring; empty columns use `chunk.BuildEmptyChunkData`. Heightmaps are still empty (`chunk.BuildEmptyHeightmaps`). Biomes are a single value per column taken from the map: the schematic's dominant biome (`schem` parses `BiomePalette`/`BiomeData`, v2 flat or v3 nested) flows template→world→`BuildChunkData(sections, biomeID)`; `server.BiomeID` resolves the name to its codec registry index (default `minecraft:plains`=39, since biome 0 is badlands). Block entities ARE sent (beds/chests/banners/skulls/… from the world's `BlockEntityProvider`, with empty NBT) so BlockEntityRenderer blocks aren't invisible. Light is full daylight (`writeFullDaylight`). Per-block Block Updates are no longer used for initial world state. Item-frame and other world entities go out separately via `sendWorldEntities` (Spawn Entity 0x01 + metadata).
3. `sendSyncPlayerPosition` (Cb 0x3C) — X/Y/Z + yaw/pitch + flags + teleport ID. Client echoes teleport ID via `SbPlayTeleportConfirm`.

## Gotchas

- **`handleLogin` reads from `c.conn` directly** for the Encryption Response while `readLoop` is parked in `processPacket`. Works only because both run on the same goroutine — don't introduce concurrent reads.
- **`ban` package is file-backed**: `ban.Load("banlist.json")` runs at startup; `/ban` adds in-memory entries (persist via `ban.Save`). Expired entries are filtered at lookup.
- **`OfflineUUID`** (`protocol/uuid.go`) uses vanilla `MD5("OfflinePlayer:" + name)` with v3 UUID bits — match this exactly if reproducing behavior.
- **Registry codec type hints (`server/registry.go`) are best-effort.** If the client disconnects right after LoginSuccess complaining about a wrong type, add the key to ByteKeys/FloatKeys/DoubleKeys/LongKeys.
- **Light data is a full level-15 sky-light for every section** (`writeFullDaylight` in `play_send.go`): all 26 light sections (24 + 2 padding) get a 2048-byte 0xFF sky array, block light zero. This forces permanent daylight with no dark chunks. The server never sends Update Time, so the client stays at its default day time and never cycles to night — that's what keeps the bright sky-light rendering as day. If you ever add a day/night cycle, sky light alone will darken at night.
- **Chunk baking holds the whole occupied region in memory per join.** `sendWorldChunks` ranges the (sparse) instance world once and allocates a `[4096]int32` per occupied section. Fine for current map sizes; a streaming/per-chunk-on-demand approach is the future step if maps get much larger or view distance grows.
- **Throwable items** (`projectile.go`): using egg / snowball / ender_pearl (right-click in air, `SbPlayUseItem`) spawns a projectile entity simulated on the instance tick loop (gravity, drag, crude non-air-block collision). On impact an ender pearl teleports its thrower to the landing spot; egg/snowball just despawn. Dispatch keys off the creative-tracked held item (`heldItemName`), so server-given menu items (e.g. the lobby arena-selector pearl) aren't thrown. No item consumption.
- **Dropped items are real entities** (`server/item_entity.go`): `Instance.DropItem` (exposed to games via `game.Instance.DropItem` / `DroppedItemsNear`) spawns a `minecraft:item` entity (type 54) that hops, falls onto the block below, merges with a same-item drop within one block (up to a 64 stack), is collected by the first living non-spectator player whose vanilla-sized pickup box contains it (`giveItem` returns the overflow, which stays on the ground), and despawns after 5 minutes. Pickup broadcasts `CbPlayPickupItem` (0x67) for the fly-in animation. They're re-streamed on join/respawn through `sendWorldEntities`. BedWars generators use this by default (`dropGranter`): the resource appears at the generator's block from the arena JSON, and the pile is capped per generator (`maxStack` in JSON, default iron 48 / gold 16 / diamond 8 / emerald 4). `inventoryGranter` (straight to inventory) is still available via `WithGranter`. **Floating text** (`server/hologram.go`, `game.Instance.SpawnHologram` → `game.Hologram{SetText,Remove}`) is an invisible small marker armor stand (type 2) with an always-visible custom name (§-codes work), no gravity; re-streamed on join/respawn like items. BedWars puts a two-line countdown (`games/bedwars/hologram.go`: resource title + "Spawns in Ns" / "Full") 3 blocks over every diamond/emerald generator, refreshed from `OnTick`.
- **Chests have a stored inventory** (`chest.go`): right-clicking a chest block opens a 9×3 GUI streamed from `Instance.chests[pos]` (27 `itemStack`s, no item NBT modelled). Click handling **trusts the client's "changed slots" array** in Click Container and persists chest-range slots (0..26) — fine for a creative server, no anti-cheat. `nbt.SkipTag` lets the slot reader skip item NBT it doesn't store. The chest GUI reuses the single menu window (`openMenu{kind:"chest", chestPos}`).
- **`sendRespawn` sends TWO Respawn packets (the_end → overworld).** A same-dimension Respawn leaves ghost entities/blocks on the client; the vanilla client only does a full unload+reload when the dimension actually changes, so we hop through `the_end` first. `resyncView` (`resync.go`) is the reusable "clear everything the client sees + re-stream" primitive (Respawn → chunks → world entities → tab list + other players + own HP/attributes); `MovePlayer` (cross-instance) and combat `respawn` (in-place) both go through this so transitions don't leave ghosts. Anything depending on a single Respawn packet (e.g. test packet-sequence assertions) must account for the pair.

## Conventions

- Comments and log messages are a mix of Russian and English — match the surrounding file's language.
- Errors wrap with `%w` and use a noun-phrase prefix (`"reading packet ID: %w"`).
- File names are `snake_case.go`. Handlers are `handler_<state>.go`. Per-direction play helpers are `play_read.go` / `play_send.go`.
- No structured logging — everything is `fmt.Printf`. Per-packet write logging is gated behind `protocol.DebugPackets = true`.
