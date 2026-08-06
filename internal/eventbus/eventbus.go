// Package eventbus is a thin wrapper over the fleet-wide shared event bus
// (src.solsynth.dev/sosys/go/pkg/eventbus), exposing the filesystem service's
// typed publish/subscribe API. Wire shapes mirror the shared Event envelope
// (event_id, timestamp, event_type, stream_name); see events.go.
//
// A nil *Bus (or one whose embedded shared.Bus is nil) is safe to call: every
// method no-ops, matching the fleet convention of running with events disabled.
package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	shared "src.solsynth.dev/sosys/go/pkg/eventbus"
)

// Bus wraps the shared event bus. The embedded *shared.Bus promotes Publish,
// PublishJetStream, EnsureStream and the NATS connection to callers.
type Bus struct {
	*shared.Bus
}

// New wraps a NATS connection in the shared event bus. Returns nil when the
// connection is nil (events disabled).
func New(conn *nats.Conn) *Bus {
	if conn == nil {
		return nil
	}
	b, err := shared.New(conn)
	if err != nil {
		return nil
	}
	return &Bus{Bus: b}
}

func (b *Bus) PublishFileUploaded(ctx context.Context, evt FileUploadedEvent) error {
	if b == nil || b.Bus == nil {
		return nil
	}
	if evt.EventID == "" {
		evt.EventID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
	}
	if evt.EventType == "" {
		evt.EventType = "filesystem.file.uploaded.v1"
	}
	if evt.StreamName == "" {
		evt.StreamName = "filesystem_events"
	}
	return b.PublishJetStream(ctx, evt.EventType, evt.StreamName, evt)
}

func (b *Bus) PublishFileAction(_ context.Context, evt FileActionEvent) error {
	if b == nil || b.Bus == nil {
		return nil
	}
	return b.Publish("file_action", evt)
}

func (b *Bus) PublishFileMetadataUpdated(ctx context.Context, evt FileMetadataUpdatedEvent) error {
	if b == nil || b.Bus == nil {
		return nil
	}
	return b.PublishJetStream(ctx, "filesystem.file.updated.v1", "filesystem_events", evt)
}

func (b *Bus) SubscribeFileUploaded(handler func(FileUploadedEvent) error) (*nats.Subscription, error) {
	if b == nil || b.Bus == nil {
		return nil, nil
	}
	const subject = "filesystem.file.uploaded.v1"
	const stream = "filesystem_events"
	if err := b.EnsureStream(context.Background(), stream, []string{subject}); err != nil {
		return nil, err
	}
	js, err := b.Conn.JetStream()
	if err != nil {
		return nil, err
	}
	sub, err := js.QueueSubscribeSync(
		subject,
		"dysonfs_file_processing",
		nats.BindStream(stream),
		nats.Durable("dysonfs_file_processing"),
		nats.ManualAck(),
		nats.AckWait(30*time.Minute),
		nats.DeliverNew(),
	)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			msg, nextErr := sub.NextMsg(time.Second)
			if nextErr != nil {
				if nextErr == nats.ErrTimeout {
					continue
				}
				return
			}
			var evt FileUploadedEvent
			if err := json.Unmarshal(msg.Data, &evt); err != nil {
				logging.Log.Error().Err(err).Str("subject", msg.Subject).Msg("failed to decode file uploaded event")
				_ = msg.Term()
				continue
			}
			if handler == nil || handler(evt) == nil {
				_ = msg.Ack()
				continue
			}
			logging.Log.Error().Str("subject", msg.Subject).Str("fileId", evt.FileID).Str("taskId", evt.TaskID).Msg("file uploaded handler failed")
			_ = msg.Nak()
		}
	}()
	return sub, nil
}

func (b *Bus) SubscribeFileAction(handler func(FileActionEvent) error) (*nats.Subscription, error) {
	if b == nil || b.Bus == nil {
		return nil, nil
	}
	return b.Conn.Subscribe("file_action", func(msg *nats.Msg) {
		var evt FileActionEvent
		if err := json.Unmarshal(msg.Data, &evt); err != nil {
			logging.Log.Error().Err(err).Str("subject", msg.Subject).Msg("failed to decode file action event")
			return
		}
		if handler != nil {
			if err := handler(evt); err != nil {
				logging.Log.Error().Err(err).Str("subject", msg.Subject).Str("fileId", evt.FileID).Str("action", evt.Action).Msg("file action handler failed")
			}
		}
	})
}
