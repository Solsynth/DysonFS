package worker

import (
	"context"
	"fmt"
	"io"
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
	db := openWorkerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
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
	if err := stor.Put(context.Background(), storageKey, strings.NewReader(string(content)), int64(len(content)), "video/mp4"); err != nil {
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

func TestProcessPoolMigrationsMovesObjectAndTracksProgress(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()
	db := openWorkerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.PersistentTask{})
	svc := service.NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(sourceDir))
	ownerID := uuid.New()
	sourcePoolID := database.NewID()
	targetPoolID := database.NewID()
	for _, pool := range []database.FilePool{
		{ID: sourcePoolID, Name: "source", AccountID: ownerID, StorageConfig: datatypes.JSON([]byte(fmt.Sprintf(`{"endpoint":%q}`, sourceDir)))},
		{ID: targetPoolID, Name: "target", AccountID: ownerID, StorageConfig: datatypes.JSON([]byte(fmt.Sprintf(`{"endpoint":%q}`, targetDir)))},
	} {
		if err := db.Create(&pool).Error; err != nil {
			t.Fatalf("create pool: %v", err)
		}
	}
	content := []byte("move me")
	objectID := database.NewID()
	storageKey := objectID
	if err := db.Create(&database.FileObject{ID: objectID, Size: int64(len(content)), MimeType: "text/plain", StorageKey: &storageKey}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	fileID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "file.txt", AccountID: ownerID, PoolID: &sourcePoolID, StorageID: &sourcePoolID, StorageKey: &storageKey, ObjectID: &objectID}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	unselectedFileID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: unselectedFileID, Name: "stay.txt", AccountID: ownerID, PoolID: &sourcePoolID, StorageID: &sourcePoolID}).Error; err != nil {
		t.Fatalf("create unselected file: %v", err)
	}
	if err := storage.NewLocalBackend(sourceDir).Put(context.Background(), storageKey, strings.NewReader(string(content)), int64(len(content)), "text/plain"); err != nil {
		t.Fatalf("put source object: %v", err)
	}
	tasks := service.NewTaskService(&database.DB{DB: db})
	task, err := tasks.CreatePoolMigrationTask(ownerID, sourcePoolID, targetPoolID, []string{fileID})
	if err != nil {
		t.Fatalf("create migration task: %v", err)
	}

	w := New(nil, svc, storage.NewLocalBackend(sourceDir), &database.DB{DB: db}, t.TempDir())
	w.processPoolMigrations(context.Background())

	if err := db.First(&task, "task_id = ?", task.TaskID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != "completed" || task.Progress != 1 || task.ChunksUploaded != 1 {
		t.Fatalf("task = %+v, want completed one-file migration", task)
	}
	var file database.CloudFile
	if err := db.First(&file, "id = ?", fileID).Error; err != nil {
		t.Fatalf("reload file: %v", err)
	}
	if file.PoolID == nil || *file.PoolID != targetPoolID || file.StorageID == nil || *file.StorageID != targetPoolID {
		t.Fatalf("file = %+v, want target pool", file)
	}
	var unselected database.CloudFile
	if err := db.First(&unselected, "id = ?", unselectedFileID).Error; err != nil {
		t.Fatalf("reload unselected file: %v", err)
	}
	if unselected.PoolID == nil || *unselected.PoolID != sourcePoolID {
		t.Fatalf("unselected file = %+v, want source pool", unselected)
	}
	reader, _, err := storage.NewLocalBackend(targetDir).Get(context.Background(), storageKey)
	if err != nil {
		t.Fatalf("get target object: %v", err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil || string(got) != string(content) {
		t.Fatalf("target content = %q, err = %v", got, err)
	}
	if _, err := storage.NewLocalBackend(sourceDir).Stat(context.Background(), storageKey); !os.IsNotExist(err) {
		t.Fatalf("source object remains after migration, err = %v", err)
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
