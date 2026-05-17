package dispatch

import (
	"context"

	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
)

type Dispatcher interface {
	PublishFileUploaded(context.Context, eventbus.FileUploadedEvent) error
	PublishFileAction(context.Context, eventbus.FileActionEvent) error
}
