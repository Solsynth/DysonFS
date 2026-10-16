package s3server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

// TestMasterBackendPutObjectStoresInBucketPool drives an upload through the
// master S3 API into a bucket that maps to a non-default pool. The object must
// be written to that pool's backend; the file row records the bucket as its
// pool, and every read resolves storage from that pool.
func TestMasterBackendPutObjectStoresInBucketPool(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+database.NewID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&database.FilePool{}, &database.FileObject{}, &database.CloudFile{}, &database.FilePermission{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	defaultDir := t.TempDir()
	poolDir := t.TempDir()
	defaultPoolID := database.NewID()
	poolID := database.NewID()
	cfg := &config.Config{
		Pools: []config.PoolConfig{
			{ID: defaultPoolID, Name: "default", Default: true, Storage: config.StoragePoolConfig{Endpoint: defaultDir}},
			{ID: poolID, Name: "custom", Storage: config.StoragePoolConfig{Endpoint: poolDir}},
		},
	}
	files := service.NewFileService(&database.DB{DB: db}, nil)
	if _, err := files.SeedPools(cfg); err != nil {
		t.Fatalf("SeedPools: %v", err)
	}
	defaultBackend, err := files.BackendForPoolID(&defaultPoolID)
	if err != nil {
		t.Fatalf("resolve default backend: %v", err)
	}
	files.SetStorage(defaultBackend)

	accountID := uuid.New()
	backend := NewMasterBackend(files, t.TempDir())
	ctx := context.WithValue(context.Background(), tokenInfoContextKey, &TokenInfo{AccountID: accountID.String(), PoolID: &poolID})

	content := []byte("report body")
	if err := backend.PutObject(ctx, poolID, "report.pdf", bytes.NewReader(content), int64(len(content)), "application/pdf"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	var file database.CloudFile
	if err := db.Preload("Object").First(&file, "name = ?", "report.pdf").Error; err != nil {
		t.Fatalf("load file: %v", err)
	}
	if file.PoolID == nil || *file.PoolID != poolID {
		t.Fatalf("file.PoolID = %v, want bucket pool %q", file.PoolID, poolID)
	}
	if file.Object == nil || file.Object.StorageKey == nil {
		t.Fatalf("file = %+v, want a stored object", file)
	}
	key := *file.Object.StorageKey

	if _, err := os.Stat(filepath.Join(defaultDir, key)); !os.IsNotExist(err) {
		t.Fatalf("bytes landed in the default pool backend (stat err = %v)", err)
	}
	stored, err := os.Stat(filepath.Join(poolDir, key))
	if err != nil {
		t.Fatalf("bytes missing from the bucket's pool: %v", err)
	}
	if stored.Size() != int64(len(content)) {
		t.Fatalf("stored size = %d, want %d", stored.Size(), len(content))
	}

	// Reading through the S3 API resolves the pool backend the file claims.
	rc, info, err := backend.GetObject(ctx, poolID, file.ID)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer rc.Close()
	if info.Size != int64(len(content)) {
		t.Fatalf("object size = %d, want %d", info.Size, len(content))
	}
}
