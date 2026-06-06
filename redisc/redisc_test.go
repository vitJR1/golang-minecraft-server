package redisc

import "testing"

func TestConfigFromEnvDefaults(t *testing.T) {
	for _, k := range []string{"REDIS_URL", "REDIS_HOST", "REDIS_PORT", "REDIS_PASSWORD", "REDIS_DB"} {
		t.Setenv(k, "")
	}
	c := ConfigFromEnv()
	if c.Host != "localhost" || c.Port != "6379" {
		t.Errorf("default addr: got %s:%s, want localhost:6379", c.Host, c.Port)
	}
	if c.Password != "" || c.DB != 0 {
		t.Errorf("defaults: password=%q db=%d", c.Password, c.DB)
	}
}

func TestConfigFromEnvOverrides(t *testing.T) {
	t.Setenv("REDIS_HOST", "cache")
	t.Setenv("REDIS_PORT", "6380")
	t.Setenv("REDIS_PASSWORD", "secret")
	t.Setenv("REDIS_DB", "3")
	c := ConfigFromEnv()
	if c.Host != "cache" || c.Port != "6380" || c.Password != "secret" || c.DB != 3 {
		t.Errorf("overrides not applied: %+v", c)
	}
}

func TestCloseNilSafe(t *testing.T) {
	var c *Client
	if err := c.Close(); err != nil { // must not panic, returns nil
		t.Errorf("nil Close: %v", err)
	}
	if err := (&Client{}).Close(); err != nil {
		t.Errorf("empty Close: %v", err)
	}
}
