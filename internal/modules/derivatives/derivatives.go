// Package derivatives wires the per-kind derivative modules. Each kind
// (app.solian.files.derivatives.<kind>) registers the preview generator for
// that file type on the shared registry; a build without it produces no
// previews for that kind.
package derivatives

import (
	"context"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/module"
)

// Module IDs.
const (
	IDImage = "app.solian.files.derivatives.image"
	IDVideo = "app.solian.files.derivatives.video"
)

// Image returns the image derivative module (compressed variant).
func Image() module.Factory {
	return module.Factory{
		ID:             IDImage,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Capabilities:   []string{"derivatives", "derivatives.image", "derivatives.compression"},
		New:            func() module.Instance { return &instance{kind: "image"} },
	}
}

// Video returns the video derivative module (thumbnail frame).
func Video() module.Factory {
	return module.Factory{
		ID:             IDVideo,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Capabilities:   []string{"derivatives", "derivatives.video", "derivatives.thumbnail"},
		New:            func() module.Instance { return &instance{kind: "video"} },
	}
}

type instance struct{ kind string }

func (i *instance) Configure(_ context.Context, host *module.Host) error {
	w := host.Core.Worker
	if w == nil {
		return nil
	}
	switch i.kind {
	case "image":
		host.Core.Derivatives.Register("image", func(ctx context.Context, file *database.CloudFile, path string) error {
			return w.ProcessImage(ctx, file, path)
		})
	case "video":
		host.Core.Derivatives.Register("video", func(ctx context.Context, file *database.CloudFile, path string) error {
			return w.ProcessVideo(ctx, file, path)
		})
	}
	host.Log.Info().Str("kind", i.kind).Msg("derivative kind enabled")
	return nil
}

func (i *instance) Stop(context.Context) error { return nil }
