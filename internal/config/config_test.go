package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPoolsConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	data := []byte(`
[app]
name = "test"

[[pools]]
id = "500e5ed8-bd44-4359-bc0a-ec85e2adf447"
name = "Driver"
default = true
hidden = false

[pools.storage]
enableSigned = true
endpoint = "/tmp/data"
bucket = "bucket"

[pools.policy]
publicUsable = true
`)

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := len(cfg.Pools); got != 1 {
		t.Fatalf("len(cfg.Pools) = %d, want 1", got)
	}
	if cfg.Pools[0].ID != "500e5ed8-bd44-4359-bc0a-ec85e2adf447" {
		t.Fatalf("pool id = %q, want uuid", cfg.Pools[0].ID)
	}
	if !cfg.Pools[0].Default {
		t.Fatal("pool default flag was false, want true")
	}
}

func TestLoadRedisDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	data := []byte(`
[redis]
addr = "redis:6379"
db = 3
`)

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Redis.Addr != "redis:6379" {
		t.Fatalf("redis addr = %q, want redis:6379", cfg.Redis.Addr)
	}
	if cfg.Redis.DB != 3 {
		t.Fatalf("redis db = %d, want 3", cfg.Redis.DB)
	}
}

func TestLoadRedisDBDefaultsToZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	data := []byte(`
[redis]
addr = "redis:6379"
`)

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Redis.DB != 0 {
		t.Fatalf("redis db = %d, want default 0", cfg.Redis.DB)
	}
}
