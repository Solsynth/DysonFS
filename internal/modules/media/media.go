// Package media mounts on-the-fly media transforms as a module. Without it the
// content routes serve stored bytes and the transform query parameters are
// ignored.
package media

import (
	"context"
	"fmt"
	"strings"

	"github.com/davidbyttow/govips/v2/vips"

	"src.solsynth.dev/sosys/filesystem/internal/media"
	"src.solsynth.dev/sosys/filesystem/internal/module"
)

// ModuleID is the media transform module's registry ID.
const ModuleID = "app.solian.files.media"

// Factory returns the media transform module. It is off by default and also
// honours the legacy media.enable switch.
func Factory() module.Factory {
	return module.Factory{
		ID:             ModuleID,
		Kind:           module.KindFeature,
		DefaultEnabled: false,
		Requires:       []string{"app.solian.files.core"},
		Capabilities:   []string{"files.media", "files.media.transform", "files.media.cache"},
		New:            func() module.Instance { return &instance{} },
	}
}

type instance struct {
	srv *media.Service
}

func (i *instance) Configure(ctx context.Context, host *module.Host) error {
	cfg := host.Core.Config
	if !cfg.Media.Enable {
		host.Log.Info().Msg("media module compiled in but media.enable is false; transforms disabled")
		return nil
	}

	// libvips is process-global and startup is idempotent; every mode that
	// builds a transform service must initialize it first.
	vips.Startup(nil)

	cacheBackend := host.Core.Files.Storage()
	if cfg.Media.Cache.Kind == "s3" {
		poolID := strings.TrimSpace(cfg.Media.Cache.PoolID)
		if poolID == "" {
			return fmt.Errorf(`init media: media.cache.poolId is required when cache.kind = "s3"`)
		}
		poolBackend, err := host.Core.Files.BackendForPoolID(&poolID)
		if err != nil {
			return fmt.Errorf("init media: resolve cache pool %q: %w", poolID, err)
		}
		cacheBackend = poolBackend
	}

	med, err := media.New(cfg.Media, cacheBackend)
	if err != nil {
		return fmt.Errorf("init media: %w", err)
	}
	host.Core.Files.SetMedia(med)
	i.srv = med

	go med.Start(ctx)
	host.Log.Info().Str("cache", cfg.Media.Cache.Kind).Msg("media transforms enabled")
	return nil
}

func (i *instance) Stop(context.Context) error {
	if i.srv != nil {
		// The disk cache sweeper stops with the context; libvips is shared
		// with any other consumer and is intentionally left running.
		i.srv = nil
	}
	return nil
}
