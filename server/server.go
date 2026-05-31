package server

import (
	"bytes"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"minecraft-server/cfg"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var (
	private   *rsa.PrivateKey
	publicKey []byte
)

func init() {
	pub, priv, err := NewEncryptionRequest()
	if err != nil {
		panic(fmt.Errorf("init encryption: %w", err))
	}
	publicKey = pub
	private = priv
}

// outboundQueueSize bounds the per-connection pending-frame channel. Set
// high enough that bursty broadcasts (e.g. 100 players × 20Hz movement) fit
// without dropping, low enough that a stuck client is detected quickly.
const outboundQueueSize = 256

// outboundMsg is what the writer goroutine reads from the channel. A frame
// is bytes ready for conn.Write. A swap message means "flush whatever is
// buffered, then start writing to this new conn" — used for the encryption
// handshake, where the conn under us changes mid-stream.
type outboundMsg struct {
	frame    []byte
	swapConn net.Conn
}

// Server holds process-wide shared state across all instances: the op
// allocator, the op set, the entity-ID counter, the hub instance, and the
// template registry. World and PlayerList live on Instance.
type Server struct {
	Hub *Instance
	// Auth is the holding-pen instance where unauthenticated players
	// land in offline mode. Nil when the auth plugin is disabled — see
	// EnableAuth. Players move to Hub via MovePlayer once /register or
	// /login succeeds.
	Auth       *Instance
	Ops        *OpSet
	Mutes      *MuteSet
	Matchmaker *Matchmaker
	// ChatModerator, when non-nil, gets a chance to inspect every chat
	// line before it broadcasts. See chat_moderation.go for the contract.
	ChatModerator      ChatModerator
	nextEntityID       atomic.Int32
	instanceSerialNext atomic.Uint64

	// instances is the registry of all live instances (including Hub).
	// templates is the registry of read-only world snapshots that
	// /instance create can clone from.
	mu        sync.RWMutex
	instances map[string]*Instance
	templates map[string]*world.Template
}

// nextInstanceSerial allocates a unique monotonic uint64 used by
// StartGame to suffix the per-round instance ID.
func (s *Server) nextInstanceSerial() uint64 {
	return s.instanceSerialNext.Add(1)
}

// New constructs a Server with an empty Hub instance and an op set seeded
// from cfg.InitialOps.
func New() *Server {
	s := &Server{
		Ops:       NewOpSet(cfg.InitialOps),
		Mutes:     NewMuteSet(),
		instances: make(map[string]*Instance),
		templates: make(map[string]*world.Template),
	}
	s.Hub = NewInstance("hub", s, world.NewMemoryWorld())
	s.instances[s.Hub.ID] = s.Hub
	s.Matchmaker = NewMatchmaker(s)
	return s
}

// RegisterTemplate stores a world template under name so /instance create
// (and game logic later) can clone it. Overwrites any existing entry with
// the same name.
func (s *Server) RegisterTemplate(name string, t *world.Template) {
	s.mu.Lock()
	s.templates[name] = t
	s.mu.Unlock()
}

// GetTemplate returns the template with this name, or nil if none.
func (s *Server) GetTemplate(name string) *world.Template {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.templates[name]
}

// TemplateNames returns every registered template's name. Used by
// /instance create tab completion.
func (s *Server) TemplateNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.templates))
	for name := range s.templates {
		out = append(out, name)
	}
	return out
}

// FindPlayer looks up a logged-in player by name across every instance.
// Returns the connection and the instance it currently lives in.
func (s *Server) FindPlayer(name string) (*ClientConnection, *Instance, bool) {
	s.mu.RLock()
	instances := make([]*Instance, 0, len(s.instances))
	for _, i := range s.instances {
		instances = append(instances, i)
	}
	s.mu.RUnlock()
	for _, i := range instances {
		if c, ok := i.Players.ByName(name); ok {
			return c, i, true
		}
	}
	return nil, nil, false
}

// PlayerCount sums Players across every instance. Cheap enough for /list
// and tests; not a hot path.
func (s *Server) PlayerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, i := range s.instances {
		total += i.Players.Count()
	}
	return total
}

// PlayerNames returns every logged-in player's name across all instances.
// Used by command tab-completion.
func (s *Server) PlayerNames() []string {
	s.mu.RLock()
	instances := make([]*Instance, 0, len(s.instances))
	for _, i := range s.instances {
		instances = append(instances, i)
	}
	s.mu.RUnlock()
	var names []string
	for _, i := range instances {
		i.Players.Range(func(c *ClientConnection) {
			names = append(names, c.player.Name)
		})
	}
	return names
}

// InstanceIDs returns every registered instance's ID. Used by /instance
// tab-completion.
func (s *Server) InstanceIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.instances))
	for id := range s.instances {
		out = append(out, id)
	}
	return out
}

// AddInstance registers an instance with the server so commands and
// MovePlayer can find it by ID. Caller still owns the instance's
// lifecycle (Stop, world cleanup, etc.).
func (s *Server) AddInstance(i *Instance) {
	s.mu.Lock()
	s.instances[i.ID] = i
	s.mu.Unlock()
}

// GetInstance returns the instance with this ID, or nil if none is
// registered.
func (s *Server) GetInstance(id string) *Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.instances[id]
}

// RemoveInstance drops an instance from the registry and stops its tick
// loop. Hub cannot be removed. Caller must ensure no players are in the
// instance (or move them out first) — RemoveInstance does NOT evacuate.
func (s *Server) RemoveInstance(id string) error {
	if s.Hub != nil && id == s.Hub.ID {
		return fmt.Errorf("cannot remove the hub instance")
	}
	s.mu.Lock()
	inst, ok := s.instances[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("no such instance: %s", id)
	}
	if inst.Players.Count() > 0 {
		s.mu.Unlock()
		return fmt.Errorf("instance %s still has %d players", id, inst.Players.Count())
	}
	delete(s.instances, id)
	s.mu.Unlock()

	inst.Stop()
	return nil
}

// MovePlayer transfers a player from their current instance to target,
// spawning them at (x, y, z) in the new world.
//
// On the wire the client sees:
//  1. PlayerInfoRemove for every UUID in the old instance — clears the
//     stale tab list (Respawn doesn't touch it).
//  2. Respawn — client throws away its world and despawns every entity.
//  3. ChunkData for the new world.
//  4. SynchronizePlayerPosition at the spawn.
//  5. Block Update for every non-Air block in the new world (Replay).
//  6. PlayerInfoUpdate + SpawnPlayer for each player already in target.
//
// In parallel, the old instance broadcasts RemoveEntities + PI Remove for
// the leaver to the players staying behind, and the new instance
// broadcasts PI Add + SpawnPlayer for the newcomer.
//
// Must be called from the player's own readLoop goroutine (e.g. from a
// command handler or an event hook). There is currently no synchronization
// on c.instance, and a concurrent call from another goroutine would race
// with handler_play's reads.
func (s *Server) MovePlayer(c *ClientConnection, target *Instance, x, y, z float64) error {
	if c.player == nil {
		return fmt.Errorf("player not logged in")
	}
	if target == nil {
		return fmt.Errorf("target instance is nil")
	}
	old := c.instance
	if old == target {
		return nil
	}

	// Capture UUIDs we'll need to clear from the client's tab list AFTER
	// LeaveAndAnnounce removes c — so include c itself.
	oldUUIDs := old.Players.UUIDs()

	// 1. Tell old instance the player is gone (broadcasts to others +
	//    removes c from old.Players).
	old.LeaveAndAnnounce(c)

	// 2. Wipe c's tab list. Respawn doesn't do this, and the next
	//    JoinAndAnnounce will rebuild it for the new instance.
	if len(oldUUIDs) > 0 {
		_ = c.safeWrite(CbPlayPlayerInfoRemove, playerInfoRemovePayload(oldUUIDs))
	}

	// 3. Respawn — client clears world data + every entity it knew.
	if err := c.sendRespawn(); err != nil {
		return fmt.Errorf("respawn: %w", err)
	}

	// 4. Switch the authoritative pointer before we stream the new world,
	//    so sendCurrentWorldState reads the right blocks.
	c.instance = target

	// 5. Reset the player's position to spawn.
	c.player.MoveTo(x, y, z, false)

	// 6. Stream the spawn-position triplet (compass + view center +
	//    start-waiting-for-chunks), THEN chunks → blocks → SyncPos.
	//    See sendPlayPackets for the rationale; same sequence applies
	//    on cross-instance teleport since Respawn just reset the
	//    client's world.
	if err := c.sendSetDefaultSpawnPosition(int(x), int(y), int(z), 0); err != nil {
		return fmt.Errorf("set spawn position: %w", err)
	}
	if err := c.sendSetCenterChunk(0, 0); err != nil {
		return fmt.Errorf("set center chunk: %w", err)
	}
	if err := c.sendStartWaitingForChunks(); err != nil {
		return fmt.Errorf("start waiting for chunks: %w", err)
	}
	if err := c.sendInitialChunks(); err != nil {
		return fmt.Errorf("chunk data: %w", err)
	}
	if err := c.sendCurrentWorldState(); err != nil {
		return fmt.Errorf("world state: %w", err)
	}
	if err := c.sendSyncPlayerPosition(x, y, z, 1); err != nil {
		return fmt.Errorf("sync pos: %w", err)
	}

	// 7. Register in target + broadcast tab list and Spawn for everyone.
	target.JoinAndAnnounce(c)
	return nil
}

// Name returns the player's chosen username (immutable for the session).
// Exposed so plugins / bots can read the name without reaching into
// unexported fields.
func (c *ClientConnection) Name() string { return c.playerName }

// connectionsFromIP returns every currently-connected ClientConnection
// whose remote address matches ip. Used by /banip to evict matching
// sessions on the spot. IP is matched via clientIP() so net.Pipe
// connections share the synthetic "pipe" key.
func (s *Server) connectionsFromIP(ip string) []*ClientConnection {
	s.mu.RLock()
	instances := make([]*Instance, 0, len(s.instances))
	for _, i := range s.instances {
		instances = append(instances, i)
	}
	s.mu.RUnlock()
	var out []*ClientConnection
	for _, i := range instances {
		i.Players.Range(func(c *ClientConnection) {
			if clientIP(c.conn.RemoteAddr()) == ip {
				out = append(out, c)
			}
		})
	}
	return out
}

// Instance returns the instance the player is currently in (hub, an
// arena, a lobby). Nil before login completes. Exposed for bots that
// need to broadcast into the same chat scope the player is talking in.
func (c *ClientConnection) Instance() *Instance { return c.instance }

// sendSystemMessage delivers a SystemChat line to a single player. Used by
// command responses and other server → one-player notifications.
func (c *ClientConnection) sendSystemMessage(text string) error {
	return c.safeWrite(CbPlaySystemChat, buildSystemChatPayload(text))
}

func (c *ClientConnection) SendChat(sender, text string) error {
    return c.sendSystemMessage("<" + sender + "> " + text)
}

// HandleConn drives a single client connection through its state machine.
// Call in a goroutine per accepted net.Conn.
func (s *Server) HandleConn(conn net.Conn) {
	client := &ClientConnection{
		server:               s,
		conn:                 conn,
		state:                StateHandshake,
		compressionThreshold: protocol.CompressionDisabled,
		outbound:             make(chan outboundMsg, outboundQueueSize),
		done:                 make(chan struct{}),
		writerDone:           make(chan struct{}),
	}
	// Default: auth plugin disabled → no gate. EnableAuth flips this to
	// false in handler_login so the player has to /register or /login.
	client.authed.Store(true)
	slog.Info("new connection", "addr", conn.RemoteAddr().String())
	defer client.cleanup()

	go client.writerLoop()
	go client.keepAlive()
	client.readLoop()
}

type ClientConnection struct {
	// sendMu is held briefly during (build frame + push to outbound) so
	// concurrent safeWrite calls can't interleave with each other or with
	// enableCompression's threshold flip, which would reorder frames on the
	// wire. The mutex is short-lived: no syscalls, just memory work.
	sendMu sync.Mutex

	server *Server

	// conn is the active socket. The read side is touched only by readLoop
	// (and by handler_login which runs in the readLoop goroutine, so swaps
	// during encryption-enable are safe). The write side is owned by
	// writerLoop, which maintains its own current-conn pointer updated via
	// outboundMsg.swapConn — writes never read this field directly.
	conn net.Conn

	state State

	// compressionThreshold is protocol.CompressionDisabled until the server
	// sends Set Compression. After that, both reads and writes use the
	// compressed framing. Guarded by sendMu when writers touch it.
	compressionThreshold int

	// outbound is the pending-write queue. Producers (safeWrite, broadcast)
	// push frames; writerLoop drains and writes them. Closed by cleanup.
	outbound chan outboundMsg

	// playerName is captured from Login Start so logs and ban-check have a
	// name to print before the full Player exists.
	playerName string

	// player is nil until login completes successfully. Reach for it only
	// after the connection has transitioned to StatePlay.
	player *player.Player

	// instance is the world/room this player currently lives in. Set when
	// login completes (defaults to Hub) and never changes until we add
	// cross-instance teleport. Single writer (handler_login), readers are
	// the broadcast paths and command handlers.
	instance *Instance

	closed int32 // atomic flag (use isClosed/cleanup)
	done   chan struct{}

	// keepAlivePendingID is the ID of the in-flight Cb KeepAlive the client
	// has not yet echoed, or 0 if none. keepAlivePendingSentNanos records
	// when it went on the wire (UnixNano). Both written by the keepAlive
	// goroutine and read+CAS'd by the SbPlayKeepAlive handler.
	keepAlivePendingID        atomic.Int64
	keepAlivePendingSentNanos atomic.Int64

	// menu holds whichever hub navigation menu (if any) is currently open
	// on this client (nil = no menu). Written from handler_play +
	// hub_menu (the player's readLoop goroutine); read from tests and
	// potentially from future cross-goroutine inspection — atomic so
	// `-race` stays clean and the test harness doesn't need locks.
	menu atomic.Pointer[openMenu]

	// authed gates the connection through the offline-mode auth plugin.
	// True = player has run /register or /login successfully (or auth
	// is disabled — see auth.go EnableAuth). False = chat + commands
	// other than the whitelist are blocked.
	authed atomic.Bool

	// heldSlot is the player's selected hotbar slot (0..8). Updated on
	// every Sb Set Held Item; default 0 matches the client's own
	// power-on default. Read by SbPlayUseItem to pick which menu item
	// the right-click should fire (blaze rod vs ender pearl, etc.).
	heldSlot atomic.Int32

	// writerDone is closed by writerLoop on exit. cleanup waits on it
	// (with a timeout) so any in-flight frames — particularly the Pong that
	// status handlers fire right before cleanup — actually make it on the
	// wire before conn.Close cuts the pipe.
	writerDone chan struct{}
}

func (c *ClientConnection) readLoop() {
	defer c.cleanup()

	for {
		if c.isClosed() {
			return
		}
		c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		packet, err := protocol.ReadPacket(c.conn, c.compressionThreshold)
		if err != nil {
			c.handleReadError(err)
			return
		}
		c.conn.SetReadDeadline(time.Time{})

		if err := c.processPacket(packet); err != nil {
			slog.Error("process packet failed",
				"player", c.playerName, "state", c.state.String(), "err", err)
			// Play-state errors aren't necessarily fatal (one malformed packet
			// shouldn't kick the player). For pre-play states, bail.
			if c.state != StatePlay {
				return
			}
		}
	}
}

// writerLoop owns the write side of the connection. It batches whatever is
// queued at the moment of a wake-up into one writev syscall via
// net.Buffers, then waits for the next message. This is the main reason
// for the queue: avoid one syscall per broadcast recipient.
func (c *ClientConnection) writerLoop() {
	defer close(c.writerDone)
	currentConn := c.conn
	var pending net.Buffers

	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		_ = currentConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := pending.WriteTo(currentConn)
		_ = currentConn.SetWriteDeadline(time.Time{})
		pending = pending[:0]
		return err
	}

	for {
		// Block waiting for the first message.
		msg, ok := <-c.outbound
		if !ok {
			_ = flush()
			return
		}
		if msg.swapConn != nil {
			if err := flush(); err != nil {
				c.handleWriteError(err)
				return
			}
			currentConn = msg.swapConn
		} else {
			pending = append(pending, msg.frame)
		}

		// Drain whatever else is queued without blocking. Bound the batch
		// so a single writev doesn't hold the cipher state forever and we
		// give the scheduler a chance to interleave.
	drain:
		for len(pending) < outboundQueueSize {
			select {
			case msg, ok := <-c.outbound:
				if !ok {
					_ = flush()
					return
				}
				if msg.swapConn != nil {
					if err := flush(); err != nil {
						c.handleWriteError(err)
						return
					}
					currentConn = msg.swapConn
				} else {
					pending = append(pending, msg.frame)
				}
			default:
				break drain
			}
		}

		if err := flush(); err != nil {
			c.handleWriteError(err)
			return
		}
	}
}

func (c *ClientConnection) handleWriteError(err error) {
	if c.isClosed() {
		return
	}
	slog.Error("client write failed", "player", c.playerName, "err", err)
	go c.cleanup()
}

func (c *ClientConnection) handleReadError(err error) {
	if c.isClosed() {
		return
	}
	// Normal disconnect: the client closed its socket → our next read
	// returns EOF (or unexpected EOF mid-frame). protocol.ReadPacket
	// wraps the underlying error with context like "packet length:
	// VarInt byte: %w", so plain `err == io.EOF` misses it — use
	// errors.Is which unwraps.
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		slog.Info("client disconnected", "player", c.playerName)
	case errors.Is(err, net.ErrClosed):
		// We closed the conn ourselves (cleanup, encryption swap, …) —
		// the leftover read returning ErrClosed isn't worth logging.
		return
	default:
		slog.Warn("client read failed", "player", c.playerName, "err", err)
	}
	c.cleanup()
}

func (c *ClientConnection) processPacket(packet *bytes.Buffer) error {
	packetID, err := protocol.ReadVarInt(packet)
	if err != nil {
		return fmt.Errorf("reading packet ID: %w", err)
	}

	switch c.state {
	case StateHandshake:
		return c.handleHandshake(packet, packetID)
	case StateStatus:
		return c.handleStatus(packet, packetID)
	case StateLogin:
		return c.handleLogin(packet, packetID)
	case StatePlay:
		return c.handlePlay(packet, packetID)
	default:
		return fmt.Errorf("unknown state: %s", c.state)
	}
}

// sendPlayPackets dispatches the post-LoginSuccess sequence that the client
// requires before it will render the world.
func (c *ClientConnection) sendPlayPackets() error {
	spawn := c.player.Snapshot()
	// Order matters here. Vanilla 1.20.1 holds the "loading terrain"
	// overlay until it has BOTH:
	//   - a known spawn position (Set Default Spawn Position, 0x50),
	//   - a known view-center chunk (Set Center Chunk, 0x4E), and
	//   - the explicit "start waiting for chunks" Game Event (id=13).
	// Without all three the client doesn't know the chunks we send are
	// the level data it should buffer, so it sits on the overlay forever
	// even after a perfectly valid SyncPos lands.
	//
	// Wire sequence:
	//   1. Login (Play)
	//   2. Set Default Spawn Position    ← compass + respawn target
	//   3. Set Center Chunk              ← "you live at (0,0)"
	//   4. Game Event 13                 ← "begin waiting for chunks"
	//   5. Chunk Data (16 empty chunks)
	//   6. World State (Block Updates for the spawn schematic)
	//   7. Synchronize Player Position   ← dismisses the overlay
	packets := []struct {
		name string
		f    func() error
	}{
		{"Login (Play)", c.sendLoginPlay},
		{"Set Default Spawn Position", func() error {
			return c.sendSetDefaultSpawnPosition(int(spawn.X), int(spawn.Y), int(spawn.Z), 0)
		}},
		{"Set Center Chunk", func() error {
			return c.sendSetCenterChunk(0, 0)
		}},
		{"Start Waiting For Chunks", c.sendStartWaitingForChunks},
		{"Chunk Data", c.sendInitialChunks},
		{"World State", c.sendCurrentWorldState},
		{"Sync Player Position", func() error {
			return c.sendSyncPlayerPosition(spawn.X, spawn.Y, spawn.Z, 1)
		}},
		// After SyncPos so loading-terrain dismisses first. Declare
		// Commands is a one-shot per-connection — Respawn (cross-instance
		// teleport) does not reset the client's command tree, so we don't
		// re-send it from MovePlayer.
		{"Declare Commands", c.sendDeclareCommands},
	}
	for _, pkt := range packets {
		if c.isClosed() {
			return fmt.Errorf("server closed during %s", pkt.name)
		}
		if err := pkt.f(); err != nil {
			if c.isClosed() {
				return fmt.Errorf("client disconnected during %s", pkt.name)
			}
			return fmt.Errorf("%s: %w", pkt.name, err)
		}
	}
	return nil
}

// safeWrite queues a packet for the writer goroutine. Returns immediately;
// transport errors surface on the next call (after the writer notices and
// closes the connection). If the queue is full, kicks the client — slow
// consumers must not block the broadcaster.
func (c *ClientConnection) safeWrite(packetID int32, payload []byte) error {
	c.sendMu.Lock()
	if c.isClosed() {
		c.sendMu.Unlock()
		return fmt.Errorf("connection closed")
	}
	frame, err := protocol.BuildFrame(packetID, payload, c.compressionThreshold)
	if err != nil {
		c.sendMu.Unlock()
		return fmt.Errorf("build frame: %w", err)
	}
	select {
	case c.outbound <- outboundMsg{frame: frame}:
		c.sendMu.Unlock()
		return nil
	default:
		c.sendMu.Unlock()
		go c.cleanup() // queue full — kick
		return fmt.Errorf("send queue full")
	}
}
