// Package redisc is the Redis connection layer. It builds a go-redis client
// from environment configuration, verifies it with a ping, and exposes the
// client for the rest of the server (caching, sessions, cross-instance
// coordination later). It holds no domain logic — just the connection.
//
// Named "redisc" (redis client) so the package name doesn't collide with the
// imported go-redis package, which is itself "redis".
package redisc

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config describes how to reach Redis. Either set URL to a full
// "redis://user:pass@host:port/db" string, or leave it empty and fill the
// discrete fields.
type Config struct {
	URL string // full redis:// URL; when non-empty, the discrete fields are ignored

	Host     string
	Port     string
	Password string
	DB       int

	// ConnectTimeout bounds the initial ping. 0 → DefaultConnectTimeout.
	ConnectTimeout time.Duration
}

// DefaultConnectTimeout is how long Connect waits for the first ping to
// succeed before giving up.
const DefaultConnectTimeout = 10 * time.Second

// ConfigFromEnv reads the REDIS_* environment variables, applying defaults
// that line up with the bundled docker-compose service.
//
//	REDIS_URL        full redis:// URL; overrides the discrete vars when set
//	REDIS_HOST       default "localhost"
//	REDIS_PORT       default "6379"
//	REDIS_PASSWORD   default "" (no auth)
//	REDIS_DB         default 0
func ConfigFromEnv() Config {
	c := Config{
		URL:      os.Getenv("REDIS_URL"),
		Host:     getenv("REDIS_HOST", "localhost"),
		Port:     getenv("REDIS_PORT", "6379"),
		Password: os.Getenv("REDIS_PASSWORD"),
	}
	if v := os.Getenv("REDIS_DB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.DB = n
		}
	}
	return c
}

// Client wraps a go-redis client. Safe for concurrent use; go-redis pools
// connections internally.
type Client struct {
	*redis.Client
}

// Connect builds the client from c and verifies it with a ping. The returned
// Client is ready to use; the caller owns it and must Close it on shutdown.
// A failed ping closes the client and returns the error, so a non-nil Client
// is always live.
func Connect(ctx context.Context, c Config) (*Client, error) {
	var opts *redis.Options
	if c.URL != "" {
		parsed, err := redis.ParseURL(c.URL)
		if err != nil {
			return nil, fmt.Errorf("parsing redis url: %w", err)
		}
		opts = parsed
	} else {
		opts = &redis.Options{
			Addr:     net.JoinHostPort(c.Host, c.Port),
			Password: c.Password,
			DB:       c.DB,
		}
	}

	timeout := c.ConnectTimeout
	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("pinging redis: %w", err)
	}
	return &Client{Client: rdb}, nil
}

// Close releases the client's connection pool. No-op on a nil receiver so
// callers can defer it even when Connect was skipped.
func (c *Client) Close() error {
	if c == nil || c.Client == nil {
		return nil
	}
	return c.Client.Close()
}

func getenv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
