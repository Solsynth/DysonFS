package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

func TestBackendFromPoolStorageLocal(t *testing.T) {
	tmp := t.TempDir()
	backend, err := backendFromPoolStorage(PoolStorageConfig{Endpoint: tmp}, nil)
	if err != nil {
		t.Fatalf("backendFromPoolStorage() error = %v", err)
	}

	local, ok := backend.(*storage.LocalBackend)
	if !ok {
		t.Fatalf("backendFromPoolStorage() backend type = %T, want *storage.LocalBackend", backend)
	}

	const key = "files/example.txt"
	if err := local.Put(context.Background(), key, strings.NewReader("hello"), "text/plain"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmp, key)); err != nil {
		t.Fatalf("file not written: %v", err)
	}
}

func TestBackendFromPoolStorageMissingEndpoint(t *testing.T) {
	if _, err := backendFromPoolStorage(PoolStorageConfig{}, nil); err == nil {
		t.Fatal("backendFromPoolStorage() error = nil, want error")
	}
}

func TestListOwnedReturnsAllUserFiles(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FileObject{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	svc := NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	otherID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "root", AccountID: accountID, Indexed: true}).Error; err != nil {
		t.Fatalf("create root file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "nested", AccountID: accountID, ParentID: ptr("parent"), Indexed: false}).Error; err != nil {
		t.Fatalf("create nested file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "other", AccountID: otherID, Indexed: true}).Error; err != nil {
		t.Fatalf("create other file: %v", err)
	}

	files, err := svc.ListOwned(accountID)
	if err != nil {
		t.Fatalf("ListOwned() error = %v", err)
	}
	if got := len(files); got != 2 {
		t.Fatalf("len(ListOwned()) = %d, want 2", got)
	}
}

func TestListRootOwnedExcludesChildren(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FileObject{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	svc := NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "root-indexed", AccountID: accountID, Indexed: true}).Error; err != nil {
		t.Fatalf("create root file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "root-unindexed", AccountID: accountID, Indexed: false}).Error; err != nil {
		t.Fatalf("create root unindexed file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "child", AccountID: accountID, ParentID: ptr("parent"), Indexed: true}).Error; err != nil {
		t.Fatalf("create child file: %v", err)
	}

	files, err := svc.ListRootOwned(accountID, 20)
	if err != nil {
		t.Fatalf("ListRootOwned() error = %v", err)
	}
	if got := len(files); got != 2 {
		t.Fatalf("len(ListRootOwned()) = %d, want 2", got)
	}
	for _, f := range files {
		if f.ParentID != nil {
			t.Fatalf("expected only root files, got child %q", f.Name)
		}
	}
}

func TestListRootOwnedDefaultsToRecentFirst(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FileObject{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	svc := NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	for i := 0; i < 25; i++ {
		createdAt := time.Unix(int64(i), 0)
		if err := db.Create(&database.CloudFile{ID: fmt.Sprintf("file-%02d", i), Name: fmt.Sprintf("file-%02d", i), AccountID: accountID, Indexed: true, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			t.Fatalf("create file %d: %v", i, err)
		}
	}

	files, err := svc.ListRootOwned(accountID, 20)
	if err != nil {
		t.Fatalf("ListRootOwned() error = %v", err)
	}
	if got := len(files); got != 20 {
		t.Fatalf("len(ListRootOwned()) = %d, want 20", got)
	}
	if files[0].Name != "file-24" {
		t.Fatalf("first file = %q, want newest first", files[0].Name)
	}
	if files[len(files)-1].Name != "file-05" {
		t.Fatalf("last returned file = %q, want 20 newest only", files[len(files)-1].Name)
	}
}

func ptr(v string) *string { return &v }
