package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	dyauth "src.solsynth.dev/sosys/go/pkg/auth"
	gen "src.solsynth.dev/sosys/go/proto"
)

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

func TestOpenFileFallsBackToLegacyThumbnailStorageKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{})
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
	if err := stor.Put(context.Background(), legacyKey, strings.NewReader("thumb"), "image/jpeg"); err != nil {
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{})
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
	if err := stor.Put(context.Background(), legacyKey, strings.NewReader("compressed"), "image/webp"); err != nil {
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{})
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
	if err := stor.Put(context.Background(), legacyKey, strings.NewReader("compressed"), "image/webp"); err != nil {
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{})
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
	if err := stor.Put(context.Background(), parentKey, strings.NewReader("original"), "image/png"); err != nil {
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{})
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{})
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{})
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{})
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{})
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{})
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{})
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{})
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{})
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{}, &database.WOPILock{})
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
	if err := stor.Put(context.Background(), key, strings.NewReader("hello"), "text/plain"); err != nil {
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
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FileReplica{}, &database.FilePermission{}, &database.WOPILock{})
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
	if err := stor.Put(context.Background(), key, strings.NewReader("hello"), "text/plain"); err != nil {
		t.Fatalf("put source: %v", err)
	}
	if err := db.Create(&database.WOPILock{FileID: fileID, LockID: "lock-a", ExpiresAt: time.Now().Add(5 * time.Minute)}).Error; err != nil {
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
