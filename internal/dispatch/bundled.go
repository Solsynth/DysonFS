package dispatch

import (
	"context"
	"fmt"

	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/worker"
)

type Bundled struct {
	workers     []*worker.Worker
	uploadQueue chan eventbus.FileUploadedEvent
	actionQueue chan eventbus.FileActionEvent
}

func NewBundled(workers []*worker.Worker) *Bundled {
	filtered := make([]*worker.Worker, 0, len(workers))
	for _, w := range workers {
		if w != nil {
			filtered = append(filtered, w)
		}
	}
	queueSize := len(filtered) * 4
	if queueSize < 1 {
		queueSize = 1
	}
	dispatcher := &Bundled{
		workers:     filtered,
		uploadQueue: make(chan eventbus.FileUploadedEvent, queueSize),
		actionQueue: make(chan eventbus.FileActionEvent, queueSize),
	}
	for _, w := range filtered {
		go dispatcher.runUploadWorker(w)
		go dispatcher.runActionWorker(w)
	}
	return dispatcher
}

func (d *Bundled) PublishFileUploaded(ctx context.Context, evt eventbus.FileUploadedEvent) error {
	if len(d.workers) == 0 {
		return fmt.Errorf("bundled dispatcher has no workers")
	}
	logging.Log.Info().Str("fileId", evt.FileID).Str("taskId", evt.TaskID).Int("workers", len(d.workers)).Msg("bundled upload processing dispatched")
	_ = ctx
	select {
	case d.uploadQueue <- evt:
	default:
		logging.Log.Warn().Str("fileId", evt.FileID).Str("taskId", evt.TaskID).Int("queueSize", cap(d.uploadQueue)).Msg("bundled upload queue saturated, applying backpressure")
		d.uploadQueue <- evt
	}
	return nil
}

func (d *Bundled) PublishFileAction(_ context.Context, evt eventbus.FileActionEvent) error {
	if len(d.workers) == 0 {
		return nil
	}
	select {
	case d.actionQueue <- evt:
	default:
		logging.Log.Warn().Str("fileId", evt.FileID).Str("action", evt.Action).Int("queueSize", cap(d.actionQueue)).Msg("bundled action queue saturated, applying backpressure")
		d.actionQueue <- evt
	}
	return nil
}

func (d *Bundled) runUploadWorker(w *worker.Worker) {
	for evt := range d.uploadQueue {
		if err := w.ProcessUploadedFile(context.Background(), evt); err != nil {
			logging.Log.Error().Err(err).Str("fileId", evt.FileID).Str("taskId", evt.TaskID).Msg("bundled upload processing failed")
		}
	}
}

func (d *Bundled) runActionWorker(w *worker.Worker) {
	for evt := range d.actionQueue {
		if err := w.HandleFileAction(evt); err != nil {
			logging.Log.Error().Err(err).Str("fileId", evt.FileID).Str("action", evt.Action).Msg("bundled file action failed")
		}
	}
}
