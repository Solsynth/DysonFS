package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
)

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

	RegisterRoutes(r, &config.Config{}, files, tasks, quota, (*eventbus.Bus)(nil), nil)
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
	RegisterRoutes(r, &config.Config{}, files, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

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
	RegisterRoutes(r, &config.Config{}, files, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

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
	RegisterRoutes(r, &config.Config{}, files, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

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
	RegisterRoutes(r, &config.Config{}, files, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

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
