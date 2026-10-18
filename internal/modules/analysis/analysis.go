// Package analysis wires the per-kind analysis modules. Each kind
// (app.solian.files.analysis.<kind>) turns on one branch of the analyzer and
// one branch of the background reanalysis queue. A build with none of them
// stores files without derived metadata.
package analysis

import (
	"context"

	"src.solsynth.dev/sosys/filesystem/internal/module"
)

// Module IDs.
const (
	IDImage     = "app.solian.files.analysis.image"
	IDVideo     = "app.solian.files.analysis.video"
	IDAudio     = "app.solian.files.analysis.audio"
	IDReanalyze = "app.solian.files.analysis.reanalysis"
)

// kindModule enables one analysis kind on the shared registry.
type kindModule struct {
	id   string
	kind string
}

// Image returns the image analysis module.
func Image() module.Factory { return factory(IDImage, "image", "Image dimensions, EXIF and blurhash") }

// Video returns the video analysis module.
func Video() module.Factory { return factory(IDVideo, "video", "Video probe, dimensions and aspect ratio") }

// Audio returns the audio analysis module.
func Audio() module.Factory { return factory(IDAudio, "audio", "Audio probe and stream metadata") }

// Reanalysis returns the background reanalysis module. It owns the queue that
// re-derives metadata for client-assisted uploads; without it the queue never
// runs, so client-supplied metadata stays as uploaded.
func Reanalysis() module.Factory {
	return module.Factory{
		ID:             IDReanalyze,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Capabilities:   []string{"analysis.reanalysis", "analysis.queue"},
		New: func() module.Instance {
			return &reanalysisInstance{}
		},
	}
}

func factory(id, kind, description string) module.Factory {
	return module.Factory{
		ID:             id,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Capabilities:   []string{"analysis", "analysis." + kind},
		New: func() module.Instance {
			return &kindModule{id: id, kind: kind}
		},
	}
}

func (m *kindModule) Configure(_ context.Context, host *module.Host) error {
	// The app builds one registry and hands it to the worker as well; enabling
	// a kind here mutates that same instance.
	host.Core.Analysis.Enable(m.kind)
	host.Log.Info().Str("module", m.id).Str("kind", m.kind).Msg("analysis kind enabled")
	return nil
}

func (m *kindModule) Stop(context.Context) error { return nil }

type reanalysisInstance struct{}

func (r *reanalysisInstance) Configure(_ context.Context, host *module.Host) error {
	host.Log.Info().Msg("analysis reanalysis queue enabled")
	return nil
}

func (r *reanalysisInstance) Stop(context.Context) error { return nil }
