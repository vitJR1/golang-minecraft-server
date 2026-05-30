// Command loadtest spins up N simulated Minecraft 1.20.1 clients against
// our server and prints throughput + error stats. By default it runs the
// server in-process so pprof traces include the server hot paths.
//
// Example:
//
//	go run ./cmd/loadtest -clients 200 -duration 30s -pprof :6060
//	# while running:
//	go tool pprof -http :7000 http://localhost:6060/debug/pprof/profile?seconds=20
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"minecraft-server/server"
)

func main() {
	var (
		nClients  = flag.Int("clients", 100, "number of simulated clients")
		duration  = flag.Duration("duration", 30*time.Second, "steady-state duration after ramp-up")
		addr      = flag.String("addr", "127.0.0.1:25565", "Minecraft server address")
		rampDur   = flag.Duration("ramp", 5*time.Second, "spread connect attempts over this window")
		posHz     = flag.Int("pos-hz", 20, "position updates per second per client")
		external  = flag.Bool("external", false, "skip starting the in-process server; connect to addr")
		pprofAddr = flag.String("pprof", "localhost:6060", "pprof HTTP listen address (empty to disable)")
		quiet     = flag.Bool("quiet", true, "silence server-side fmt.Printf during the run")
	)
	flag.Parse()

	if *quiet && !*external {
		os.Stdout = devNull()
	}

	if !*external {
		startInProcessServer(*addr)
		// Give the listener a moment to come up.
		time.Sleep(100 * time.Millisecond)
	}

	if *pprofAddr != "" {
		go func() {
			log.Println("pprof listening on", *pprofAddr)
			_ = http.ListenAndServe(*pprofAddr, nil)
		}()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agg := &stats{}

	var wg sync.WaitGroup
	wg.Add(*nClients)

	rampStep := *rampDur / time.Duration(*nClients)
	if rampStep <= 0 {
		rampStep = time.Microsecond
	}

	startedAt := time.Now()
	for i := 0; i < *nClients; i++ {
		i := i
		time.Sleep(rampStep)
		go func() {
			defer wg.Done()
			runClient(ctx, *addr, i, *posHz, agg)
		}()
	}
	rampedAt := time.Now()
	restoreStdout()
	fmt.Fprintf(os.Stderr, "ramped %d clients in %s (%.1f conn/s target)\n",
		*nClients, rampedAt.Sub(startedAt).Round(time.Millisecond),
		float64(*nClients)/rampedAt.Sub(startedAt).Seconds())

	// Steady-state phase
	steadyStart := time.Now()
	time.Sleep(*duration)
	steadyEnd := time.Now()

	cancel()
	wg.Wait()
	finishedAt := time.Now()

	report(agg, *nClients, steadyEnd.Sub(steadyStart), finishedAt.Sub(startedAt))
}

// startInProcessServer mirrors what main.go does, but without the banlist
// loader (loadtest needs no bans). Runs in a goroutine and never returns.
func startInProcessServer(addr string) {
	srv := server.New()
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal("listen:", err)
	}
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go srv.HandleConn(conn)
		}
	}()
}

// devNull replaces os.Stdout so the server's fmt.Printf calls don't drown
// the harness output. The original stdout is captured for restoration.
var (
	originalStdout *os.File
	stdoutMu       sync.Mutex
)

func devNull() *os.File {
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	if originalStdout == nil {
		originalStdout = os.Stdout
	}
	null, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	return null
}

func restoreStdout() {
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	if originalStdout != nil {
		os.Stdout = originalStdout
	}
}

// stats accumulates counters across all simulated clients. Update via the
// atomic methods so concurrent clients don't tear values.
type stats struct {
	connected     atomic.Int64
	loggedIn      atomic.Int64
	disconnects   atomic.Int64
	errors        atomic.Int64
	packetsSent   atomic.Int64
	packetsRecv   atomic.Int64
	bytesSent     atomic.Int64
	bytesRecv     atomic.Int64
	loginLatTotal atomic.Int64 // nanoseconds, sum over all logins
}

func report(s *stats, target int, steady, total time.Duration) {
	loggedIn := s.loggedIn.Load()
	var avgLoginMs float64
	if loggedIn > 0 {
		avgLoginMs = float64(s.loginLatTotal.Load()) / float64(loggedIn) / float64(time.Millisecond)
	}
	mibSent := float64(s.bytesSent.Load()) / 1024.0 / 1024.0
	mibRecv := float64(s.bytesRecv.Load()) / 1024.0 / 1024.0
	steadySec := steady.Seconds()
	if steadySec <= 0 {
		steadySec = 1 // avoid /0 in the rate calculations
	}

	fmt.Fprintf(os.Stderr, "\n=== load test report ===\n")
	fmt.Fprintf(os.Stderr, "target clients          : %d\n", target)
	fmt.Fprintf(os.Stderr, "connected               : %d\n", s.connected.Load())
	fmt.Fprintf(os.Stderr, "logged in               : %d  (avg login %.1f ms)\n", loggedIn, avgLoginMs)
	fmt.Fprintf(os.Stderr, "disconnects             : %d\n", s.disconnects.Load())
	fmt.Fprintf(os.Stderr, "errors                  : %d\n", s.errors.Load())
	fmt.Fprintf(os.Stderr, "packets sent / recv     : %d / %d\n", s.packetsSent.Load(), s.packetsRecv.Load())
	fmt.Fprintf(os.Stderr, "MiB sent / recv (total) : %.2f / %.2f\n", mibSent, mibRecv)
	fmt.Fprintf(os.Stderr, "steady-state duration   : %s\n", steady.Round(time.Millisecond))
	fmt.Fprintf(os.Stderr, "wallclock total         : %s\n", total.Round(time.Millisecond))
	fmt.Fprintf(os.Stderr, "send rate (steady)      : %.0f pkt/s, %.2f MiB/s\n",
		float64(s.packetsSent.Load())/steadySec, mibSent/steadySec)
	fmt.Fprintf(os.Stderr, "recv rate (steady)      : %.0f pkt/s, %.2f MiB/s\n",
		float64(s.packetsRecv.Load())/steadySec, mibRecv/steadySec)
}

// Random-looking but reproducible per-client seed so we get varied movement
// without all clients standing still.
func clientRand(id int) *rand.Rand {
	return rand.New(rand.NewSource(int64(id) ^ time.Now().UnixNano()))
}

// runClient is implemented in client.go.
var _ = io.EOF
