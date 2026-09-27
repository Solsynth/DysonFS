package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"gorm.io/datatypes"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

// TestReanalyzedMetadataPublishedToFleet drives the full background chain over
// a real NATS JetStream server: a client-flagged object is reanalyzed by the
// worker and the corrected metadata is published on
// filesystem.file.updated.v1 for the fleet. Skipped when the nats-server
// binary is unavailable.
func TestReanalyzedMetadataPublishedToFleet(t *testing.T) {
	if _, err := exec.LookPath("nats-server"); err != nil {
		t.Skip("nats-server binary not available")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	cmd := exec.Command("nats-server", "-js", "-sd", t.TempDir(), "-a", "127.0.0.1", "-p", fmt.Sprint(port))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start nats-server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	url := fmt.Sprintf("nats://127.0.0.1:%d", port)
	var conn *nats.Conn
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err = nats.Connect(url)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if conn == nil {
		t.Fatalf("nats not reachable: %v", err)
	}
	defer conn.Close()

	bus := eventbus.New(conn)
	if bus == nil {
		t.Fatal("eventbus.New returned nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bus.EnsureStream(ctx, "filesystem_events", []string{"filesystem.file.updated.v1"}); err != nil {
		t.Fatalf("ensure stream: %v", err)
	}

	received := make(chan eventbus.FileMetadataUpdatedEvent, 8)
	go func() {
		_ = bus.Consume(ctx, "filesystem_events", "filesystem.file.updated.v1", "reanalysis_event_test", func(data []byte) error {
			var evt eventbus.FileMetadataUpdatedEvent
			if err := json.Unmarshal(data, &evt); err != nil {
				return err
			}
			select {
			case received <- evt:
			default:
			}
			return nil
		})
	}()
	time.Sleep(750 * time.Millisecond)

	tmp := t.TempDir()
	db := openWorkerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
	stor := storage.NewLocalBackend(tmp)
	svc := service.NewFileService(&database.DB{DB: db}, stor)

	imgPath := filepath.Join(tmp, "source.png")
	encoded, err := os.Create(imgPath)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	if err := png.Encode(encoded, image.NewRGBA(image.Rect(0, 0, 4, 5))); err != nil {
		t.Fatalf("encode image: %v", err)
	}
	_ = encoded.Close()
	payload, err := os.ReadFile(imgPath)
	if err != nil {
		t.Fatalf("read image: %v", err)
	}

	objectID := database.NewID()
	if err := stor.Put(context.Background(), objectID, bytes.NewReader(payload), int64(len(payload)), "image/png"); err != nil {
		t.Fatalf("put object: %v", err)
	}
	if err := db.Create(&database.FileObject{
		ID: objectID, Size: int64(len(payload)), MimeType: "image/png", StorageKey: &objectID,
		Meta:            datatypes.JSON([]byte(`{"analysis_source":"client","width":1920,"height":1080,"duration_ms":83420}`)),
		NeedsReanalysis: true,
	}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	fileID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "photo.png", AccountID: uuid.New(), ObjectID: &objectID, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := db.Model(&database.FileObject{}).Where("id = ?", objectID).Updates(map[string]any{"updated_at": time.Now().Add(-time.Hour)}).Error; err != nil {
		t.Fatalf("age entry: %v", err)
	}

	w := New(bus, svc, stor, &database.DB{DB: db}, tmp)
	w.processReanalysisQueue(context.Background())

	select {
	case evt := <-received:
		t.Logf("event: file_id=%s status=%d file_meta=%v", evt.FileID, evt.Status, evt.File.FileMeta)
		if evt.FileID != fileID {
			t.Fatalf("event file_id = %q, want %q", evt.FileID, fileID)
		}
		if evt.File.FileMeta["analysis_source"] != "server" {
			t.Fatalf("file_meta analysis_source = %v, want server", evt.File.FileMeta["analysis_source"])
		}
		if evt.File.FileMeta["width"] != float64(4) || evt.File.FileMeta["height"] != float64(5) {
			t.Fatalf("file_meta dimensions = %v x %v, want server-measured 4 x 5", evt.File.FileMeta["width"], evt.File.FileMeta["height"])
		}
		if _, ok := evt.File.FileMeta["duration_ms"]; ok {
			t.Fatalf("client-only duration_ms leaked into the fleet event: %v", evt.File.FileMeta)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no filesystem.file.updated.v1 event was published for the reanalysis")
	}

	var object database.FileObject
	if err := db.First(&object, "id = ?", objectID).Error; err != nil {
		t.Fatalf("reload object: %v", err)
	}
	if object.NeedsReanalysis {
		t.Fatal("queue entry not cleared after publishing")
	}
}
