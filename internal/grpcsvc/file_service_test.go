package grpcsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	gen "src.solsynth.dev/sosys/go/proto"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestToProtoCloudFileUsesFolderMimeType(t *testing.T) {
	file := &database.CloudFile{
		ID:       database.NewID(),
		Name:     "docs",
		IsFolder: true,
		Object: &database.FileObject{
			ID:       database.NewID(),
			MimeType: "application/octet-stream",
		},
	}

	resp := toProtoCloudFile(file)

	if resp.GetMimeType() != database.FolderMimeType {
		t.Fatalf("mime_type = %q, want %q", resp.GetMimeType(), database.FolderMimeType)
	}
	if resp.GetContentType() != database.FolderMimeType {
		t.Fatalf("content_type = %q, want %q", resp.GetContentType(), database.FolderMimeType)
	}
	if resp.GetObject().GetMimeType() != "application/octet-stream" {
		t.Fatalf("object.mime_type = %q, want %q", resp.GetObject().GetMimeType(), "application/octet-stream")
	}
}

func TestToProtoCloudFileExposesVideoDimensionsFromObjectMeta(t *testing.T) {
	meta := datatypes.JSON([]byte(`{"width":1920,"height":1080,"aspect_ratio":"16:9"}`))
	file := &database.CloudFile{
		ID:   database.NewID(),
		Name: "clip.mp4",
		Object: &database.FileObject{
			ID:       database.NewID(),
			MimeType: "video/mp4",
			Meta:     meta,
		},
	}

	resp := toProtoCloudFile(file)

	if resp.GetWidth() != 1920 {
		t.Fatalf("width = %d, want 1920", resp.GetWidth())
	}
	if resp.GetHeight() != 1080 {
		t.Fatalf("height = %d, want 1080", resp.GetHeight())
	}

	var decoded map[string]any
	if err := json.Unmarshal(resp.GetObject().GetMeta(), &decoded); err != nil {
		t.Fatalf("unmarshal object meta: %v", err)
	}
	if got, _ := decoded["aspect_ratio"].(string); got != "16:9" {
		t.Fatalf("aspect_ratio = %q, want %q", got, "16:9")
	}
}

type recordingUploadPublisher struct {
	events []eventbus.FileUploadedEvent
}

func (p *recordingUploadPublisher) PublishFileUploaded(_ context.Context, event eventbus.FileUploadedEvent) error {
	p.events = append(p.events, event)
	return nil
}

type uploadTestFixture struct {
	client    gen.DyFileServiceClient
	db        *gorm.DB
	files     *service.FileService
	publisher *recordingUploadPublisher
}

func newUploadTestFixture(t *testing.T) *uploadTestFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+database.NewID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(
		&database.FilePool{},
		&database.FileObject{},
		&database.CloudFile{},
		&database.FilePermission{},
		&database.QuotaRecord{},
		&database.PersistentTask{},
		&database.UploadChunk{},
	); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	tempDir := t.TempDir()
	files := service.NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(t.TempDir()))
	tasks := service.NewTaskService(&database.DB{DB: db})
	quota := service.NewQuotaService(&database.DB{DB: db})
	publisher := &recordingUploadPublisher{}
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	Register(server, &config.Config{Storage: config.StorageConfig{TempDir: tempDir}}, files, tasks, quota, publisher)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &uploadTestFixture{
		client:    gen.NewDyFileServiceClient(conn),
		db:        db,
		files:     files,
		publisher: publisher,
	}
}

func TestUploadFileStreamsAndPersistsForImpersonatedAccount(t *testing.T) {
	fixture := newUploadTestFixture(t)
	accountID := uuid.New()
	content := []byte("hello from grpc")
	stream, err := fixture.client.UploadFile(context.Background())
	if err != nil {
		t.Fatalf("UploadFile() error = %v", err)
	}
	if err := stream.Send(&gen.DyUploadFileRequest{Payload: &gen.DyUploadFileRequest_Options{
		Options: &gen.DyFileUploadOptions{
			AccountId:   accountID.String(),
			FileName:    "hello.txt",
			FileSize:    int64(len(content)),
			ContentType: "text/plain",
			Index:       true,
		},
	}}); err != nil {
		t.Fatalf("send options: %v", err)
	}
	for _, part := range [][]byte{content[:5], content[5:]} {
		if err := stream.Send(&gen.DyUploadFileRequest{Payload: &gen.DyUploadFileRequest_Data{Data: part}}); err != nil {
			t.Fatalf("send data: %v", err)
		}
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("CloseAndRecv() error = %v", err)
	}
	if response.GetId() == "" || response.GetSize() != int64(len(content)) || response.GetHash() == "" {
		t.Fatalf("response = %+v, want id, size, and hash", response)
	}

	var stored database.CloudFile
	if err := fixture.db.Preload("Object").First(&stored, "id = ?", response.GetId()).Error; err != nil {
		t.Fatalf("load stored file: %v", err)
	}
	if stored.AccountID != accountID || stored.Name != "hello.txt" || !stored.Indexed {
		t.Fatalf("stored file = %+v, want impersonated account metadata", stored)
	}
	if len(fixture.publisher.events) != 1 || fixture.publisher.events[0].FileID != stored.ID {
		t.Fatalf("events = %+v, want upload event for %s", fixture.publisher.events, stored.ID)
	}
}

func TestMultipartUploadAcceptsOutOfOrderIdempotentParts(t *testing.T) {
	fixture := newUploadTestFixture(t)
	accountID := uuid.New()
	content := []byte("hello world")
	created, err := fixture.client.CreateMultipartUpload(context.Background(), &gen.DyCreateMultipartUploadRequest{
		Options: &gen.DyFileUploadOptions{
			AccountId:   accountID.String(),
			FileName:    "multipart.txt",
			FileSize:    int64(len(content)),
			ContentType: "text/plain",
		},
		ChunkSize: 5,
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload() error = %v", err)
	}
	if created.GetPartsCount() != 3 || created.GetChunkSize() != 5 {
		t.Fatalf("create response = %+v, want three five-byte parts", created)
	}

	parts := map[int32][]byte{0: content[:5], 1: content[5:10], 2: content[10:]}
	for _, index := range []int32{1, 0, 1, 2} {
		stream, err := fixture.client.UploadMultipartPart(context.Background())
		if err != nil {
			t.Fatalf("UploadMultipartPart() error = %v", err)
		}
		if err := stream.Send(&gen.DyUploadMultipartPartRequest{Payload: &gen.DyUploadMultipartPartRequest_Header{
			Header: &gen.DyMultipartPartHeader{UploadId: created.GetUploadId(), PartIndex: index},
		}}); err != nil {
			t.Fatalf("send part header: %v", err)
		}
		if err := stream.Send(&gen.DyUploadMultipartPartRequest{Payload: &gen.DyUploadMultipartPartRequest_Data{Data: parts[index]}}); err != nil {
			t.Fatalf("send part data: %v", err)
		}
		partResponse, err := stream.CloseAndRecv()
		if err != nil {
			t.Fatalf("close part %d: %v", index, err)
		}
		if partResponse.GetBytesReceived() != int64(len(parts[index])) {
			t.Fatalf("part %d bytes_received = %d, want %d", index, partResponse.GetBytesReceived(), len(parts[index]))
		}
	}

	response, err := fixture.client.CompleteMultipartUpload(context.Background(), &gen.DyCompleteMultipartUploadRequest{UploadId: created.GetUploadId()})
	if err != nil {
		t.Fatalf("CompleteMultipartUpload() error = %v", err)
	}
	if response.GetContentType() != "text/plain" {
		t.Fatalf("content_type = %q, want %q", response.GetContentType(), "text/plain")
	}
	file, err := fixture.files.GetFile(response.GetId())
	if err != nil {
		t.Fatalf("GetFile() error = %v", err)
	}
	reader, _, err := fixture.files.Storage().Get(context.Background(), *file.Object.StorageKey)
	if err != nil {
		t.Fatalf("storage.Get() error = %v", err)
	}
	defer reader.Close()
	storedContent, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stored content: %v", err)
	}
	if !bytes.Equal(storedContent, content) {
		t.Fatalf("stored content = %q, want %q", storedContent, content)
	}
	var task database.PersistentTask
	if err := fixture.db.First(&task, "task_id = ?", created.GetUploadId()).Error; err != nil {
		t.Fatalf("load task: %v", err)
	}
	if task.ChunksUploaded != 3 || task.Status != "completed" {
		t.Fatalf("task = %+v, want three unique parts and completed status", task)
	}
}

func TestUploadFileRejectsDataBeforeOptions(t *testing.T) {
	fixture := newUploadTestFixture(t)
	stream, err := fixture.client.UploadFile(context.Background())
	if err != nil {
		t.Fatalf("UploadFile() error = %v", err)
	}
	if err := stream.Send(&gen.DyUploadFileRequest{Payload: &gen.DyUploadFileRequest_Data{Data: []byte("bad")}}); err != nil {
		t.Fatalf("send data: %v", err)
	}
	_, err = stream.CloseAndRecv()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status code = %s, want %s (error: %v)", status.Code(err), codes.InvalidArgument, err)
	}
}

func TestCreateMultipartUploadRejectsOverwriteOwnedByAnotherAccount(t *testing.T) {
	fixture := newUploadTestFixture(t)
	ownerID := uuid.New()
	target := database.CloudFile{
		ID:        database.NewID(),
		Name:      "private.txt",
		AccountID: ownerID,
		Indexed:   true,
	}
	if err := fixture.db.Create(&target).Error; err != nil {
		t.Fatalf("create overwrite target: %v", err)
	}
	targetID := target.ID
	_, err := fixture.client.CreateMultipartUpload(context.Background(), &gen.DyCreateMultipartUploadRequest{
		Options: &gen.DyFileUploadOptions{
			AccountId:   uuid.NewString(),
			FileName:    "replacement.txt",
			FileSize:    5,
			ContentType: "text/plain",
			OverwriteId: &targetID,
		},
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status code = %s, want %s (error: %v)", status.Code(err), codes.PermissionDenied, err)
	}
}
