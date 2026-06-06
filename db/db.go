// Package db is the PostgreSQL connection layer: it builds a pgx connection
// pool from environment configuration, verifies it with a ping, and exposes
// the pool for the rest of the server to query. It deliberately knows nothing
// about the schema — there are no tables yet. Callers reach the pool via
// DB.Pool and run their own queries.
package db

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config describes how to reach Postgres. Either set URL to a full
// "postgres://user:pass@host:port/dbname?sslmode=..." DSN, or leave it empty
// and fill the discrete fields — Connect assembles a DSN from them.
type Config struct {
	URL string // full DSN; when non-empty, the discrete fields are ignored

	Host     string
	Port     string
	User     string
	Password string
	Name     string // database name
	SSLMode  string // disable | require | verify-ca | verify-full

	// MaxConns caps the pool size. 0 lets pgx pick its default (4× CPUs).
	MaxConns int32

	// ConnectTimeout bounds the initial dial + ping. 0 → DefaultConnectTimeout.
	ConnectTimeout time.Duration
}

// DefaultConnectTimeout is how long Connect waits for the pool to come up
// (dial + ping) before giving up.
const DefaultConnectTimeout = 10 * time.Second

// ConfigFromEnv reads the POSTGRES_* / DATABASE_URL environment variables,
// applying defaults that line up with the bundled docker-compose service.
//
//	DATABASE_URL        full DSN; overrides the discrete vars when set
//	POSTGRES_HOST       default "localhost"
//	POSTGRES_PORT       default "5432"
//	POSTGRES_USER       default "minecraft"
//	POSTGRES_PASSWORD   default "minecraft"
//	POSTGRES_DB         default "minecraft"
//	POSTGRES_SSLMODE    default "disable"
//	POSTGRES_MAX_CONNS  default 0 (pgx default)
func ConfigFromEnv() Config {
	c := Config{
		URL:      os.Getenv("DATABASE_URL"),
		Host:     getenv("POSTGRES_HOST", "localhost"),
		Port:     getenv("POSTGRES_PORT", "5432"),
		User:     getenv("POSTGRES_USER", "minecraft"),
		Password: getenv("POSTGRES_PASSWORD", "minecraft"),
		Name:     getenv("POSTGRES_DB", "minecraft"),
		SSLMode:  getenv("POSTGRES_SSLMODE", "disable"),
	}
	if v := os.Getenv("POSTGRES_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.MaxConns = int32(n)
		}
	}
	return c
}

// DSN returns the connection string Connect will dial: Config.URL verbatim
// when set, otherwise a DSN assembled from the discrete fields.
func (c Config) DSN() string {
	if c.URL != "" {
		return c.URL
	}
	return fmt.Sprintf(
		"postgres://%s:%s@%s/%s?sslmode=%s",
		c.User, c.Password, net.JoinHostPort(c.Host, c.Port), c.Name, c.SSLMode,
	)
}

// DB wraps a pgx connection pool. Safe for concurrent use by many goroutines
// — pgxpool hands out connections from the pool per query.
type DB struct {
	Pool *pgxpool.Pool
}

// Connect builds the pool from c and verifies it with a ping. The returned
// DB is ready for queries; the caller owns it and must Close it on shutdown.
// A failed ping closes the pool and returns the error, so a non-nil DB is
// always live.
func Connect(ctx context.Context, c Config) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(c.DSN())
	if err != nil {
		return nil, fmt.Errorf("parsing postgres config: %w", err)
	}
	if c.MaxConns > 0 {
		poolCfg.MaxConns = c.MaxConns
	}

	timeout := c.ConnectTimeout
	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}
	return &DB{Pool: pool}, nil
}

// Ping checks the connection is still alive. Cheap; suitable for health
// checks.
func (d *DB) Ping(ctx context.Context) error {
	return d.Pool.Ping(ctx)
}

// Close drains and closes every pooled connection. Idempotent-ish: safe to
// call once on shutdown. No-op on a nil receiver so callers can defer it
// even when Connect was skipped.
func (d *DB) Close() {
	if d == nil || d.Pool == nil {
		return
	}
	d.Pool.Close()
}

func getenv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
