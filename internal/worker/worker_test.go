package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "file.txt", AccountID: ownerID, PoolID: &sourcePoolID, StorageKey: &storageKey, ObjectID: &objectID}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	unselectedFileID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: unselectedFileID, Name: "stay.txt", AccountID: ownerID, PoolID: &sourcePoolID}).Error; err != nil {
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
	if file.PoolID == nil || *file.PoolID != targetPoolID {
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

func TestProcessReanalysisQueueReanalyzesFlaggedObject(t *testing.T) {
	tmp := t.TempDir()
	db := openWorkerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
	stor := storage.NewLocalBackend(tmp)
	svc := service.NewFileService(&database.DB{DB: db}, stor)

	imgPath := filepath.Join(tmp, "source.png")
	encoded, err := os.Create(imgPath)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	blank := image.NewRGBA(image.Rect(0, 0, 4, 5))
	if err := png.Encode(encoded, blank); err != nil {
		_ = encoded.Close()
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
	clientMeta := datatypes.JSON([]byte(`{"analysis_source":"client","width":1920,"height":1080}`))
	if err := db.Create(&database.FileObject{
		ID: objectID, Size: int64(len(payload)), MimeType: "image/png", StorageKey: &objectID,
		Meta: clientMeta, NeedsReanalysis: true,
	}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	fileID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "photo.png", AccountID: uuid.New(), ObjectID: &objectID, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	// Age the queue entry past the delay the worker applies before picking it up.
	if err := db.Model(&database.FileObject{}).Where("id = ?", objectID).Updates(map[string]any{"updated_at": time.Now().Add(-time.Hour)}).Error; err != nil {
		t.Fatalf("age queue entry: %v", err)
	}

	w := New(nil, svc, stor, &database.DB{DB: db}, tmp)
	w.processReanalysisQueue(context.Background())

	var object database.FileObject
	if err := db.First(&object, "id = ?", objectID).Error; err != nil {
		t.Fatalf("reload object: %v", err)
	}
	if object.NeedsReanalysis {
		t.Fatal("reanalysis flag was not cleared after a successful pass")
	}
	var meta map[string]any
	if err := json.Unmarshal(object.Meta, &meta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if meta["analysis_source"] != "server" {
		t.Fatalf("analysis_source = %v, want server", meta["analysis_source"])
	}
	if meta["width"] != float64(4) || meta["height"] != float64(5) {
		t.Fatalf("dimensions = %v x %v, want the server-measured 4 x 5", meta["width"], meta["height"])
	}
}

func TestProcessReanalysisQueueRetriesThenGivesUp(t *testing.T) {
	tmp := t.TempDir()
	db := openWorkerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
	stor := storage.NewLocalBackend(tmp)
	svc := service.NewFileService(&database.DB{DB: db}, stor)

	// The object is flagged but its bytes were never written, so every pass fails.
	objectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: objectID, Size: 4, MimeType: "image/png", StorageKey: &objectID, Meta: datatypes.JSON([]byte(`{}`)), NeedsReanalysis: true}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	fileID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "missing.png", AccountID: uuid.New(), ObjectID: &objectID, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}

	w := New(nil, svc, stor, &database.DB{DB: db}, tmp)
	for attempt := 1; attempt <= sourceReanalysisMaxAttempts; attempt++ {
		if err := db.Model(&database.FileObject{}).Where("id = ?", objectID).Updates(map[string]any{"updated_at": time.Now().Add(-time.Hour)}).Error; err != nil {
			t.Fatalf("age queue entry: %v", err)
		}
		w.processReanalysisQueue(context.Background())

		var object database.FileObject
		if err := db.First(&object, "id = ?", objectID).Error; err != nil {
			t.Fatalf("reload object: %v", err)
		}
		if object.ReanalysisAttempts != attempt {
			t.Fatalf("attempts after pass %d = %d, want %d", attempt, object.ReanalysisAttempts, attempt)
		}
		wantQueued := attempt < sourceReanalysisMaxAttempts
		if object.NeedsReanalysis != wantQueued {
			t.Fatalf("queued after pass %d = %v, want %v", attempt, object.NeedsReanalysis, wantQueued)
		}
	}
}
