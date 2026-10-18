// Package worker mounts the processing loop as a module. Without it a process
// accepts uploads but never derives previews, rehashes, or runs maintenance.
package worker

import (
	"context"

	"src.solsynth.dev/sosys/filesystem/internal/module"
)

// ModuleID is the worker module's registry ID.
const ModuleID = "app.solian.files.worker"

// Factory returns the worker module descriptor.
func Factory() module.Factory {
	return module.Factory{
		ID:             ModuleID,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Capabilities:   []string{"files.worker", "files.maintenance", "files.pool-migration"},
		New:            func() module.Instance { return &instance{} },
	}
}

type instance struct{}

// Configure starts the loop. The master dispatches uploads over NATS and does
// not start a worker; a worker process runs this module alone.
func (i *instance) Configure(ctx context.Context, host *module.Host) error {
	w := host.Core.Worker
	if w == nil {
		return nil
	}
	// The master owns the HTTP and gRPC surfaces and dispatches uploads over
	// NATS; it must not consume its own queue. Only the worker and bundled
	// modes run the loop in-process.
	switch host.Core.Mode {
	case "worker", "bundled":
	default:
		host.Log.Info().Str("mode", host.Core.Mode).Msg("worker module idle in this mode")
		return nil
	}
	if loop, ok := w.(interface{ Start(context.Context) error }); ok {
		go func() { _ = loop.Start(ctx) }()
		host.Log.Info().Msg("worker loop started")
	}
	return nil
}

func (i *instance) Stop(context.Context) error { return nil }
