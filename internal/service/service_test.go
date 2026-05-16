package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

func TestBackendFromPoolStorageLocal(t *testing.T) {
	tmp := t.TempDir()
	backend, err := backendFromPoolStorage(PoolStorageConfig{Endpoint: tmp}, nil)
	if err != nil {
		t.Fatalf("backendFromPoolStorage() error = %v", err)
	}

	local, ok := backend.(*storage.LocalBackend)
	if !ok {
		t.Fatalf("backendFromPoolStorage() backend type = %T, want *storage.LocalBackend", backend)
	}

	const key = "files/example.txt"
	if err := local.Put(context.Background(), key, strings.NewReader("hello"), "text/plain"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmp, key)); err != nil {
		t.Fatalf("file not written: %v", err)
	}
}

func TestBackendFromPoolStorageMissingEndpoint(t *testing.T) {
	if _, err := backendFromPoolStorage(PoolStorageConfig{}, nil); err == nil {
		t.Fatal("backendFromPoolStorage() error = nil, want error")
	}
}
