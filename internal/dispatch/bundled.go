package dispatch

import (
	"context"
	"sync"

	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
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
		_ = d.worker().ProcessUploadedFile(ctx, evt)
	}()
	return nil
}

func (d *Bundled) PublishFileAction(_ context.Context, evt eventbus.FileActionEvent) error {
	if len(d.workers) == 0 {
		return nil
	}
	go func() {
		_ = d.worker().HandleFileAction(evt)
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
