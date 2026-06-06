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
               with a sparse hash-map MemoryWorld implementation.
player/        Player gameplay entity (EntityID, Name, UUID, position,
               gamemode). Pure data type; no wire knowledge.
chunk/         Chunk-level data builders (empty chunk sections,
               heightmaps NBT).
encryption/    AES-128 CFB8 cipher + net.Conn wrapper.
mojang/        sessionserver.mojang.com client (hasJoined endpoint).
ban/           Ban list loader (reads banlist.json with reload support).
cfg/           Runtime config vars (ServerId, OnlineMode).
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
```

Optional Postgres + Redis backends are deployed via `docker-compose.yml`
(`docker compose up -d`) and connected at startup (`connectStores` in
`main.go`). Both are best-effort: a down backend logs a warning and the
server boots without it. Handles live on `Server.DB` / `Server.Redis`
(nil-checked; nothing in the core depends on them yet).

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

**Compression is not implemented.** The server never sends Set Compression (0x03), so packets stay uncompressed.

### Encryption (online mode)

Flow in `handler_login.go`:
1. `sendEncryptionRequest` — per-connection 4-byte verify token + global RSA public key.
2. `recvAndVerifyEncryptionResponse` — reads response via `ReadPacketSplit`, RSA-decrypts shared secret + verify token, compares token bytes.
3. `mojang.VerifyWithMojang` — POSTs to sessionserver. Mojang requires the "negative bigint hex" hash format (see `mojang/mojang.go`).
4. `enableEncryption` — wraps `c.conn` with `encryption.WrapEncryptedConn` (AES-128 CFB8). Shared secret doubles as AES key and IV. The swap holds `c.writeMu`.
5. `LoginSuccess` sent over the encrypted connection. Properties array count (VarInt 0) is required for both online and offline.

The RSA keypair is process-global, generated in `server.init()` via `NewEncryptionRequest`. Errors panic immediately (not silently dropped).

CFB8 in `encryption/cfb8.go` is a hand-optimized ring-buffer variant. The wrapping `Write` in `encryption/encrypt_connection.go` chunks at 4 KiB and guarantees full writes — **do not collapse to a single `XORKeyStream + Write`**; a partial write desyncs the cipher permanently.

### Packet IDs

`server/packet_ids.go` is split into state-scoped const blocks (`Sb*` serverbound, `Cb*` clientbound, with `Handshake`/`Status`/`Login`/`Play` prefixes). Many IDs collide at `0x00` across states; that's correct because dispatch is state-keyed. Source of truth: wiki.vg for protocol 763.

### Play-state join sequence

After LoginSuccess + state transition to play, `sendPlayPackets` (in `server.go`) fires in order:
1. `sendLoginPlay` (Cb 0x28) — includes the registry codec (NBT). Codec is built once via `RegistryCodec()` in `registry.go`, which embeds `registry-codec.json` and converts to NBT through `nbt.FromJSONBytes` with `registryHints`. The hints map encodes per-key NBT type overrides for the 1.20.1 codec; **if the client kicks complaining about a type mismatch, add the offending key to the relevant hint set.**
2. `sendWorldChunks` (Cb 0x24 per column) — bakes the instance world into real chunk data. Every non-air block is bucketed by (chunkX, chunkZ) column + 16-tall section and packed into paletted sections by `chunk.BuildChunkData` (single-valued / indirect 4–8-bit palette / direct 15-bit, 1.16+ non-spanning long packing). Streams the occupied-column bounding box (plus a one-chunk pad) unioned with the spawn ring; empty columns use `chunk.BuildEmptyChunkData`. Heightmaps are still empty (`chunk.BuildEmptyHeightmaps`), block entities 0, light masks empty. Per-block Block Updates are no longer used for initial world state.
3. `sendSyncPlayerPosition` (Cb 0x3C) — X/Y/Z + yaw/pitch + flags + teleport ID. Client echoes teleport ID via `SbPlayTeleportConfirm`.

## Gotchas

- **`handleLogin` reads from `c.conn` directly** for the Encryption Response while `readLoop` is parked in `processPacket`. Works only because both run on the same goroutine — don't introduce concurrent reads.
- **`ban.IsBanned` is hardcoded** (returns a stub for `"BannedPerson"`); `banlist.json` at the repo root is not read.
- **`OfflineUUID`** (`protocol/uuid.go`) uses vanilla `MD5("OfflinePlayer:" + name)` with v3 UUID bits — match this exactly if reproducing behavior.
- **Registry codec type hints (`server/registry.go`) are best-effort.** If the client disconnects right after LoginSuccess complaining about a wrong type, add the key to ByteKeys/FloatKeys/DoubleKeys/LongKeys.
- **Light data is a full level-15 sky-light for every section** (`writeFullDaylight` in `play_send.go`): all 26 light sections (24 + 2 padding) get a 2048-byte 0xFF sky array, block light zero. This forces permanent daylight with no dark chunks. The server never sends Update Time, so the client stays at its default day time and never cycles to night — that's what keeps the bright sky-light rendering as day. If you ever add a day/night cycle, sky light alone will darken at night.
- **Chunk baking holds the whole occupied region in memory per join.** `sendWorldChunks` ranges the (sparse) instance world once and allocates a `[4096]int32` per occupied section. Fine for current map sizes; a streaming/per-chunk-on-demand approach is the future step if maps get much larger or view distance grows.

## Conventions

- Comments and log messages are a mix of Russian and English — match the surrounding file's language.
- Errors wrap with `%w` and use a noun-phrase prefix (`"reading packet ID: %w"`).
- File names are `snake_case.go`. Handlers are `handler_<state>.go`. Per-direction play helpers are `play_read.go` / `play_send.go`.
- No structured logging — everything is `fmt.Printf`. Per-packet write logging is gated behind `protocol.DebugPackets = true`.
