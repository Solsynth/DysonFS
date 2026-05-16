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
