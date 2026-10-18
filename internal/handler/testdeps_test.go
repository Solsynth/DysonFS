package handler

import (
	"context"

	"src.solsynth.dev/sosys/filesystem/internal/core"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

// analysisRegistryFor builds the registry the analysis modules would build,
// with every kind enabled. *service.FileService satisfies core.AnalysisEngine.
func analysisRegistryFor(files *service.FileService) *core.AnalysisRegistry {
	registry := core.NewAnalysisRegistry(files)
	registry.Enable("image")
	registry.Enable("video")
	registry.Enable("audio")
	return registry
}

// derivativeRegistryFor builds the registry the derivative modules would build,
// backed by an in-process worker.
func derivativeRegistryFor(w interface {
	ProcessImage(ctx context.Context, file *database.CloudFile, path string) error
	ProcessVideo(ctx context.Context, file *database.CloudFile, path string) error
}) *core.DerivativeRegistry {
	registry := core.NewDerivativeRegistry()
	registry.Register("image", w.ProcessImage)
	registry.Register("video", w.ProcessVideo)
	return registry
}
