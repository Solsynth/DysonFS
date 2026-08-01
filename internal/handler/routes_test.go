package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/s3server"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	dyauth "src.solsynth.dev/sosys/go/pkg/auth"
	gen "src.solsynth.dev/sosys/go/proto"
)

type recordingDispatcher struct {
	uploaded []eventbus.FileUploadedEvent
}

type permissionCheckerFunc func(context.Context, string, string) (bool, error)

func (f permissionCheckerFunc) HasPermission(ctx context.Context, accountID, key string) (bool, error) {
	return f(ctx, accountID, key)
}

func (d *recordingDispatcher) PublishFileUploaded(_ context.Context, evt eventbus.FileUploadedEvent) error {
	d.uploaded = append(d.uploaded, evt)
	return nil
}

func (d *recordingDispatcher) PublishFileAction(context.Context, eventbus.FileActionEvent) error {
	return nil
}

func newTestWOPIService(t *testing.T, files *service.FileService) *service.WOPIService {
	t.Helper()
	discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hosting/discovery" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<wopi-discovery>
  <net-zone name="external-http">
    <app name="writer">
      <action ext="txt" name="view" urlsrc="https://collabora.example/browser/view?" />
      <action ext="txt" name="edit" urlsrc="https://collabora.example/browser/edit?" />
    </app>
  </net-zone>
</wopi-discovery>`))
	}))
	t.Cleanup(discovery.Close)
	files.SetAccessSecret("test-secret")
	wopi, err := service.NewWOPIService(config.WOPIConfig{
		Enabled:      true,
		PublicURL:    "https://fs.example.test",
		CollaboraURL: discovery.URL,
		TokenTTL:     15 * time.Minute,
	}, files)
	if err != nil {
		t.Fatalf("NewWOPIService() error = %v", err)
	}
	return wopi
}

func TestRegisterRoutesNoPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	files := service.NewFileService(&database.DB{}, nil)
	tasks := service.NewTaskService(&database.DB{})
	quota := service.NewQuotaService(&database.DB{})

	defer func() {
		if recover() != nil {
			t.Fatal("RegisterRoutes() panicked")
		}
	}()

	RegisterRoutes(r, &config.Config{}, files, nil, tasks, quota, (*eventbus.Bus)(nil), nil)
}

func TestDirectUploadReturnsSynchronousSourceMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.QuotaRecord{})
	tempDir := t.TempDir()
	files := service.NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(t.TempDir()))
	tasks := service.NewTaskService(&database.DB{DB: db})
	quota := service.NewQuotaService(&database.DB{DB: db})
	accountID := uuid.New()
	dispatcher := &recordingDispatcher{}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{Storage: config.StorageConfig{TempDir: tempDir}}, files, nil, tasks, quota, nil, dispatcher)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	imageData := bytes.Buffer{}
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 3))
	canvas.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&imageData, canvas); err != nil {
		t.Fatalf("encode image: %v", err)
	}
	part, err := writer.CreateFormFile("file", "note.png")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(imageData.Bytes()); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/files/upload/direct", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var response struct {
		ID     string `json:"id"`
		Size   int64  `json:"size"`
		Hash   string `json:"hash"`
		Object struct {
			Meta map[string]any `json:"meta"`
		} `json:"object"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if response.ID == "" || response.Size != int64(imageData.Len()) || response.Hash == "" {
		t.Fatalf("response = %+v, want file id, size, and hash", response)
	}
	if response.Object.Meta == nil {
		t.Fatalf("response object metadata is nil")
	}
	if response.Object.Meta["width"] != float64(2) || response.Object.Meta["height"] != float64(3) || response.Object.Meta["blurhash"] == "" {
		t.Fatalf("response object metadata = %#v, want synchronous image dimensions and blurhash", response.Object.Meta)
	}
	if len(dispatcher.uploaded) != 1 || dispatcher.uploaded[0].FileID != response.ID {
		t.Fatalf("uploaded events = %+v, want one event for %s", dispatcher.uploaded, response.ID)
	}
}

func TestDirectUploadRequiresFilesUploadPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.QuotaRecord{})
	files := service.NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(t.TempDir()))
	accountID := uuid.New()
	files.SetPermissionChecker(permissionCheckerFunc(func(_ context.Context, gotAccountID, key string) (bool, error) {
		if gotAccountID != accountID.String() {
			t.Fatalf("permission account ID = %q, want %q", gotAccountID, accountID)
		}
		if key != service.PermissionFilesUpload {
			t.Fatalf("permission key = %q, want %q", key, service.PermissionFilesUpload)
		}
		return false, nil
	}))

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{Storage: config.StorageConfig{TempDir: t.TempDir()}}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/files/upload/direct", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestAdminStorageConfigRequiresFilesManagePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.StorageNode{})
	files := service.NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(t.TempDir()))
	accountID := uuid.New()
	files.SetPermissionChecker(permissionCheckerFunc(func(_ context.Context, gotAccountID, key string) (bool, error) {
		if gotAccountID != accountID.String() {
			t.Fatalf("permission account ID = %q, want %q", gotAccountID, accountID)
		}
		if key != service.PermissionFilesManage {
			t.Fatalf("permission key = %q, want %q", key, service.PermissionFilesManage)
		}
		return false, nil
	}))

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/storage/config", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestOpenFileFallsBackToLegacyThumbnailStorageKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	tmp := t.TempDir()
	stor := storage.NewLocalBackend(tmp)
	files := service.NewFileService(&database.DB{DB: db}, stor)

	objectID := database.NewID()
	fileID := database.NewID()
	legacyKey := fileID + ".thumbnail"
	if err := db.Create(&database.FileObject{ID: objectID, Size: 12, MimeType: "image/png", Hash: "hash", StorageKey: &objectID, Meta: datatypes.JSON([]byte(`{}`)), HasThumbnail: true}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "sample.png", AccountID: uuid.New(), ObjectID: &objectID, StorageKey: &objectID, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := stor.Put(context.Background(), legacyKey, strings.NewReader("thumb"), int64(len("thumb")), "image/jpeg"); err != nil {
		t.Fatalf("put legacy thumbnail: %v", err)
	}

	r := gin.New()
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?thumbnail=1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusTemporaryRedirect)
	}
	if location := w.Header().Get("Location"); !strings.Contains(location, legacyKey) {
		t.Fatalf("location = %q, want it to contain %q", location, legacyKey)
	}
}

func TestOpenFileFallsBackToLegacyCompressionStorageKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	tmp := t.TempDir()
	stor := storage.NewLocalBackend(tmp)
	files := service.NewFileService(&database.DB{DB: db}, stor)

	objectID := database.NewID()
	fileID := database.NewID()
	legacyKey := fileID + ".compressed"
	if err := db.Create(&database.FileObject{ID: objectID, Size: 12, MimeType: "image/png", Hash: "hash", StorageKey: &objectID, Meta: datatypes.JSON([]byte(`{}`)), HasCompression: true}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "sample.png", AccountID: uuid.New(), ObjectID: &objectID, StorageKey: &objectID, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := stor.Put(context.Background(), legacyKey, strings.NewReader("compressed"), int64(len("compressed")), "image/webp"); err != nil {
		t.Fatalf("put legacy compressed: %v", err)
	}

	r := gin.New()
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusTemporaryRedirect)
	}
	if location := w.Header().Get("Location"); !strings.Contains(location, legacyKey) {
		t.Fatalf("location = %q, want it to contain %q", location, legacyKey)
	}
}

func TestOpenFileNormalizesDerivedCompressionStorageKeyFromObjectID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	tmp := t.TempDir()
	stor := storage.NewLocalBackend(tmp)
	files := service.NewFileService(&database.DB{DB: db}, stor)

	parentObjectID := database.NewID()
	parentFileID := database.NewID()
	derivedObjectID := database.NewID()
	wrongKey := derivedObjectID + ".compressed"
	legacyKey := parentFileID + ".compressed"
	appType := "system.compression.low"

	if err := db.Create(&database.FileObject{ID: parentObjectID, Size: 12, MimeType: "image/png", Hash: "hash", StorageKey: &parentObjectID, Meta: datatypes.JSON([]byte(`{}`)), HasCompression: true}).Error; err != nil {
		t.Fatalf("create parent object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: parentFileID, Name: "sample.png", AccountID: uuid.New(), ObjectID: &parentObjectID, StorageKey: &parentObjectID, Indexed: true}).Error; err != nil {
		t.Fatalf("create parent file: %v", err)
	}
	if err := db.Create(&database.FileObject{ID: derivedObjectID, Size: 8, MimeType: "image/webp", Hash: "derived-hash", StorageKey: &wrongKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create derived object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "sample.png", AccountID: uuid.New(), ObjectID: &derivedObjectID, ParentID: &parentFileID, StorageKey: &wrongKey, ApplicationType: &appType, Indexed: false}).Error; err != nil {
		t.Fatalf("create derived file: %v", err)
	}
	if err := stor.Put(context.Background(), legacyKey, strings.NewReader("compressed"), int64(len("compressed")), "image/webp"); err != nil {
		t.Fatalf("put legacy compressed: %v", err)
	}

	r := gin.New()
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+parentFileID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusTemporaryRedirect)
	}
	location := w.Header().Get("Location")
	if !strings.Contains(location, legacyKey) {
		t.Fatalf("location = %q, want it to contain %q", location, legacyKey)
	}
	if strings.Contains(location, wrongKey) {
		t.Fatalf("location = %q, should not contain wrong key %q", location, wrongKey)
	}
}

func TestOpenFileFallsBackToOriginalWhenDerivedCompressionIsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	tmp := t.TempDir()
	stor := storage.NewLocalBackend(tmp)
	files := service.NewFileService(&database.DB{DB: db}, stor)

	parentObjectID := database.NewID()
	parentFileID := database.NewID()
	derivedObjectID := database.NewID()
	parentKey := parentFileID
	missingDerivedKey := derivedObjectID + ".compressed"
	appType := "system.compression.low"

	if err := db.Create(&database.FileObject{ID: parentObjectID, Size: 12, MimeType: "image/png", Hash: "hash", StorageKey: &parentKey, Meta: datatypes.JSON([]byte(`{}`)), HasCompression: true}).Error; err != nil {
		t.Fatalf("create parent object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: parentFileID, Name: "sample.png", AccountID: uuid.New(), ObjectID: &parentObjectID, StorageKey: &parentKey, Indexed: true}).Error; err != nil {
		t.Fatalf("create parent file: %v", err)
	}
	if err := db.Create(&database.FileObject{ID: derivedObjectID, Size: 8, MimeType: "image/webp", Hash: "derived-hash", StorageKey: &missingDerivedKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create derived object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "sample.png", AccountID: uuid.New(), ObjectID: &derivedObjectID, ParentID: &parentFileID, StorageKey: &missingDerivedKey, ApplicationType: &appType, Indexed: false}).Error; err != nil {
		t.Fatalf("create derived file: %v", err)
	}
	if err := stor.Put(context.Background(), parentKey, strings.NewReader("original"), int64(len("original")), "image/png"); err != nil {
		t.Fatalf("put original object: %v", err)
	}

	r := gin.New()
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+parentFileID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusTemporaryRedirect)
	}
	location := w.Header().Get("Location")
	if !strings.Contains(location, parentKey) {
		t.Fatalf("location = %q, want it to contain original key %q", location, parentKey)
	}
	if strings.Contains(location, missingDerivedKey) {
		t.Fatalf("location = %q, should not contain missing key %q", location, missingDerivedKey)
	}
}

func TestListRootOwnedFiltersByUsageAndApplicationType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()
	usageAvatar := "avatar"
	usageBackup := "backup"
	appImage := "image/png"
	appText := "text/plain"

	items := []database.CloudFile{
		{ID: database.NewID(), Name: "avatar.png", AccountID: accountID, Indexed: true, Usage: &usageAvatar, ApplicationType: &appImage},
		{ID: database.NewID(), Name: "notes.txt", AccountID: accountID, Indexed: true, Usage: &usageAvatar, ApplicationType: &appText},
		{ID: database.NewID(), Name: "archive.png", AccountID: accountID, Indexed: true, Usage: &usageBackup, ApplicationType: &appImage},
	}
	for _, item := range items {
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create file: %v", err)
		}
	}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/me?usage=avatar&application_type=image/png", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if total := w.Header().Get("X-Total"); total != "1" {
		t.Fatalf("X-Total = %q, want %q", total, "1")
	}
	var got []database.CloudFile
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(got))
	}
	if got[0].Name != "avatar.png" {
		t.Fatalf("got file %q, want %q", got[0].Name, "avatar.png")
	}
}

func TestListUnindexedFiltersByUsageAndApplicationType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()
	usageImport := "import"
	usageExport := "export"
	appZip := "application/zip"
	appJSON := "application/json"

	items := []database.CloudFile{
		{ID: database.NewID(), Name: "import.zip", AccountID: accountID, Indexed: false, Usage: &usageImport, ApplicationType: &appZip},
		{ID: database.NewID(), Name: "import.json", AccountID: accountID, Indexed: false, Usage: &usageImport, ApplicationType: &appJSON},
		{ID: database.NewID(), Name: "export.zip", AccountID: accountID, Indexed: false, Usage: &usageExport, ApplicationType: &appZip},
	}
	for _, item := range items {
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create file: %v", err)
		}
	}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/unindexed?usage=import&application_type=application/zip", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if total := w.Header().Get("X-Total"); total != "1" {
		t.Fatalf("X-Total = %q, want %q", total, "1")
	}
	var got []database.CloudFile
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(got))
	}
	if got[0].Name != "import.zip" {
		t.Fatalf("got file %q, want %q", got[0].Name, "import.zip")
	}
}

func TestListRootOwnedFiltersByContentTypeAndExtendedFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()
	poolID := database.NewID()
	usageAvatar := "avatar"
	appImage := "image/png"
	now := time.Date(2026, time.May, 29, 12, 0, 0, 0, time.UTC)

	object1 := database.NewID()
	object2 := database.NewID()
	object3 := database.NewID()
	items := []database.FileObject{
		{ID: object1, Size: 128, MimeType: "image/png", Hash: "hash-1", Meta: datatypes.JSON([]byte(`{}`)), HasThumbnail: true, HasCompression: true},
		{ID: object2, Size: 96, MimeType: "image/png", Hash: "hash-2", Meta: datatypes.JSON([]byte(`{}`)), HasThumbnail: false, HasCompression: true},
		{ID: object3, Size: 128, MimeType: "text/plain", Hash: "hash-3", Meta: datatypes.JSON([]byte(`{}`)), HasThumbnail: true, HasCompression: true},
	}
	for _, item := range items {
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create object: %v", err)
		}
	}

	filesToCreate := []database.CloudFile{
		{
			ID:              database.NewID(),
			Name:            "avatar.png",
			AccountID:       accountID,
			PoolID:          &poolID,
			ObjectID:        &object1,
			Indexed:         true,
			Usage:           &usageAvatar,
			ApplicationType: &appImage,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              database.NewID(),
			Name:            "avatar-copy.png",
			AccountID:       accountID,
			PoolID:          &poolID,
			ObjectID:        &object2,
			Indexed:         true,
			Usage:           &usageAvatar,
			ApplicationType: &appImage,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              database.NewID(),
			Name:            "avatar.txt",
			AccountID:       accountID,
			PoolID:          &poolID,
			ObjectID:        &object3,
			Indexed:         true,
			Usage:           &usageAvatar,
			ApplicationType: &appImage,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	}
	for _, item := range filesToCreate {
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create file: %v", err)
		}
	}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/me?content_type=image/png&extension=png&pool_id="+poolID+"&has_thumbnail=1&has_compression=1&min_size=120&max_size=140&created_after=2026-05-28T00:00:00Z&updated_before=2026-05-30T00:00:00Z", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if total := w.Header().Get("X-Total"); total != "1" {
		t.Fatalf("X-Total = %q, want %q", total, "1")
	}
	var got []database.CloudFile
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(got))
	}
	if got[0].Name != "avatar.png" {
		t.Fatalf("got file %q, want %q", got[0].Name, "avatar.png")
	}
}

func TestListUnindexedFiltersByMimeTypeAliasAndFlags(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()
	poolID := database.NewID()
	usageImport := "import"
	appZip := "application/zip"
	recycled := true

	object1 := database.NewID()
	object2 := database.NewID()
	objects := []database.FileObject{
		{ID: object1, Size: 256, MimeType: "application/zip", Hash: "zip-1", Meta: datatypes.JSON([]byte(`{}`)), HasCompression: true},
		{ID: object2, Size: 64, MimeType: "application/json", Hash: "json-1", Meta: datatypes.JSON([]byte(`{}`)), HasCompression: false},
	}
	for _, item := range objects {
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create object: %v", err)
		}
	}

	filesToCreate := []database.CloudFile{
		{ID: database.NewID(), Name: "import.zip", AccountID: accountID, PoolID: &poolID, ObjectID: &object1, Indexed: false, IsMarkedRecycle: recycled, Usage: &usageImport, ApplicationType: &appZip},
		{ID: database.NewID(), Name: "import.json", AccountID: accountID, PoolID: &poolID, ObjectID: &object2, Indexed: false, IsMarkedRecycle: recycled, Usage: &usageImport, ApplicationType: &appZip},
	}
	for _, item := range filesToCreate {
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create file: %v", err)
		}
	}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/unindexed?mime_type=application/zip&pool="+poolID+"&recycled=1&indexed=0&has_compression=1&extension=zip&min_size=200", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if total := w.Header().Get("X-Total"); total != "1" {
		t.Fatalf("X-Total = %q, want %q", total, "1")
	}
	var got []database.CloudFile
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(got))
	}
	if got[0].Name != "import.zip" {
		t.Fatalf("got file %q, want %q", got[0].Name, "import.zip")
	}
}

func TestFileBreadcrumbReturnsRootToCurrent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()

	root := database.CloudFile{ID: database.NewID(), Name: "root", AccountID: accountID, Indexed: true, IsFolder: true}
	folder := database.CloudFile{ID: database.NewID(), Name: "folder", AccountID: accountID, Indexed: true, IsFolder: true, ParentID: &root.ID}
	file := database.CloudFile{ID: database.NewID(), Name: "notes.txt", AccountID: accountID, Indexed: true, ParentID: &folder.ID}
	for _, item := range []database.CloudFile{root, folder, file} {
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create file tree: %v", err)
		}
	}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+file.ID+"/breadcrumb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got []breadcrumbItem
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(got))
	}
	if got[0].ID != root.ID || got[1].ID != folder.ID || got[2].ID != file.ID {
		t.Fatalf("breadcrumb order = %+v", got)
	}
	if !got[0].IsFolder || !got[1].IsFolder || got[2].IsFolder {
		t.Fatalf("unexpected folder flags: %+v", got)
	}
}

func TestFileBreadcrumbRequiresReadAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, nil)

	file := database.CloudFile{ID: database.NewID(), Name: "private.txt", AccountID: uuid.New(), Indexed: true}
	if err := db.Create(&file).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	perm := database.FilePermission{ID: database.NewID(), FileID: file.ID, SubjectType: "account", SubjectID: uuid.New().String(), Permission: "read"}
	if err := db.Create(&perm).Error; err != nil {
		t.Fatalf("create permission: %v", err)
	}

	r := gin.New()
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+file.ID+"/breadcrumb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestListFilesMetadataPreservesRequestedOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()
	first := database.CloudFile{ID: database.NewID(), Name: "first.txt", AccountID: accountID, Indexed: true}
	second := database.CloudFile{ID: database.NewID(), Name: "second.txt", AccountID: accountID, Indexed: true}
	for _, file := range []database.CloudFile{first, second} {
		if err := db.Create(&file).Error; err != nil {
			t.Fatalf("create file: %v", err)
		}
	}

	r := gin.New()
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/meta?ids="+second.ID+","+first.ID+"&ids=missing", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	if total := w.Header().Get("X-Total"); total != "2" {
		t.Fatalf("X-Total = %q, want 2", total)
	}
	var got []database.CloudFile
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 2 || got[0].ID != second.ID || got[1].ID != first.ID {
		t.Fatalf("metadata order = %+v, want [%s %s]", got, second.ID, first.ID)
	}
}

func TestListFilesMetadataRequiresIDsAndFiltersUnreadableFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	ownerID := uuid.New()
	viewerID := uuid.New()
	public := database.CloudFile{ID: database.NewID(), Name: "public.txt", AccountID: ownerID, Indexed: true}
	private := database.CloudFile{ID: database.NewID(), Name: "private.txt", AccountID: ownerID, Indexed: true}
	for _, file := range []database.CloudFile{public, private} {
		if err := db.Create(&file).Error; err != nil {
			t.Fatalf("create file: %v", err)
		}
	}
	if err := db.Create(&database.FilePermission{ID: database.NewID(), FileID: private.ID, SubjectType: "account", SubjectID: ownerID.String(), Permission: "read"}).Error; err != nil {
		t.Fatalf("create permission: %v", err)
	}

	r := gin.New()
	r.Use(testAuthMiddleware(viewerID))
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	missingReq := httptest.NewRequest(http.MethodGet, "/api/files/meta", nil)
	missingRes := httptest.NewRecorder()
	r.ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusBadRequest {
		t.Fatalf("missing ids status = %d, want %d", missingRes.Code, http.StatusBadRequest)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/files/meta?ids="+private.ID+"&ids="+public.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var got []database.CloudFile
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 || got[0].ID != public.ID {
		t.Fatalf("metadata = %+v, want only %s", got, public.ID)
	}
}

func testAuthMiddleware(accountID uuid.UUID) gin.HandlerFunc {
	return func(c *gin.Context) {
		dyauth.WithAuth(c, &dyauth.AuthResult{
			Account: &gen.DyAccount{Id: accountID.String()},
			Session: &gen.DyAuthSession{Id: "session-1", AccountId: accountID.String()},
		}, dyauth.TokenInfo{Token: "test-token"})
		c.Next()
	}
}

func openHandlerTestDB(t *testing.T, values ...any) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+database.NewID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(values...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	return db
}

func TestPatchFileRenamesOwnedFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()
	file := database.CloudFile{ID: database.NewID(), Name: "before.txt", AccountID: accountID, Indexed: true}
	if err := db.Create(&file).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/files/"+file.ID, strings.NewReader(`{"name":"after.txt"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	updated, err := files.GetFile(file.ID)
	if err != nil {
		t.Fatalf("GetFile() error = %v", err)
	}
	if updated.Name != "after.txt" {
		t.Fatalf("updated.Name = %q, want %q", updated.Name, "after.txt")
	}

	var got database.CloudFile
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name != "after.txt" {
		t.Fatalf("response name = %q, want %q", got.Name, "after.txt")
	}
}

func TestPatchFileRequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	file := database.CloudFile{ID: database.NewID(), Name: "before.txt", AccountID: uuid.New(), Indexed: true}
	if err := db.Create(&file).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}

	r := gin.New()
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/files/"+file.ID, strings.NewReader(`{"name":"after.txt"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestPatchFileRejectsForbiddenRename(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	ownerID := uuid.New()
	viewerID := uuid.New()
	file := database.CloudFile{ID: database.NewID(), Name: "before.txt", AccountID: ownerID, Indexed: true}
	if err := db.Create(&file).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	perm := database.FilePermission{ID: database.NewID(), FileID: file.ID, SubjectType: "account", SubjectID: viewerID.String(), Permission: "read"}
	if err := db.Create(&perm).Error; err != nil {
		t.Fatalf("create permission: %v", err)
	}

	r := gin.New()
	r.Use(testAuthMiddleware(viewerID))
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/files/"+file.ID, strings.NewReader(`{"name":"after.txt"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestCreateEditSessionAndWOPIRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.FileLock{})
	tmp := t.TempDir()
	stor := storage.NewLocalBackend(tmp)
	files := service.NewFileService(&database.DB{DB: db}, stor)
	wopi := newTestWOPIService(t, files)

	accountID := uuid.New()
	objectID := database.NewID()
	fileID := database.NewID()
	key := objectID
	if err := db.Create(&database.FileObject{ID: objectID, Size: int64(len("hello")), MimeType: "text/plain", Hash: "hash-1", StorageKey: &key, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "notes.txt", AccountID: accountID, ObjectID: &objectID, StorageKey: &key, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := stor.Put(context.Background(), key, strings.NewReader("hello"), int64(len("hello")), "text/plain"); err != nil {
		t.Fatalf("put source: %v", err)
	}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, wopi, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	editReq := httptest.NewRequest(http.MethodPost, "/api/files/"+fileID+"/edit", nil)
	editRes := httptest.NewRecorder()
	r.ServeHTTP(editRes, editReq)
	if editRes.Code != http.StatusOK {
		t.Fatalf("edit session status = %d, body = %s", editRes.Code, editRes.Body.String())
	}
	var session struct {
		Action     string            `json:"action"`
		ActionURL  string            `json:"action_url"`
		FormFields map[string]string `json:"form_fields"`
	}
	if err := json.Unmarshal(editRes.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode edit session: %v", err)
	}
	if session.Action != "edit" {
		t.Fatalf("session.Action = %q, want edit", session.Action)
	}
	token := session.FormFields["access_token"]
	if token == "" {
		t.Fatal("access_token is empty")
	}
	if !strings.Contains(session.ActionURL, "WOPISrc=") {
		t.Fatalf("actionUrl = %q, want WOPISrc", session.ActionURL)
	}

	infoReq := httptest.NewRequest(http.MethodGet, "/wopi/files/"+fileID+"?access_token="+token, nil)
	infoRes := httptest.NewRecorder()
	r.ServeHTTP(infoRes, infoReq)
	if infoRes.Code != http.StatusOK {
		t.Fatalf("checkfileinfo status = %d, body = %s", infoRes.Code, infoRes.Body.String())
	}

	lockReq := httptest.NewRequest(http.MethodPost, "/wopi/files/"+fileID+"?access_token="+token, nil)
	lockReq.Header.Set("X-WOPI-Override", "LOCK")
	lockReq.Header.Set("X-WOPI-Lock", "lock-1")
	lockRes := httptest.NewRecorder()
	r.ServeHTTP(lockRes, lockReq)
	if lockRes.Code != http.StatusOK {
		t.Fatalf("lock status = %d, body = %s", lockRes.Code, lockRes.Body.String())
	}

	putReq := httptest.NewRequest(http.MethodPost, "/wopi/files/"+fileID+"/contents?access_token="+token, strings.NewReader("hello world"))
	putReq.Header.Set("Content-Type", "text/plain")
	putReq.Header.Set("X-WOPI-Lock", "lock-1")
	putRes := httptest.NewRecorder()
	r.ServeHTTP(putRes, putReq)
	if putRes.Code != http.StatusOK {
		t.Fatalf("putfile status = %d, body = %s", putRes.Code, putRes.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/wopi/files/"+fileID+"/contents?access_token="+token, nil)
	getRes := httptest.NewRecorder()
	r.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("getfile status = %d, body = %s", getRes.Code, getRes.Body.String())
	}
	if got := getRes.Body.String(); got != "hello world" {
		t.Fatalf("getfile body = %q, want %q", got, "hello world")
	}
}

func TestWOPIPutFileRejectsLockMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.FileLock{})
	tmp := t.TempDir()
	stor := storage.NewLocalBackend(tmp)
	files := service.NewFileService(&database.DB{DB: db}, stor)
	wopi := newTestWOPIService(t, files)

	accountID := uuid.New()
	objectID := database.NewID()
	fileID := database.NewID()
	key := objectID
	if err := db.Create(&database.FileObject{ID: objectID, Size: 5, MimeType: "text/plain", Hash: "hash-1", StorageKey: &key, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "notes.txt", AccountID: accountID, ObjectID: &objectID, StorageKey: &key, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := stor.Put(context.Background(), key, strings.NewReader("hello"), int64(len("hello")), "text/plain"); err != nil {
		t.Fatalf("put source: %v", err)
	}
	if err := db.Create(&database.FileLock{FileID: fileID, LockToken: "lock-a", Protocol: "wopi", ExpiresAt: time.Now().Add(5 * time.Minute)}).Error; err != nil {
		t.Fatalf("create lock: %v", err)
	}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, wopi, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	editReq := httptest.NewRequest(http.MethodPost, "/api/files/"+fileID+"/edit", nil)
	editRes := httptest.NewRecorder()
	r.ServeHTTP(editRes, editReq)
	if editRes.Code != http.StatusOK {
		t.Fatalf("edit session status = %d, body = %s", editRes.Code, editRes.Body.String())
	}
	var session struct {
		FormFields map[string]string `json:"form_fields"`
	}
	if err := json.Unmarshal(editRes.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode edit session: %v", err)
	}

	putReq := httptest.NewRequest(http.MethodPost, "/wopi/files/"+fileID+"/contents?access_token="+session.FormFields["access_token"], strings.NewReader("updated"))
	putReq.Header.Set("Content-Type", "text/plain")
	putReq.Header.Set("X-WOPI-Lock", "lock-b")
	putRes := httptest.NewRecorder()
	r.ServeHTTP(putRes, putReq)
	if putRes.Code != http.StatusConflict {
		t.Fatalf("putfile status = %d, want %d, body = %s", putRes.Code, http.StatusConflict, putRes.Body.String())
	}
}

func TestWOPIEndpointsAcceptBearerAccessToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.FileLock{})
	tmp := t.TempDir()
	stor := storage.NewLocalBackend(tmp)
	files := service.NewFileService(&database.DB{DB: db}, stor)
	wopi := newTestWOPIService(t, files)

	accountID := uuid.New()
	objectID := database.NewID()
	fileID := database.NewID()
	key := objectID
	if err := db.Create(&database.FileObject{ID: objectID, Size: int64(len("hello")), MimeType: "text/plain", Hash: "hash-1", StorageKey: &key, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "notes.txt", AccountID: accountID, ObjectID: &objectID, StorageKey: &key, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := stor.Put(context.Background(), key, strings.NewReader("hello"), int64(len("hello")), "text/plain"); err != nil {
		t.Fatalf("put source: %v", err)
	}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, wopi, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	editReq := httptest.NewRequest(http.MethodPost, "/api/files/"+fileID+"/edit", nil)
	editRes := httptest.NewRecorder()
	r.ServeHTTP(editRes, editReq)
	if editRes.Code != http.StatusOK {
		t.Fatalf("edit session status = %d, body = %s", editRes.Code, editRes.Body.String())
	}
	var session struct {
		FormFields map[string]string `json:"form_fields"`
	}
	if err := json.Unmarshal(editRes.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode edit session: %v", err)
	}
	token := session.FormFields["access_token"]
	if token == "" {
		t.Fatal("access_token is empty")
	}

	infoReq := httptest.NewRequest(http.MethodGet, "/wopi/files/"+fileID, nil)
	infoReq.Header.Set("Authorization", "Bearer "+token)
	infoRes := httptest.NewRecorder()
	r.ServeHTTP(infoRes, infoReq)
	if infoRes.Code != http.StatusOK {
		t.Fatalf("checkfileinfo status = %d, body = %s", infoRes.Code, infoRes.Body.String())
	}
}

// memS3Backend is a thread-safe in-memory s3server.Backend used to host the
// mock S3 endpoint for multipart direct-upload handler tests.
type memS3Backend struct {
	mu      sync.Mutex
	objects map[string][]byte
	buckets map[string]bool
}

func newMemS3Backend() *memS3Backend {
	return &memS3Backend{objects: map[string][]byte{}, buckets: map[string]bool{}}
}

func (b *memS3Backend) ListBuckets(context.Context) ([]s3server.BucketInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []s3server.BucketInfo
	for name := range b.buckets {
		out = append(out, s3server.BucketInfo{Name: name})
	}
	return out, nil
}

func (b *memS3Backend) HeadBucket(_ context.Context, bucket string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.buckets[bucket] {
		return fmt.Errorf("bucket not found")
	}
	return nil
}

func (b *memS3Backend) CreateBucket(_ context.Context, bucket string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buckets[bucket] = true
	return nil
}

func (b *memS3Backend) DeleteBucket(_ context.Context, bucket string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.buckets, bucket)
	return nil
}

func (b *memS3Backend) ListObjects(_ context.Context, bucket, prefix, marker string, maxKeys int) ([]s3server.ObjectEntry, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []s3server.ObjectEntry
	for key, data := range b.objects {
		if !strings.HasPrefix(key, bucket+"/") {
			continue
		}
		objKey := key[len(bucket)+1:]
		if prefix != "" && !strings.HasPrefix(objKey, prefix) {
			continue
		}
		if marker != "" && objKey <= marker {
			continue
		}
		out = append(out, s3server.ObjectEntry{Key: objKey, Size: int64(len(data)), LastModified: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), StorageClass: "STANDARD"})
	}
	return out, false, nil
}

func (b *memS3Backend) GetObject(_ context.Context, bucket, key string) (io.ReadCloser, s3server.ObjectInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[bucket+"/"+key]
	if !ok {
		return nil, s3server.ObjectInfo{}, fmt.Errorf("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), s3server.ObjectInfo{Size: int64(len(data)), ModTime: time.Now()}, nil
}

func (b *memS3Backend) PutObject(_ context.Context, bucket, key string, reader io.Reader, _ int64, _ string) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[bucket+"/"+key] = data
	return nil
}

func (b *memS3Backend) DeleteObject(_ context.Context, bucket, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, bucket+"/"+key)
	return nil
}

func (b *memS3Backend) StatObject(_ context.Context, bucket, key string) (s3server.ObjectInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[bucket+"/"+key]
	if !ok {
		return s3server.ObjectInfo{}, fmt.Errorf("not found")
	}
	return s3server.ObjectInfo{Size: int64(len(data)), ModTime: time.Now()}, nil
}

func (b *memS3Backend) SignedURL(context.Context, string, string, time.Duration, string, bool) (string, error) {
	return "", nil
}

// startNoAuthMockS3 starts the mock S3 server without credential checks and
// returns its host:port endpoint.
func startNoAuthMockS3(t *testing.T, bucket string) string {
	t.Helper()
	srv := s3server.New(newMemS3Backend(), "", "")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	endpoint := ts.Listener.Addr().String()
	req, err := http.NewRequest(http.MethodPut, "http://"+endpoint+"/"+bucket, nil)
	if err != nil {
		t.Fatalf("bucket request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create bucket status = %d", resp.StatusCode)
	}
	return endpoint
}

func putURL(t *testing.T, url string, body []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new PUT request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT %s: status = %d", url, resp.StatusCode)
	}
}

func TestPrepareDirectUploadMultipartFallsBackToProxied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.QuotaRecord{}, &database.PersistentTask{})
	files := service.NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(t.TempDir()))
	tasks := service.NewTaskService(&database.DB{DB: db})
	quota := service.NewQuotaService(&database.DB{DB: db})
	accountID := uuid.New()

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, tasks, quota, nil, nil)

	body := `{"file_name":"a.bin","file_size":1024,"content_type":"application/octet-stream","multipart":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/files/upload/prepare", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Error         string `json:"error"`
		UseProxied    bool   `json:"use_proxied_upload"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode prepare response: %v", err)
	}
	if !resp.UseProxied {
		t.Fatalf("prepare response = %+v, want use_proxied_upload = true", resp)
	}
}

func TestPrepareDirectUploadMultipartRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoint := startNoAuthMockS3(t, "testbucket")
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.QuotaRecord{}, &database.PersistentTask{})
	files := service.NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(t.TempDir()))
	tasks := service.NewTaskService(&database.DB{DB: db})
	quota := service.NewQuotaService(&database.DB{DB: db})
	accountID := uuid.New()

	poolID := database.NewID()
	pool := database.FilePool{
		ID:        poolID,
		Name:      "test-s3",
		AccountID: accountID,
		StorageConfig: datatypes.JSON([]byte(fmt.Sprintf(
			`{"enable_signed":true,"enable_ssl":false,"endpoint":%q,"bucket":"testbucket","secret_id":"ak","secret_key":"sk"}`,
			endpoint))),
		BillingConfig: datatypes.JSON([]byte(`{}`)),
		PolicyConfig:  datatypes.JSON([]byte(`{}`)),
	}
	if err := db.Create(&pool).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	dispatcher := &recordingDispatcher{}
	RegisterRoutes(r, &config.Config{}, files, nil, tasks, quota, nil, dispatcher)

	fileSize := int64(12*1024*1024 + 7) // 3 parts at 5 MiB
	prepareBody := fmt.Sprintf(`{"file_name":"big.bin","file_size":%d,"content_type":"application/octet-stream","multipart":true,"pool_id":%q}`, fileSize, poolID)
	req := httptest.NewRequest(http.MethodPost, "/api/files/upload/prepare", strings.NewReader(prepareBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("prepare status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var prepared struct {
		TaskID    string `json:"task_id"`
		UploadID  string `json:"upload_id"`
		PartSize  int64  `json:"part_size"`
		PartCount int    `json:"part_count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &prepared); err != nil {
		t.Fatalf("decode prepare response: %v", err)
	}
	if prepared.TaskID == "" || prepared.UploadID == "" {
		t.Fatalf("prepare response = %+v, want task_id and upload_id", prepared)
	}
	if prepared.PartSize != 5*1024*1024 || prepared.PartCount != 3 {
		t.Fatalf("prepare part plan = size %d count %d, want 5242880 x 3", prepared.PartSize, prepared.PartCount)
	}

	// Completing with no uploaded parts must be rejected and leave the task
	// awaiting upload.
	req = httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/complete", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("early complete status = %d, want 400, body = %s", w.Code, w.Body.String())
	}

	// Upload each part through its presigned URL.
	partPayloads := [][]byte{
		bytes.Repeat([]byte{0x01}, 5*1024*1024),
		bytes.Repeat([]byte{0x02}, 5*1024*1024),
		bytes.Repeat([]byte{0x03}, int(fileSize-10*1024*1024)),
	}
	for i := 1; i <= 3; i++ {
		partReq := httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/part", strings.NewReader(fmt.Sprintf(`{"part_number":%d}`, i)))
		partReq.Header.Set("Content-Type", "application/json")
		partRes := httptest.NewRecorder()
		r.ServeHTTP(partRes, partReq)
		if partRes.Code != http.StatusOK {
			t.Fatalf("presign part %d status = %d, body = %s", i, partRes.Code, partRes.Body.String())
		}
		var part struct {
			UploadURL string `json:"upload_url"`
		}
		if err := json.Unmarshal(partRes.Body.Bytes(), &part); err != nil {
			t.Fatalf("decode presign part response: %v", err)
		}
		if part.UploadURL == "" {
			t.Fatalf("presign part %d returned empty upload_url", i)
		}
		putURL(t, part.UploadURL, partPayloads[i-1])
	}

	req = httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/complete", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("complete-direct status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var file struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &file); err != nil {
		t.Fatalf("decode complete-direct response: %v", err)
	}
	if file.ID == "" {
		t.Fatalf("complete-direct response missing file id: %s", w.Body.String())
	}
	created, err := files.GetFile(file.ID)
	if err != nil {
		t.Fatalf("GetFile() error = %v", err)
	}
	if created.Object == nil || created.Object.Size != fileSize {
		t.Fatalf("created file size = %+v, want %d", created.Object, fileSize)
	}
}

func TestPresignUploadPartRejectsOutOfRangeAndUnknownTask(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoint := startNoAuthMockS3(t, "testbucket")
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.QuotaRecord{}, &database.PersistentTask{})
	files := service.NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(t.TempDir()))
	tasks := service.NewTaskService(&database.DB{DB: db})
	quota := service.NewQuotaService(&database.DB{DB: db})
	accountID := uuid.New()

	poolID := database.NewID()
	pool := database.FilePool{
		ID:        poolID,
		Name:      "test-s3",
		AccountID: accountID,
		StorageConfig: datatypes.JSON([]byte(fmt.Sprintf(
			`{"enable_signed":true,"enable_ssl":false,"endpoint":%q,"bucket":"testbucket","secret_id":"ak","secret_key":"sk"}`,
			endpoint))),
		BillingConfig: datatypes.JSON([]byte(`{}`)),
		PolicyConfig:  datatypes.JSON([]byte(`{}`)),
	}
	if err := db.Create(&pool).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, tasks, quota, nil, nil)

	prepareBody := `{"file_name":"small.bin","file_size":1024,"content_type":"application/octet-stream","multipart":true,"pool_id":"` + poolID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/files/upload/prepare", strings.NewReader(prepareBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("prepare status = %d, body = %s", w.Code, w.Body.String())
	}
	var prepared struct {
		TaskID    string `json:"task_id"`
		PartCount int    `json:"part_count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &prepared); err != nil {
		t.Fatalf("decode prepare response: %v", err)
	}

	// 1024 bytes at 5 MiB parts is exactly one part.
	for _, invalid := range []int{0, 2} {
		partReq := httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/part", strings.NewReader(fmt.Sprintf(`{"part_number":%d}`, invalid)))
		partReq.Header.Set("Content-Type", "application/json")
		partRes := httptest.NewRecorder()
		r.ServeHTTP(partRes, partReq)
		if partRes.Code != http.StatusBadRequest {
			t.Fatalf("part_number %d status = %d, want 400, body = %s", invalid, partRes.Code, partRes.Body.String())
		}
	}

	// Unknown task id.
	partReq := httptest.NewRequest(http.MethodPost, "/api/files/upload/nope/part", strings.NewReader(`{"part_number":1}`))
	partReq.Header.Set("Content-Type", "application/json")
	partRes := httptest.NewRecorder()
	r.ServeHTTP(partRes, partReq)
	if partRes.Code != http.StatusNotFound {
		t.Fatalf("unknown task status = %d, want 404, body = %s", partRes.Code, partRes.Body.String())
	}
}
