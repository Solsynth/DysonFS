package worker

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

func TestProcessUploadedFileFallsBackToStorageWhenTempPathMissing(t *testing.T) {
	tmp := t.TempDir()
	db := openWorkerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{})
	stor := storage.NewLocalBackend(tmp)
	svc := service.NewFileService(&database.DB{DB: db}, stor)
	svcDefaultPoolID := seedWorkerDefaultPool(t, db, tmp)
	_ = svcDefaultPoolID
	content := []byte("not-a-real-video-but-good-enough-for-fallback")
	objectID := database.NewID()
	storageKey := objectID
	if err := db.Create(&database.FileObject{ID: objectID, Size: int64(len(content)), MimeType: "video/mp4", Hash: service.ComputeHash(content), StorageKey: &storageKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	fileID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "sample.mp4", AccountID: uuid.New(), ObjectID: &objectID, StorageKey: &storageKey, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := stor.Put(context.Background(), storageKey, strings.NewReader(string(content)), "video/mp4"); err != nil {
		t.Fatalf("put object: %v", err)
	}
	w := New(nil, svc, stor, &database.DB{DB: db}, tmp)
	err := w.ProcessUploadedFile(context.Background(), eventbus.FileUploadedEvent{FileID: fileID, ContentType: "video/mp4", ProcessingFilePath: tmp + "/missing-file", IsTempFile: true})
	if err == nil {
		t.Fatal("expected ffmpeg processing error after storage fallback, got nil")
	}
	if strings.Contains(err.Error(), tmp+"/missing-file") {
		t.Fatalf("expected fallback to avoid missing temp path error, got %v", err)
	}
	if _, statErr := os.Stat(tmp + "/missing-file"); !os.IsNotExist(statErr) {
		t.Fatalf("expected missing temp path to stay absent, stat err = %v", statErr)
	}
}

func openWorkerTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return db
}

func seedWorkerDefaultPool(t *testing.T, db *gorm.DB, endpoint string) string {
	t.Helper()
	poolID := database.NewID()
	if err := db.Create(&database.FilePool{ID: poolID, Name: "default", AccountID: uuid.Nil, StorageConfig: datatypes.JSON([]byte(fmt.Sprintf(`{"endpoint":%q}`, endpoint))), BillingConfig: datatypes.JSON([]byte(`{}`)), PolicyConfig: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}
	return poolID
}
