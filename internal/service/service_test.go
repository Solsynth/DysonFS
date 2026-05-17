package service

import (
	"context"
	"encoding/json"
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

func TestCreateUploadedFileCreatesReplica(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{})
	tmp := t.TempDir()
	poolID := seedDefaultPool(t, db, tmp)
	svc := NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(tmp))
	svc.defaultPoolID = poolID
	objectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: objectID, Size: 12, MimeType: "text/plain", Hash: "hash", Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	storageKey := objectID
	file, err := svc.CreateUploadedFile(uuid.New(), "sample.txt", nil, objectID, nil, nil, &storageKey)
	if err != nil {
		t.Fatalf("CreateUploadedFile() error = %v", err)
	}
	if file.PoolID == nil || *file.PoolID != poolID {
		t.Fatalf("file.PoolID = %v, want %q", file.PoolID, poolID)
	}
	if file.Description != nil {
		t.Fatalf("file.Description = %v, want nil", *file.Description)
	}
	var replica database.FileReplica
	if err := db.First(&replica, "object_id = ?", objectID).Error; err != nil {
		t.Fatalf("load replica: %v", err)
	}
	if replica.PoolID == nil || *replica.PoolID != poolID {
		t.Fatalf("replica.PoolID = %v, want %q", replica.PoolID, poolID)
	}
	if replica.Status != "primary" || !replica.IsPrimary {
		t.Fatalf("replica = %+v, want primary replica", replica)
	}
}

func TestCreateDerivedFileCreatesReplicaUsingParentPool(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{})
	tmp := t.TempDir()
	poolID := seedDefaultPool(t, db, tmp)
	svc := NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(tmp))
	svc.defaultPoolID = poolID
	parentObjectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: parentObjectID, Size: 12, MimeType: "text/plain", Hash: "hash", Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create parent object: %v", err)
	}
	parentID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: parentID, Name: "parent", AccountID: uuid.New(), PoolID: ptr(poolID), ObjectID: &parentObjectID, Indexed: true}).Error; err != nil {
		t.Fatalf("create parent file: %v", err)
	}
	derivedObjectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: derivedObjectID, Size: 8, MimeType: "image/webp", Hash: "hash2", Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create derived object: %v", err)
	}
	storageKey := parentID + "/thumbnail.webp"
	file, err := svc.CreateDerivedFile(uuid.New(), parentID, "parent", derivedObjectID, "system.thumbnail", &storageKey)
	if err != nil {
		t.Fatalf("CreateDerivedFile() error = %v", err)
	}
	if file.PoolID == nil || *file.PoolID != poolID {
		t.Fatalf("file.PoolID = %v, want %q", file.PoolID, poolID)
	}
	var replica database.FileReplica
	if err := db.First(&replica, "object_id = ?", derivedObjectID).Error; err != nil {
		t.Fatalf("load replica: %v", err)
	}
	if replica.PoolID == nil || *replica.PoolID != poolID {
		t.Fatalf("replica.PoolID = %v, want %q", replica.PoolID, poolID)
	}
}

func TestCreateUploadedFilePersistsDescription(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{})
	tmp := t.TempDir()
	poolID := seedDefaultPool(t, db, tmp)
	svc := NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(tmp))
	svc.defaultPoolID = poolID
	objectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: objectID, Size: 12, MimeType: "text/plain", Hash: "hash", Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	description := "uploaded from phone"
	storageKey := objectID
	file, err := svc.CreateUploadedFile(uuid.New(), "sample.txt", &description, objectID, nil, nil, &storageKey)
	if err != nil {
		t.Fatalf("CreateUploadedFile() error = %v", err)
	}
	if file.Description == nil || *file.Description != description {
		t.Fatalf("file.Description = %v, want %q", file.Description, description)
	}
}

func TestRepairMissingReplicasCreatesReplicaOnlyForExistingRemoteObject(t *testing.T) {
	tmp := t.TempDir()
	stor := storage.NewLocalBackend(tmp)
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{})
	poolID := seedDefaultPool(t, db, tmp)
	svc := NewFileService(&database.DB{DB: db}, stor)
	svc.defaultPoolID = poolID
	accountID := uuid.New()
	objectID := database.NewID()
	storageKey := objectID
	if err := db.Create(&database.FileObject{ID: objectID, Size: 3, MimeType: "text/plain", Hash: "hash", StorageKey: &storageKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "sample.txt", AccountID: accountID, PoolID: ptr(poolID), StorageID: ptr(poolID), ObjectID: &objectID, StorageKey: &storageKey, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := stor.Put(context.Background(), storageKey, strings.NewReader("abc"), "text/plain"); err != nil {
		t.Fatalf("stor.Put() error = %v", err)
	}
	missingID := database.NewID()
	missingKey := missingID
	if err := db.Create(&database.FileObject{ID: missingID, Size: 4, MimeType: "text/plain", Hash: "hash2", StorageKey: &missingKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create missing object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "missing.txt", AccountID: accountID, PoolID: ptr(poolID), StorageID: ptr(poolID), ObjectID: &missingID, StorageKey: &missingKey, Indexed: true}).Error; err != nil {
		t.Fatalf("create missing file: %v", err)
	}
	previews, summary, err := svc.PreviewMissingReplicas(context.Background(), 0)
	if err != nil {
		t.Fatalf("PreviewMissingReplicas() error = %v", err)
	}
	if summary.Verified != 1 || summary.MissingRemote != 1 {
		t.Fatalf("preview summary = %+v, want verified=1 missing_remote=1", summary)
	}
	if len(previews) != 2 {
		t.Fatalf("len(previews) = %d, want 2", len(previews))
	}
	_, summary, err = svc.RepairMissingReplicas(context.Background(), 0)
	if err != nil {
		t.Fatalf("RepairMissingReplicas() error = %v", err)
	}
	if summary.Created != 1 {
		t.Fatalf("summary.Created = %d, want 1", summary.Created)
	}
	var replicaCount int64
	if err := db.Model(&database.FileReplica{}).Count(&replicaCount).Error; err != nil {
		t.Fatalf("count replicas: %v", err)
	}
	if replicaCount != 1 {
		t.Fatalf("replica count = %d, want 1", replicaCount)
	}
}

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
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{}); err != nil {
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
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{}); err != nil {
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
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{}); err != nil {
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
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	tmp := t.TempDir()
	stor := storage.NewLocalBackend(tmp)
	svc := NewFileService(&database.DB{DB: db}, stor)
	svc.defaultPoolID = seedDefaultPool(t, db, tmp)

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

func TestStoreSourceAnalysisStoresSharedMediaDimensions(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{})
	svc := NewFileService(&database.DB{DB: db}, nil)
	objectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: objectID, Size: 12, MimeType: "video/mp4", Hash: "hash", Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	fileID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "sample.mp4", AccountID: uuid.New(), ObjectID: &objectID, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	analysis := &SourceAnalysis{
		Width:  1920,
		Height: 1080,
		Media: map[string]any{
			"streams": []any{map[string]any{"codec_type": "video", "width": float64(1920), "height": float64(1080)}},
		},
	}
	updated, err := svc.StoreSourceAnalysis(fileID, analysis)
	if err != nil {
		t.Fatalf("StoreSourceAnalysis() error = %v", err)
	}
	if updated.Object == nil {
		t.Fatal("expected object to be loaded")
	}
	var meta map[string]any
	if err := json.Unmarshal(updated.Object.Meta, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if got := int(meta["width"].(float64)); got != 1920 {
		t.Fatalf("meta width = %d, want 1920", got)
	}
	if got := int(meta["height"].(float64)); got != 1080 {
		t.Fatalf("meta height = %d, want 1080", got)
	}
	if _, ok := meta["media"].(map[string]any); !ok {
		t.Fatalf("meta media missing or wrong type: %#v", meta["media"])
	}
	if width, height := mediaDimensions(analysis.Media); width != 1920 || height != 1080 {
		t.Fatalf("mediaDimensions() = (%d, %d), want (1920, 1080)", width, height)
	}
}

func TestListReanalysisCandidatesIncludesVideoMetadataGaps(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{})
	svc := NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(t.TempDir()))
	objectID := database.NewID()
	storageKey := objectID
	if err := db.Create(&database.FileObject{ID: objectID, Size: 12, MimeType: "video/quicktime", Hash: "hash", StorageKey: &storageKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	fileID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "clip.mov", AccountID: uuid.New(), ObjectID: &objectID, StorageKey: &storageKey, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	items, err := svc.ListReanalysisCandidates(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListReanalysisCandidates() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Kind != "video" {
		t.Fatalf("candidate kind = %q, want video", items[0].Kind)
	}
	if !strings.Contains(items[0].Reason, "missing media") {
		t.Fatalf("candidate reason = %q, want missing media", items[0].Reason)
	}
}

func TestReanalyzeFilesDeduplicatesAndUpdatesSourceMetadata(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{})
	tmp := t.TempDir()
	stor := storage.NewLocalBackend(tmp)
	svc := NewFileService(&database.DB{DB: db}, stor)
	svc.defaultPoolID = seedDefaultPool(t, db, tmp)

	imgPath := filepath.Join(tmp, "target.png")
	f, err := os.Create(imgPath)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	if err := png.Encode(f, blankImage(4, 5)); err != nil {
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
	file := &database.CloudFile{ID: database.NewID(), Name: "target.png", AccountID: uuid.New(), ObjectID: &object.ID, StorageKey: &object.ID, Indexed: true}
	if err := db.Create(file).Error; err != nil {
		t.Fatalf("create cloud file: %v", err)
	}
	if err := db.Model(&database.FileObject{}).Where("id = ?", object.ID).Updates(map[string]any{"meta": datatypes.JSON([]byte(`{}`)), "size": 0}).Error; err != nil {
		t.Fatalf("seed object: %v", err)
	}

	res, err := svc.ReanalyzeFiles(context.Background(), []string{file.ID, file.ID, ""})
	if err != nil {
		t.Fatalf("ReanalyzeFiles() error = %v", err)
	}
	if res.Scanned != 1 || res.Updated != 1 {
		t.Fatalf("result = %+v, want scanned=1 updated=1", res)
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

func openTestDB(t *testing.T, models ...any) *gorm.DB {
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

func seedDefaultPool(t *testing.T, db *gorm.DB, endpoint string) string {
	t.Helper()
	poolID := database.NewID()
	if err := db.Create(&database.FilePool{ID: poolID, Name: "default", AccountID: uuid.Nil, StorageConfig: datatypes.JSON([]byte(fmt.Sprintf(`{"endpoint":%q}`, endpoint))), BillingConfig: datatypes.JSON([]byte(`{}`)), PolicyConfig: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}
	return poolID
}

func ptr(v string) *string { return &v }
