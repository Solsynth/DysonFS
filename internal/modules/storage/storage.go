// Package storage mounts the object-storage surfaces as modules: the master S3
// gateway and the standalone storage node.
package storage

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/handler"
	"src.solsynth.dev/sosys/filesystem/internal/module"
	"src.solsynth.dev/sosys/filesystem/internal/s3server"
)

// Module IDs.
const (
	IDMasterS3 = "app.solian.files.s3"
	IDNodeS3   = "app.solian.files.storage-node-s3"
)

// MasterS3 returns the master S3 gateway module. It is off by default and also
// honours the legacy masterS3.enabled switch.
func MasterS3() module.Factory {
	return module.Factory{
		ID:             IDMasterS3,
		Kind:           module.KindFeature,
		DefaultEnabled: false,
		Requires:       []string{"app.solian.files.core"},
		Capabilities:   []string{"files.s3", "files.s3.gateway"},
		Models:         []any{&database.S3Token{}},
		New: func() module.Instance {
			return &instance{configure: func(host *module.Host) error {
				cfg := host.Core.Config
				if !cfg.MasterS3.Enabled {
					host.Log.Info().Msg("s3 gateway module compiled in but masterS3.enabled is false; not started")
					return nil
				}
				backend := s3server.NewMasterBackend(host.Core.Files, cfg.Storage.TempDir)
				srv := s3server.NewWithResolver(backend, backend)
				h := srv.Handler()

				mux := http.NewServeMux()
				mux.HandleFunc("/", h)
				server := &http.Server{
					Addr:         ":" + cfg.MasterS3.Port,
					Handler:      mux,
					ReadTimeout:  120 * time.Second,
					WriteTimeout: 120 * time.Second,
				}
				host.Core.Servers = append(host.Core.Servers, server)
				go func() {
					host.Log.Info().Str("addr", server.Addr).Msg("master S3 API listening")
					if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
						host.Log.Error().Err(err).Msg("master S3 server failed")
					}
				}()
				return nil
			}}
		},
	}
}

// NodeS3 returns the standalone storage-node module: the /_dfs control surface
// plus the S3 data plane on its own port. It only runs in storage mode.
func NodeS3() module.Factory {
	return module.Factory{
		ID:             IDNodeS3,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Capabilities:   []string{"files.storage-node", "files.s3.node"},
		New: func() module.Instance {
			return &instance{configure: func(host *module.Host) error {
				cfg := host.Core.Config
				if host.Core.Mode != "storage" {
					return nil
				}
				backend := s3server.NewStorageBackend(host.Core.Files.Storage())
				srv := s3server.New(backend, cfg.StorageNode.S3AccessKey, cfg.StorageNode.S3SecretKey)
				s3handler := srv.Handler()

				r := gin.New()
				r.Use(gin.Recovery())
				handler.RegisterStorageNodeRoutes(r, handler.FromCore(host.Core))
				r.NoRoute(func(c *gin.Context) { s3handler(c.Writer, c.Request) })

				addr := ":" + cfg.StorageNode.Port
				server := &http.Server{Addr: addr, Handler: r, ReadTimeout: 120 * time.Second, WriteTimeout: 120 * time.Second}
				host.Core.Servers = append(host.Core.Servers, server)
				go func() {
					host.Log.Info().Str("addr", addr).Msg("storage node HTTP server listening")
					if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
						host.Log.Error().Err(err).Msg("storage node HTTP server failed")
					}
				}()
				return nil
			}}
		},
	}
}

type instance struct{ configure func(*module.Host) error }

func (i *instance) Configure(_ context.Context, host *module.Host) error {
	if i.configure != nil {
		return i.configure(host)
	}
	return nil
}

func (i *instance) Stop(context.Context) error { return nil }
