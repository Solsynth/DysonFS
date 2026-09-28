package eventbus

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	shared "src.solsynth.dev/sosys/go/pkg/eventbus"
)

// The fleet envelope pins event_id to a UUID: every .NET consumer
// (DysonNetwork.Shared.EventBus.EventBase) deserializes it into a System.Guid
// and aborts the whole event with a JsonException on any other id shape, so a
// ULID or a nanosecond counter here silently kills fleet delivery.
func TestFileMetadataUpdatedEventEnvelopeUsesUUIDEventID(t *testing.T) {
	evt := NewFileMetadataUpdatedEvent("01JFILEULID", "01JTASKULID", "account-1", 2, FileMetadataSnapshot{ID: "01JFILEULID"})

	if _, err := uuid.Parse(evt.EventID); err != nil {
		t.Fatalf("event_id %q is not a UUID: %v", evt.EventID, err)
	}
	if evt.Timestamp.IsZero() {
		t.Fatal("metadata update envelope carries no timestamp")
	}
	if evt.EventType != "filesystem.file.updated.v1" || evt.StreamName != "filesystem_events" {
		t.Fatalf("envelope subject/stream = %q/%q, want filesystem.file.updated.v1/filesystem_events", evt.EventType, evt.StreamName)
	}

	var wire struct {
		EventID   string    `json:"event_id"`
		Timestamp time.Time `json:"timestamp"`
		EventType string    `json:"event_type"`
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal metadata update: %v", err)
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal metadata update wire payload: %v", err)
	}
	if _, err := uuid.Parse(wire.EventID); err != nil {
		t.Fatalf("wire event_id %q is not a UUID: %v", wire.EventID, err)
	}
	if wire.EventType != "filesystem.file.updated.v1" {
		t.Fatalf("wire event_type = %q", wire.EventType)
	}
}

func TestUploadEnvelopeFillsUUIDEventID(t *testing.T) {
	env := uploadEnvelope(shared.Event{}, "filesystem.file.uploaded.v1")
	if _, err := uuid.Parse(env.EventID); err != nil {
		t.Fatalf("event_id %q is not a UUID: %v", env.EventID, err)
	}
	if env.Timestamp.IsZero() {
		t.Fatal("upload envelope carries no timestamp")
	}
	if env.EventType != "filesystem.file.uploaded.v1" || env.StreamName != "filesystem_events" {
		t.Fatalf("upload envelope subject/stream = %q/%q", env.EventType, env.StreamName)
	}

	stamp := time.Date(2026, 7, 31, 23, 0, 0, 0, time.UTC)
	const id = "11111111-1111-1111-1111-111111111111"
	kept := uploadEnvelope(shared.Event{EventID: id, Timestamp: stamp, EventType: "custom.v1", StreamName: "custom_stream"}, "filesystem.file.uploaded.v1")
	if kept.EventID != id || !kept.Timestamp.Equal(stamp) || kept.EventType != "custom.v1" || kept.StreamName != "custom_stream" {
		t.Fatalf("pre-set envelope fields were overwritten: %+v", kept)
	}

	evt := FileUploadedEvent{Event: env, FileID: "01JFILEULID"}
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal uploaded event: %v", err)
	}
	var wire struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal uploaded wire payload: %v", err)
	}
	if _, err := uuid.Parse(wire.EventID); err != nil {
		t.Fatalf("wire event_id %q is not a UUID: %v", wire.EventID, err)
	}
}
