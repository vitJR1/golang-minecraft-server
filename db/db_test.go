package db

import "testing"

func TestDSNFromDiscreteFields(t *testing.T) {
	c := Config{
		Host:     "dbhost",
		Port:     "5433",
		User:     "u",
		Password: "p",
		Name:     "mc",
		SSLMode:  "require",
	}
	want := "postgres://u:p@dbhost:5433/mc?sslmode=require"
	if got := c.DSN(); got != want {
		t.Errorf("DSN() = %q, want %q", got, want)
	}
}

func TestDSNURLOverridesFields(t *testing.T) {
	c := Config{
		URL:  "postgres://override/db",
		Host: "ignored",
		User: "ignored",
	}
	if got := c.DSN(); got != "postgres://override/db" {
		t.Errorf("DSN() = %q, want the URL verbatim", got)
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	// Empty values are treated as unset (getenv falls back to defaults).
	for _, k := range []string{
		"DATABASE_URL", "POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_USER",
		"POSTGRES_PASSWORD", "POSTGRES_DB", "POSTGRES_SSLMODE", "POSTGRES_MAX_CONNS",
	} {
		t.Setenv(k, "")
	}
	c := ConfigFromEnv()
	if c.DSN() != "postgres://minecraft:minecraft@localhost:5432/minecraft?sslmode=disable" {
		t.Errorf("default DSN mismatch: %q", c.DSN())
	}
	if c.MaxConns != 0 {
		t.Errorf("default MaxConns = %d, want 0", c.MaxConns)
	}
}

func TestConfigFromEnvOverrides(t *testing.T) {
	t.Setenv("POSTGRES_HOST", "pg")
	t.Setenv("POSTGRES_PORT", "6000")
	t.Setenv("POSTGRES_MAX_CONNS", "12")
	c := ConfigFromEnv()
	if c.Host != "pg" || c.Port != "6000" {
		t.Errorf("host/port: got %s:%s", c.Host, c.Port)
	}
	if c.MaxConns != 12 {
		t.Errorf("MaxConns: got %d, want 12", c.MaxConns)
	}
}

func TestCloseNilSafe(t *testing.T) {
	var d *DB
	d.Close() // must not panic
	(&DB{}).Close()
}
