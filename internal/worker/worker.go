package worker

import (
	"context"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
)

type Worker struct{ bus *eventbus.Bus }

func New(bus *eventbus.Bus) *Worker { return &Worker{bus: bus} }

func (w *Worker) Start(ctx context.Context) error {
	go func() {
		<-ctx.Done()
	}()
	logging.Log.Info().Msg("worker loop started")
	return nil
}

func (w *Worker) ProcessUploadedFile(_ context.Context, evt eventbus.FileUploadedEvent) error {
	_ = evt
	time.Sleep(1 * time.Millisecond)
	return nil
}
