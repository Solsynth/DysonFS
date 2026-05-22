package dispatch

import (
	"context"
	"sync"

	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/worker"
)

type Bundled struct {
	workers []*worker.Worker
	next    uint32
	mu      sync.Mutex
}

func NewBundled(workers []*worker.Worker) *Bundled {
	filtered := make([]*worker.Worker, 0, len(workers))
	for _, w := range workers {
		if w != nil {
			filtered = append(filtered, w)
		}
	}
	return &Bundled{workers: filtered}
}

func (d *Bundled) PublishFileUploaded(ctx context.Context, evt eventbus.FileUploadedEvent) error {
	if len(d.workers) == 0 {
		return nil
	}
	go func() {
		if err := d.worker().ProcessUploadedFile(ctx, evt); err != nil {
			logging.Log.Error().Err(err).Str("fileId", evt.FileID).Str("taskId", evt.TaskID).Msg("bundled upload processing failed")
		}
	}()
	return nil
}

func (d *Bundled) PublishFileAction(_ context.Context, evt eventbus.FileActionEvent) error {
	if len(d.workers) == 0 {
		return nil
	}
	go func() {
		if err := d.worker().HandleFileAction(evt); err != nil {
			logging.Log.Error().Err(err).Str("fileId", evt.FileID).Str("action", evt.Action).Msg("bundled file action failed")
		}
	}()
	return nil
}

func (d *Bundled) worker() *worker.Worker {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.workers) == 1 {
		return d.workers[0]
	}
	w := d.workers[d.next%uint32(len(d.workers))]
	d.next++
	return w
}
