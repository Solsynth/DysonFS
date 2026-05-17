package service

import (
	"context"
	"fmt"
	"image"
	"image/png"
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
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	gen "src.solsynth.dev/sosys/go/proto"
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
	if err := db.AutoMigrate(&database.FilePermission{}); err != nil {
		t.Fatalf("AutoMigrate() permission table error = %v", err)
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
	if err := db.AutoMigrate(&database.FilePermission{}); err != nil {
		t.Fatalf("AutoMigrate() permission table error = %v", err)
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
	if err := db.AutoMigrate(&database.FilePermission{}); err != nil {
		t.Fatalf("AutoMigrate() permission table error = %v", err)
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

func TestReanalyzeMissingImageMetadata(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FileObject{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	tmp := t.TempDir()
	stor := storage.NewLocalBackend(tmp)
	svc := NewFileService(&database.DB{DB: db}, stor)

	imgPath := filepath.Join(tmp, "sample.png")
	f, err := os.Create(imgPath)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	if err := png.Encode(f, blankImage(2, 3)); err != nil {
		_ = f.Close()
		t.Fatalf("encode image: %v", err)
	}
	_ = f.Close()

	object, err := svc.DetectAndCreateObject(imgPath)
	if err != nil {
		t.Fatalf("DetectAndCreateObject() error = %v", err)
	}
	imgFile, err := os.Open(imgPath)
	if err != nil {
		t.Fatalf("open image: %v", err)
	}
	if err := stor.Put(context.Background(), object.ID, imgFile, object.MimeType); err != nil {
		_ = imgFile.Close()
		t.Fatalf("stor.Put() error = %v", err)
	}
	_ = imgFile.Close()
	file := &database.CloudFile{ID: database.NewID(), Name: "sample.png", AccountID: uuid.New(), ObjectID: &object.ID, Indexed: true, FileMeta: nil, UserMeta: nil}
	if err := db.Create(file).Error; err != nil {
		t.Fatalf("create cloud file: %v", err)
	}
	if err := db.Model(&database.FileObject{}).Where("id = ?", object.ID).Updates(map[string]any{"meta": datatypes.JSON([]byte(`{}`)), "size": 0}).Error; err != nil {
		t.Fatalf("seed object: %v", err)
	}

	res, err := svc.ReanalyzeMissingImageMetadata(context.Background(), 10)
	if err != nil {
		t.Fatalf("ReanalyzeMissingImageMetadata() error = %v", err)
	}
	if res.Updated != 1 {
		t.Fatalf("Updated = %d, want 1", res.Updated)
	}
	updated, err := svc.GetFile(file.ID)
	if err != nil {
		t.Fatalf("GetFile() error = %v", err)
	}
	if updated.Object == nil || updated.Object.Meta == nil {
		t.Fatalf("expected object meta to be populated")
	}
	if updated.Object == nil || updated.Object.Meta == nil {
		t.Fatalf("expected object meta to be populated")
	}
}

func TestCanAccessFileInheritsPermissionsFromAncestors(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FilePermission{}, &database.FileObject{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	svc := NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	viewerID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	childID := database.NewID()
	parentID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: parentID, Name: "parent", AccountID: accountID, Indexed: true}).Error; err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: childID, Name: "child", AccountID: accountID, ParentID: ptr(parentID), Indexed: true}).Error; err != nil {
		t.Fatalf("create child: %v", err)
	}
	perm := database.FilePermission{ID: database.NewID(), FileID: parentID, SubjectType: "account", SubjectID: viewerID.String(), Permission: "read"}
	if err := db.Create(&perm).Error; err != nil {
		t.Fatalf("create permission: %v", err)
	}

	child, err := svc.GetFile(childID)
	if err != nil {
		t.Fatalf("GetFile() error = %v", err)
	}
	if !svc.CanAccessFile(&gen.DyAccount{Id: viewerID.String()}, nil, child, "read") {
		t.Fatal("expected child access to inherit from parent permission")
	}

	grandchildID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: grandchildID, Name: "grandchild", AccountID: accountID, ParentID: ptr(childID), Indexed: true}).Error; err != nil {
		t.Fatalf("create grandchild: %v", err)
	}
	grandchild, err := svc.GetFile(grandchildID)
	if err != nil {
		t.Fatalf("GetFile() error = %v", err)
	}
	if !svc.CanAccessFile(&gen.DyAccount{Id: viewerID.String()}, nil, grandchild, "read") {
		t.Fatal("expected grandchild access to inherit through the full parent tree")
	}
}

func TestCanAccessFileDefaultsToPublicWithoutPermissions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FileObject{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	if err := db.AutoMigrate(&database.FilePermission{}); err != nil {
		t.Fatalf("AutoMigrate() permission table error = %v", err)
	}

	svc := NewFileService(&database.DB{DB: db}, nil)
	file := &database.CloudFile{ID: database.NewID(), Name: "file", AccountID: uuid.New(), Indexed: true}
	if err := db.Create(file).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	loaded, err := svc.GetFile(file.ID)
	if err != nil {
		t.Fatalf("GetFile() error = %v", err)
	}
	if !svc.CanAccessFile(nil, nil, loaded, "read") {
		t.Fatal("expected files without permissions to remain public")
	}
}

func blankImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	return img
}

func ptr(v string) *string { return &v }
