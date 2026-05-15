package eventbus

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"

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
	return b.PublishJSON("file_uploaded", evt)
}

func (b *Bus) SubscribeFileUploaded(handler func(FileUploadedEvent) error) (*nats.Subscription, error) {
	if b == nil || b.Conn == nil {
		return nil, nil
	}
	return b.Conn.Subscribe("file_uploaded", func(msg *nats.Msg) {
		var evt FileUploadedEvent
		if err := json.Unmarshal(msg.Data, &evt); err != nil {
			return
		}
		if handler != nil {
			_ = handler(evt)
		}
	})
}
