package core

import (
	"context"
	"fmt"

	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
)

// Module IDs the host itself branches on: the core gRPC surface only exists
// where the file modules are enabled.
const (
	ModuleFilesGRPC = "app.solian.files.grpc"
)

// Publisher is the event sink every HTTP and gRPC upload surface writes to.
// The NATS bus and the in-process bundled dispatcher both satisfy it, so a
// deployment without NATS still processes uploads.
type Publisher interface {
	PublishFileUploaded(context.Context, eventbus.FileUploadedEvent) error
	PublishFileAction(context.Context, eventbus.FileActionEvent) error
	PublishFileMetadataUpdated(context.Context, eventbus.FileMetadataUpdatedEvent) error
}

// BusPublisher adapts *eventbus.Bus to Publisher.
type BusPublisher struct{ Bus *eventbus.Bus }

func (p BusPublisher) PublishFileUploaded(ctx context.Context, evt eventbus.FileUploadedEvent) error {
	if p.Bus == nil {
		return nil
	}
	return p.Bus.PublishFileUploaded(ctx, evt)
}

func (p BusPublisher) PublishFileAction(ctx context.Context, evt eventbus.FileActionEvent) error {
	if p.Bus == nil {
		return nil
	}
	return p.Bus.PublishFileAction(ctx, evt)
}

func (p BusPublisher) PublishFileMetadataUpdated(ctx context.Context, evt eventbus.FileMetadataUpdatedEvent) error {
	if p.Bus == nil {
		return nil
	}
	return p.Bus.PublishFileMetadataUpdated(ctx, evt)
}

// DispatchSink is the subset of the bundled dispatcher the publisher needs.
type DispatchSink interface {
	PublishFileUploaded(context.Context, eventbus.FileUploadedEvent) error
	PublishFileAction(context.Context, eventbus.FileActionEvent) error
}

// DispatcherPublisher adapts the in-process bundled dispatcher to Publisher.
// Its metadata publish is a no-op: an in-process consumer does not need the
// cross-process event.
type DispatcherPublisher struct{ Dispatcher DispatchSink }

func (p DispatcherPublisher) PublishFileUploaded(ctx context.Context, evt eventbus.FileUploadedEvent) error {
	if p.Dispatcher == nil {
		return fmt.Errorf("dispatcher is not configured")
	}
	return p.Dispatcher.PublishFileUploaded(ctx, evt)
}

func (p DispatcherPublisher) PublishFileAction(ctx context.Context, evt eventbus.FileActionEvent) error {
	if p.Dispatcher == nil {
		return nil
	}
	return p.Dispatcher.PublishFileAction(ctx, evt)
}

func (p DispatcherPublisher) PublishFileMetadataUpdated(context.Context, eventbus.FileMetadataUpdatedEvent) error {
	return nil
}