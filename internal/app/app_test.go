package app

import (
	"strings"
	"testing"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/core"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	"src.solsynth.dev/sosys/filesystem/internal/module"
)

// TestHostRequiresGRPCTLSFiles checks the gRPC listener refuses to start with
// TLS enabled but no certificate material.
func TestHostRequiresGRPCTLSFiles(t *testing.T) {
	cfg := &config.Config{}
	cfg.HTTP.Port = "0"
	cfg.GRPC.Port = "0"
	cfg.GRPC.UseTLS = true

	files := service.NewFileService(&database.DB{}, storage.NewLocalBackend(t.TempDir()))
	r := &Runtime{
		Config: cfg, Mode: "master",
		Core: &core.Services{Config: cfg, Mode: "master", Files: files},
	}
	_, err := r.host(&module.Resolution{Enabled: map[string]bool{}}, nil)
	if err == nil {
		t.Fatal("expected an error when grpc tls files are missing")
	}
	if !strings.Contains(err.Error(), "grpc.certFile") {
		t.Fatalf("error = %v, want a grpc tls message", err)
	}
}

// TestNormalizeMode covers the bundled override that turns a master into an
// in-process worker deployment.
func TestNormalizeMode(t *testing.T) {
	tests := []struct {
		name    string
		bundled bool
		mode    string
		want    string
	}{
		{name: "default", mode: "", want: "master"},
		{name: "explicit", mode: "Worker", want: "worker"},
		{name: "bundled override", bundled: true, mode: "master", want: "bundled"},
		{name: "bundled keeps worker", bundled: true, mode: "worker", want: "worker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Bundled: config.BundledConfig{Enable: tt.bundled}}
			if got := normalizeMode(cfg, tt.mode); got != tt.want {
				t.Fatalf("normalizeMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestModulesResponseReflectsResolvedSet proves the introspection payload is
// built from the resolved factories, not the compiled list.
func TestModulesResponseReflectsResolvedSet(t *testing.T) {
	factories := []module.Factory{
		{ID: "app.solian.files.core", Kind: module.KindFeature, Capabilities: []string{"files.read"}},
		{ID: "app.solian.files.auth", Kind: module.KindFeature, Capabilities: []string{"auth", "files.read"}},
	}
	resp := modulesResponse(factories)
	if len(resp.Modules) != 2 {
		t.Fatalf("modules = %d, want 2", len(resp.Modules))
	}
	if len(resp.Capabilities) != 2 || resp.Capabilities[0] != "auth" {
		t.Fatalf("capabilities = %#v, want deduplicated and sorted", resp.Capabilities)
	}
	if resp.Modules[0].Kind != "feature" {
		t.Fatalf("kind = %q, want feature", resp.Modules[0].Kind)
	}
}

// TestCoreModelsDeduplicatesAcrossModules keeps AutoMigrate from applying the
// same model twice when two modules share a table.
func TestCoreModelsDeduplicatesAcrossModules(t *testing.T) {
	type model struct{}
	first := &model{}
	second := &model{}
	got := coreModels([]module.Factory{
		{Models: []any{first}},
		{Models: []any{second}},
	})
	if len(got) != 1 || got[0] != any(first) {
		t.Fatalf("coreModels() = %#v, want only the first occurrence", got)
	}
}
