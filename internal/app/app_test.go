package app

import (
	"testing"

	"src.solsynth.dev/sosys/filesystem/internal/config"
)

func TestStartMasterRequiresGrpcTLSFiles(t *testing.T) {
	cfg := &config.Config{}
	cfg.HTTP.Port = "0"
	cfg.GRPC.Port = "0"
	cfg.GRPC.UseTLS = true

	a := &App{cfg: cfg}
	if err := a.startMaster(nil); err == nil {
		t.Fatal("expected error when grpc tls files are missing")
	}
}
