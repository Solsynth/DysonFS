package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
)

type Bus struct{ Conn *nats.Conn }

func New(conn *nats.Conn) *Bus { return &Bus{Conn: conn} }

func (b *Bus) PublishJSON(subject string, v any) error {
	if b == nil || b.Conn == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return b.Conn.Publish(subject, data)
}

func (b *Bus) PublishFileUploaded(_ context.Context, evt FileUploadedEvent) error {
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
	return b.PublishJetStreamJSON(evt.EventType, evt.StreamName, evt)
}

func (b *Bus) PublishFileAction(_ context.Context, evt FileActionEvent) error {
	return b.PublishJSON("file_action", evt)
}

func (b *Bus) PublishFileMetadataUpdated(_ context.Context, evt FileMetadataUpdatedEvent) error {
	return b.PublishJetStreamJSON("filesystem.file.updated.v1", "filesystem_events", evt)
}

func (b *Bus) PublishJetStreamJSON(subject, stream string, v any) error {
	if b == nil || b.Conn == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	js, err := b.Conn.JetStream()
	if err != nil {
		return fmt.Errorf("create jetstream context: %w", err)
	}
	if info, err := js.StreamInfo(stream); err != nil {
		if _, addErr := js.AddStream(&nats.StreamConfig{Name: stream, Subjects: []string{subject}}); addErr != nil {
			if _, retryErr := js.StreamInfo(stream); retryErr != nil {
				return fmt.Errorf("ensure stream %s: %w", stream, addErr)
			}
		}
	} else {
		covered := false
		for _, existing := range info.Config.Subjects {
			if existing == subject {
				covered = true
				break
			}
		}
		if !covered {
			subjects := append([]string{}, info.Config.Subjects...)
			subjects = append(subjects, subject)
			if _, err := js.UpdateStream(&nats.StreamConfig{Name: stream, Subjects: subjects}); err != nil {
				return fmt.Errorf("add subject %s to stream %s: %w", subject, stream, err)
			}
		}
	}
	if _, err := js.Publish(subject, data); err != nil {
		return fmt.Errorf("publish %s to %s: %w", subject, stream, err)
	}
	return nil
}

func (b *Bus) SubscribeFileUploaded(handler func(FileUploadedEvent) error) (*nats.Subscription, error) {
	if b == nil || b.Conn == nil {
		return nil, nil
	}
	const subject = "filesystem.file.uploaded.v1"
	const stream = "filesystem_events"
	if err := b.ensureStream(stream, subject); err != nil {
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

func (b *Bus) ensureStream(stream, subject string) error {
	js, err := b.Conn.JetStream()
	if err != nil {
		return err
	}
	if info, err := js.StreamInfo(stream); err == nil {
		for _, existing := range info.Config.Subjects {
			if existing == subject {
				return nil
			}
		}
		subjects := append(append([]string{}, info.Config.Subjects...), subject)
		_, err = js.UpdateStream(&nats.StreamConfig{Name: stream, Subjects: subjects})
		return err
	}
	_, err = js.AddStream(&nats.StreamConfig{Name: stream, Subjects: []string{subject}})
	if err != nil {
		_, retryErr := js.StreamInfo(stream)
		return retryErr
	}
	return nil
}

func (b *Bus) SubscribeFileAction(handler func(FileActionEvent) error) (*nats.Subscription, error) {
	if b == nil || b.Conn == nil {
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
