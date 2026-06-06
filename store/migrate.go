package store

import (
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the "pgx5" database driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all pending up-migrations embedded under migrations/. dsn is
// a standard postgres DSN ("postgres://user:pass@host:port/db?sslmode=...");
// it's rewritten to the pgx5 scheme golang-migrate's driver expects. Safe to
// call on every startup — already-applied migrations are skipped, and an
// up-to-date schema returns nil (ErrNoChange is swallowed).
func Migrate(dsn string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("loading embedded migrations: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, migrateURL(dsn))
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return nil
}

// migrateURL rewrites a postgres:// DSN to the pgx5:// scheme registered by
// the migrate pgx/v5 driver. Other schemes are passed through unchanged.
func migrateURL(dsn string) string {
	for _, scheme := range []string{"postgres://", "postgresql://"} {
		if rest, ok := strings.CutPrefix(dsn, scheme); ok {
			return "pgx5://" + rest
		}
	}
	return dsn
}
