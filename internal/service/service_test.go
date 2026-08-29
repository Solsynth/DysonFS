package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	sharedcache "src.solsynth.dev/sosys/go/pkg/cache"
	gen "src.solsynth.dev/sosys/go/proto"
)

type stubAccountClient struct {
	account *gen.DyAccount
	calls   int
}

func (s *stubAccountClient) GetAccount(context.Context, *gen.DyGetAccountRequest, ...grpc.CallOption) (*gen.DyAccount, error) {
	s.calls++
	return s.account, nil
}

func (s *stubAccountClient) GetAccountBatch(context.Context, *gen.DyGetAccountBatchRequest, ...grpc.CallOption) (*gen.DyGetAccountBatchResponse, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) GetBotAccountBatch(context.Context, *gen.DyGetBotAccountBatchRequest, ...grpc.CallOption) (*gen.DyGetAccountBatchResponse, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) GetBotAccount(context.Context, *gen.DyGetBotAccountRequest, ...grpc.CallOption) (*gen.DyAccount, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) LookupAccountBatch(context.Context, *gen.DyLookupAccountBatchRequest, ...grpc.CallOption) (*gen.DyGetAccountBatchResponse, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) SearchAccount(context.Context, *gen.DySearchAccountRequest, ...grpc.CallOption) (*gen.DyGetAccountBatchResponse, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) ListAccounts(context.Context, *gen.DyListAccountsRequest, ...grpc.CallOption) (*gen.DyListAccountsResponse, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) GetProfile(context.Context, *gen.DyGetProfileRequest, ...grpc.CallOption) (*gen.DyAccountProfile, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) UpdateProfile(context.Context, *gen.DyUpdateProfileRequest, ...grpc.CallOption) (*gen.DyAccountProfile, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) ListBadges(context.Context, *gen.DyListBadgesRequest, ...grpc.CallOption) (*gen.DyListBadgesResponse, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) GrantBadge(context.Context, *gen.DyGrantBadgeRequest, ...grpc.CallOption) (*gen.DyGrantBadgeResponse, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) GetBadge(context.Context, *gen.DyGetBadgeRequest, ...grpc.CallOption) (*gen.DyGetBadgeResponse, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) UpdateBadge(context.Context, *gen.DyUpdateBadgeRequest, ...grpc.CallOption) (*gen.DyUpdateBadgeResponse, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) GetRelationship(context.Context, *gen.DyGetRelationshipRequest, ...grpc.CallOption) (*gen.DyGetRelationshipResponse, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) HasRelationship(context.Context, *gen.DyGetRelationshipRequest, ...grpc.CallOption) (*wrapperspb.BoolValue, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) ListFriends(context.Context, *gen.DyListRelationshipSimpleRequest, ...grpc.CallOption) (*gen.DyListRelationshipSimpleResponse, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) ListBlocked(context.Context, *gen.DyListRelationshipSimpleRequest, ...grpc.CallOption) (*gen.DyListRelationshipSimpleResponse, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) GetAccountStatus(context.Context, *gen.DyGetAccountRequest, ...grpc.CallOption) (*gen.DyAccountStatus, error) {
	panic("unexpected call")
}

func (s *stubAccountClient) GetAccountStatusBatch(context.Context, *gen.DyGetAccountBatchRequest, ...grpc.CallOption) (*gen.DyGetAccountStatusBatchResponse, error) {
	panic("unexpected call")
}

func TestCreateUploadedFileCreatesReplica(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
	tmp := t.TempDir()
	poolID := seedDefaultPool(t, db, tmp)
	svc := NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(tmp))
	svc.defaultPoolID = poolID
	objectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: objectID, Size: 12, MimeType: "text/plain", Hash: "hash", Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	storageKey := objectID
	file, err := svc.CreateUploadedFile(uuid.New(), "sample.txt", nil, nil, nil, nil, nil, objectID, nil, nil, &storageKey, false)
	if err != nil {
		t.Fatalf("CreateUploadedFile() error = %v", err)
	}
	if file.PoolID == nil || *file.PoolID != poolID {
		t.Fatalf("file.PoolID = %v, want %q", file.PoolID, poolID)
	}
	if file.Description != nil {
		t.Fatalf("file.Description = %v, want nil", *file.Description)
	}
	var object database.FileObject
	if err := db.First(&object, "id = ?", objectID).Error; err != nil {
		t.Fatalf("load object: %v", err)
	}
	if file.StorageKey == nil || *file.StorageKey != storageKey {
		t.Fatalf("file.StorageKey = %v, want %q", file.StorageKey, storageKey)
	}
}

func TestCreateDerivedFileCreatesReplicaUsingParentPool(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
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
	storageKey := parentID + ".thumbnail"
	file, err := svc.CreateDerivedFile(uuid.New(), parentID, "parent", derivedObjectID, "system.thumbnail", &storageKey)
	if err != nil {
		t.Fatalf("CreateDerivedFile() error = %v", err)
	}
	if file.PoolID == nil || *file.PoolID != poolID {
		t.Fatalf("file.PoolID = %v, want %q", file.PoolID, poolID)
	}
	if file.StorageKey == nil || *file.StorageKey != storageKey {
		t.Fatalf("file.StorageKey = %v, want %q", file.StorageKey, storageKey)
	}
}

func TestDeleteDerivedFileUpdatesParentCompatibilityFlags(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
	tmp := t.TempDir()
	poolID := seedDefaultPool(t, db, tmp)
	svc := NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(tmp))
	svc.defaultPoolID = poolID

	parentObjectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: parentObjectID, Size: 12, MimeType: "image/webp", Hash: "parent-hash", Meta: datatypes.JSON([]byte(`{}`)), HasThumbnail: true, HasCompression: true}).Error; err != nil {
		t.Fatalf("create parent object: %v", err)
	}
	parentID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: parentID, Name: "parent", AccountID: uuid.New(), PoolID: ptr(poolID), ObjectID: &parentObjectID, Indexed: true}).Error; err != nil {
		t.Fatalf("create parent file: %v", err)
	}

	thumbObjectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: thumbObjectID, Size: 8, MimeType: "image/webp", Hash: "thumb-hash", Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create thumbnail object: %v", err)
	}
	thumbKey := parentID + ".thumbnail"
	thumbFile, err := svc.CreateDerivedFile(uuid.New(), parentID, "parent", thumbObjectID, "system.thumbnail", &thumbKey)
	if err != nil {
		t.Fatalf("create thumbnail file: %v", err)
	}

	compObjectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: compObjectID, Size: 8, MimeType: "image/webp", Hash: "comp-hash", Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create compression object: %v", err)
	}
	compKey := parentID + ".compressed"
	compFile, err := svc.CreateDerivedFile(uuid.New(), parentID, "parent", compObjectID, "system.compression.low", &compKey)
	if err != nil {
		t.Fatalf("create compression file: %v", err)
	}

	if err := svc.DeleteFile(thumbFile.ID); err != nil {
		t.Fatalf("DeleteFile(thumbnail) error = %v", err)
	}
	var parentObject database.FileObject
	if err := db.First(&parentObject, "id = ?", parentObjectID).Error; err != nil {
		t.Fatalf("reload parent object after thumbnail delete: %v", err)
	}
	if parentObject.HasThumbnail {
		t.Fatal("HasThumbnail = true, want false after deleting last thumbnail child")
	}
	if !parentObject.HasCompression {
		t.Fatal("HasCompression = false, want true while compression child remains")
	}

	if err := svc.DeleteFile(compFile.ID); err != nil {
		t.Fatalf("DeleteFile(compression) error = %v", err)
	}
	if err := db.First(&parentObject, "id = ?", parentObjectID).Error; err != nil {
		t.Fatalf("reload parent object after compression delete: %v", err)
	}
	if parentObject.HasCompression {
		t.Fatal("HasCompression = true, want false after deleting last compression child")
	}
}

func TestCreateUploadedFilePersistsDescription(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
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
	file, err := svc.CreateUploadedFile(uuid.New(), "sample.txt", &description, nil, nil, nil, nil, objectID, nil, nil, &storageKey, false)
	if err != nil {
		t.Fatalf("CreateUploadedFile() error = %v", err)
	}
	if file.Description == nil || *file.Description != description {
		t.Fatalf("file.Description = %v, want %q", file.Description, description)
	}
}

func TestPurgeFileDeletesDereferencedObjectAndRemote(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	tmp := t.TempDir()
	poolID := seedDefaultPool(t, db, tmp)
	stor := storage.NewLocalBackend(tmp)
	svc := NewFileService(&database.DB{DB: db}, stor)
	svc.defaultPoolID = poolID

	objectID := database.NewID()
	storageKey := objectID
	if err := db.Create(&database.FileObject{ID: objectID, Size: 5, MimeType: "text/plain", Hash: "hash", StorageKey: &storageKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	fileID := database.NewID()
	accountID := uuid.New()
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "sample.txt", AccountID: accountID, PoolID: ptr(poolID), StorageID: ptr(poolID), ObjectID: &objectID, StorageKey: &storageKey, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := db.Create(&database.FilePermission{ID: database.NewID(), FileID: fileID, SubjectType: "private", Permission: "read"}).Error; err != nil {
		t.Fatalf("create permission: %v", err)
	}
	if err := stor.Put(context.Background(), storageKey, strings.NewReader("hello"), int64(len("hello")), "text/plain"); err != nil {
		t.Fatalf("put remote object: %v", err)
	}

	if err := svc.PurgeFile(fileID); err != nil {
		t.Fatalf("PurgeFile() error = %v", err)
	}

	if err := db.Unscoped().First(&database.CloudFile{}, "id = ?", fileID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cloud file still exists, err = %v", err)
	}
	if err := db.Unscoped().First(&database.FileObject{}, "id = ?", objectID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("file object still exists, err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, storageKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remote object still exists, stat err = %v", err)
	}
}

func TestPurgeFileDeletesDescendantsAndTheirObjects(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	tmp := t.TempDir()
	poolID := seedDefaultPool(t, db, tmp)
	stor := storage.NewLocalBackend(tmp)
	svc := NewFileService(&database.DB{DB: db}, stor)
	svc.defaultPoolID = poolID

	accountID := uuid.New()
	rootID := database.NewID()
	childID := database.NewID()
	grandchildID := database.NewID()
	childObjectID := database.NewID()
	grandchildObjectID := database.NewID()
	childStorageKey := childObjectID
	grandchildStorageKey := grandchildObjectID

	if err := db.Create(&database.CloudFile{ID: rootID, Name: "root", AccountID: accountID, PoolID: ptr(poolID), IsFolder: true, Indexed: true}).Error; err != nil {
		t.Fatalf("create root folder: %v", err)
	}
	if err := db.Create(&database.FileObject{ID: childObjectID, Size: 5, MimeType: "text/plain", Hash: "child-hash", StorageKey: &childStorageKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create child object: %v", err)
	}
	if err := db.Create(&database.FileObject{ID: grandchildObjectID, Size: 7, MimeType: "text/plain", Hash: "grandchild-hash", StorageKey: &grandchildStorageKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create grandchild object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: childID, Name: "child.txt", AccountID: accountID, PoolID: ptr(poolID), StorageID: ptr(poolID), ParentID: &rootID, ObjectID: &childObjectID, StorageKey: &childStorageKey, Indexed: true}).Error; err != nil {
		t.Fatalf("create child file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: grandchildID, Name: "grandchild.txt", AccountID: accountID, PoolID: ptr(poolID), StorageID: ptr(poolID), ParentID: &childID, ObjectID: &grandchildObjectID, StorageKey: &grandchildStorageKey, Indexed: true}).Error; err != nil {
		t.Fatalf("create grandchild file: %v", err)
	}
	for _, perm := range []database.FilePermission{
		{ID: database.NewID(), FileID: childID, SubjectType: "private", Permission: "read"},
		{ID: database.NewID(), FileID: grandchildID, SubjectType: "private", Permission: "read"},
	} {
		if err := db.Create(&perm).Error; err != nil {
			t.Fatalf("create permission for %s: %v", perm.FileID, err)
		}
	}
	if err := stor.Put(context.Background(), childStorageKey, strings.NewReader("child"), int64(len("child")), "text/plain"); err != nil {
		t.Fatalf("put child remote object: %v", err)
	}
	if err := stor.Put(context.Background(), grandchildStorageKey, strings.NewReader("grandchild"), int64(len("grandchild")), "text/plain"); err != nil {
		t.Fatalf("put grandchild remote object: %v", err)
	}

	if err := svc.PurgeFile(rootID); err != nil {
		t.Fatalf("PurgeFile(root) error = %v", err)
	}

	for _, fileID := range []string{rootID, childID, grandchildID} {
		if err := db.Unscoped().First(&database.CloudFile{}, "id = ?", fileID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("cloud file %s still exists, err = %v", fileID, err)
		}
	}
	for _, objectID := range []string{childObjectID, grandchildObjectID} {
		if err := db.Unscoped().First(&database.FileObject{}, "id = ?", objectID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("file object %s still exists, err = %v", objectID, err)
		}
	}
	var permissionCount int64
	if err := db.Unscoped().Model(&database.FilePermission{}).Where("file_id IN ?", []string{childID, grandchildID}).Count(&permissionCount).Error; err != nil {
		t.Fatalf("count permissions: %v", err)
	}
	if permissionCount != 0 {
		t.Fatalf("permission count = %d, want 0", permissionCount)
	}
	for _, key := range []string{childStorageKey, grandchildStorageKey} {
		if _, err := os.Stat(filepath.Join(tmp, key)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("remote object %s still exists, stat err = %v", key, err)
		}
	}
}

func TestPurgeFileKeepsSharedObjectAndRemote(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	tmp := t.TempDir()
	poolID := seedDefaultPool(t, db, tmp)
	stor := storage.NewLocalBackend(tmp)
	svc := NewFileService(&database.DB{DB: db}, stor)
	svc.defaultPoolID = poolID

	objectID := database.NewID()
	storageKey := objectID
	if err := db.Create(&database.FileObject{ID: objectID, Size: 5, MimeType: "text/plain", Hash: "hash", StorageKey: &storageKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	firstFileID := database.NewID()
	secondFileID := database.NewID()
	accountID := uuid.New()
	for _, fileID := range []string{firstFileID, secondFileID} {
		if err := db.Create(&database.CloudFile{ID: fileID, Name: "shared.txt", AccountID: accountID, PoolID: ptr(poolID), StorageID: ptr(poolID), ObjectID: &objectID, StorageKey: &storageKey, Indexed: true}).Error; err != nil {
			t.Fatalf("create file %s: %v", fileID, err)
		}
	}
	if err := stor.Put(context.Background(), storageKey, strings.NewReader("hello"), int64(len("hello")), "text/plain"); err != nil {
		t.Fatalf("put remote object: %v", err)
	}

	if err := svc.PurgeFile(firstFileID); err != nil {
		t.Fatalf("PurgeFile() error = %v", err)
	}

	if err := db.Unscoped().First(&database.CloudFile{}, "id = ?", firstFileID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("first file still exists, err = %v", err)
	}
	if err := db.First(&database.CloudFile{}, "id = ?", secondFileID).Error; err != nil {
		t.Fatalf("second file missing: %v", err)
	}
	if err := db.First(&database.FileObject{}, "id = ?", objectID).Error; err != nil {
		t.Fatalf("shared object missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, storageKey)); err != nil {
		t.Fatalf("remote object missing: %v", err)
	}
}

func TestOverwriteFileSwapsObjectAndDeletesDereferencedSource(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
	tmp := t.TempDir()
	poolID := seedDefaultPool(t, db, tmp)
	stor := storage.NewLocalBackend(tmp)
	svc := NewFileService(&database.DB{DB: db}, stor)
	svc.defaultPoolID = poolID

	accountID := uuid.New()
	oldObjectID := database.NewID()
	oldStorageKey := oldObjectID
	newObjectID := database.NewID()
	newStorageKey := newObjectID
	fileID := database.NewID()
	derivedObjectID := database.NewID()
	derivedStorageKey := fileID + ".compressed"
	derivedType := "system.compression.low"

	for _, object := range []database.FileObject{
		{ID: oldObjectID, Size: 5, MimeType: "text/plain", Hash: "old-hash", StorageKey: &oldStorageKey, Meta: datatypes.JSON([]byte(`{}`))},
		{ID: newObjectID, Size: 7, MimeType: "text/plain", Hash: "new-hash", StorageKey: &newStorageKey, Meta: datatypes.JSON([]byte(`{}`))},
		{ID: derivedObjectID, Size: 3, MimeType: "image/webp", Hash: "derived-hash", StorageKey: &derivedStorageKey, Meta: datatypes.JSON([]byte(`{}`))},
	} {
		if err := db.Create(&object).Error; err != nil {
			t.Fatalf("create object %s: %v", object.ID, err)
		}
	}
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "doc.txt", AccountID: accountID, PoolID: ptr(poolID), StorageID: ptr(poolID), ObjectID: &oldObjectID, StorageKey: &oldStorageKey, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "doc.txt", AccountID: accountID, PoolID: ptr(poolID), StorageID: ptr(poolID), ParentID: &fileID, ObjectID: &derivedObjectID, StorageKey: &derivedStorageKey, Indexed: false, ApplicationType: &derivedType}).Error; err != nil {
		t.Fatalf("create derived file: %v", err)
	}
	if err := stor.Put(context.Background(), oldStorageKey, strings.NewReader("hello"), int64(len("hello")), "text/plain"); err != nil {
		t.Fatalf("put old object: %v", err)
	}
	if err := stor.Put(context.Background(), derivedStorageKey, strings.NewReader("cmp"), int64(len("cmp")), "image/webp"); err != nil {
		t.Fatalf("put derived object: %v", err)
	}

	updated, err := svc.OverwriteFile(fileID, newObjectID, &newStorageKey)
	if err != nil {
		t.Fatalf("OverwriteFile() error = %v", err)
	}
	if updated.ObjectID == nil || *updated.ObjectID != newObjectID {
		t.Fatalf("updated.ObjectID = %v, want %q", updated.ObjectID, newObjectID)
	}
	if updated.StorageKey == nil || *updated.StorageKey != newStorageKey {
		t.Fatalf("updated.StorageKey = %v, want %q", updated.StorageKey, newStorageKey)
	}
	if err := db.Unscoped().First(&database.FileObject{}, "id = ?", oldObjectID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("old object still exists, err = %v", err)
	}
	if err := db.Unscoped().First(&database.FileObject{}, "id = ?", derivedObjectID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("derived object still exists, err = %v", err)
	}
	var derivedCount int64
	if err := db.Model(&database.CloudFile{}).Where("parent_id = ? AND deleted_at IS NULL", fileID).Count(&derivedCount).Error; err != nil {
		t.Fatalf("count derived files: %v", err)
	}
	if derivedCount != 0 {
		t.Fatalf("derived count = %d, want 0", derivedCount)
	}
	if _, err := os.Stat(filepath.Join(tmp, oldStorageKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old remote object still exists, err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, derivedStorageKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("derived remote object still exists, err = %v", err)
	}
	if err := db.First(&database.FileObject{}, "id = ?", newObjectID).Error; err != nil {
		t.Fatalf("new object missing: %v", err)
	}
	var newObject database.FileObject
	if err := db.First(&newObject, "id = ?", newObjectID).Error; err != nil {
		t.Fatalf("load new object: %v", err)
	}
	if newObject.StorageKey == nil || *newObject.StorageKey != newStorageKey {
		t.Fatalf("new object StorageKey = %v, want %q", newObject.StorageKey, newStorageKey)
	}
}

func TestOverwriteFileKeepsSharedPreviousObject(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
	tmp := t.TempDir()
	poolID := seedDefaultPool(t, db, tmp)
	stor := storage.NewLocalBackend(tmp)
	svc := NewFileService(&database.DB{DB: db}, stor)
	svc.defaultPoolID = poolID

	accountID := uuid.New()
	sharedObjectID := database.NewID()
	sharedStorageKey := sharedObjectID
	newObjectID := database.NewID()
	newStorageKey := newObjectID
	firstFileID := database.NewID()
	secondFileID := database.NewID()

	for _, object := range []database.FileObject{
		{ID: sharedObjectID, Size: 5, MimeType: "text/plain", Hash: "shared-hash", StorageKey: &sharedStorageKey, Meta: datatypes.JSON([]byte(`{}`))},
		{ID: newObjectID, Size: 7, MimeType: "text/plain", Hash: "new-hash", StorageKey: &newStorageKey, Meta: datatypes.JSON([]byte(`{}`))},
	} {
		if err := db.Create(&object).Error; err != nil {
			t.Fatalf("create object %s: %v", object.ID, err)
		}
	}
	for _, fileID := range []string{firstFileID, secondFileID} {
		if err := db.Create(&database.CloudFile{ID: fileID, Name: "shared.txt", AccountID: accountID, PoolID: ptr(poolID), StorageID: ptr(poolID), ObjectID: &sharedObjectID, StorageKey: &sharedStorageKey, Indexed: true}).Error; err != nil {
			t.Fatalf("create file %s: %v", fileID, err)
		}
	}
	if err := stor.Put(context.Background(), sharedStorageKey, strings.NewReader("hello"), int64(len("hello")), "text/plain"); err != nil {
		t.Fatalf("put shared object: %v", err)
	}

	updated, err := svc.OverwriteFile(firstFileID, newObjectID, &newStorageKey)
	if err != nil {
		t.Fatalf("OverwriteFile() error = %v", err)
	}
	if updated.ObjectID == nil || *updated.ObjectID != newObjectID {
		t.Fatalf("updated.ObjectID = %v, want %q", updated.ObjectID, newObjectID)
	}
	if err := db.First(&database.FileObject{}, "id = ?", sharedObjectID).Error; err != nil {
		t.Fatalf("shared object missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, sharedStorageKey)); err != nil {
		t.Fatalf("shared remote object missing: %v", err)
	}
	var reloaded database.CloudFile
	if err := db.First(&reloaded, "id = ?", secondFileID).Error; err != nil {
		t.Fatalf("reload second file: %v", err)
	}
	if reloaded.ObjectID == nil || *reloaded.ObjectID != sharedObjectID {
		t.Fatalf("second file object = %v, want %q", reloaded.ObjectID, sharedObjectID)
	}
}

func TestFastOverwriteFileUpdatesExistingObject(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
	tmp := t.TempDir()
	poolID := seedDefaultPool(t, db, tmp)
	stor := storage.NewLocalBackend(tmp)
	svc := NewFileService(&database.DB{DB: db}, stor)
	svc.defaultPoolID = poolID

	accountID := uuid.New()
	objectID := database.NewID()
	storageKey := objectID
	fileID := database.NewID()
	derivedObjectID := database.NewID()
	derivedStorageKey := fileID + ".compressed"
	derivedType := "system.compression.low"

	for _, object := range []database.FileObject{
		{ID: objectID, Size: 3, MimeType: "text/plain", Hash: "old-hash", StorageKey: &storageKey, Meta: datatypes.JSON([]byte(`{"width":1}`))},
		{ID: derivedObjectID, Size: 4, MimeType: "image/webp", Hash: "derived-hash", StorageKey: &derivedStorageKey, Meta: datatypes.JSON([]byte(`{}`))},
	} {
		if err := db.Create(&object).Error; err != nil {
			t.Fatalf("create object %s: %v", object.ID, err)
		}
	}
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "doc.txt", AccountID: accountID, PoolID: ptr(poolID), StorageID: ptr(poolID), ObjectID: &objectID, StorageKey: &storageKey, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "doc.txt", AccountID: accountID, PoolID: ptr(poolID), StorageID: ptr(poolID), ParentID: &fileID, ObjectID: &derivedObjectID, StorageKey: &derivedStorageKey, Indexed: false, ApplicationType: &derivedType}).Error; err != nil {
		t.Fatalf("create derived file: %v", err)
	}
	if err := stor.Put(context.Background(), storageKey, strings.NewReader("old"), int64(len("old")), "text/plain"); err != nil {
		t.Fatalf("put source object: %v", err)
	}
	if err := stor.Put(context.Background(), derivedStorageKey, strings.NewReader("old-derived"), int64(len("old-derived")), "image/webp"); err != nil {
		t.Fatalf("put derived object: %v", err)
	}
	sourcePath := filepath.Join(tmp, "updated.txt")
	if err := os.WriteFile(sourcePath, []byte("updated body"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	updated, applied, err := svc.FastOverwriteFile(fileID, sourcePath, &SourceAnalysis{Width: 10, Height: 20})
	if err != nil {
		t.Fatalf("FastOverwriteFile() error = %v", err)
	}
	if !applied {
		t.Fatal("FastOverwriteFile() did not apply fast overwrite")
	}
	if updated.ObjectID == nil || *updated.ObjectID != objectID {
		t.Fatalf("updated.ObjectID = %v, want %q", updated.ObjectID, objectID)
	}

	var object database.FileObject
	if err := db.First(&object, "id = ?", objectID).Error; err != nil {
		t.Fatalf("reload object: %v", err)
	}
	if object.Hash != ComputeHash([]byte("updated body")) {
		t.Fatalf("object.Hash = %q, want updated hash", object.Hash)
	}
	if object.Size != int64(len("updated body")) {
		t.Fatalf("object.Size = %d, want %d", object.Size, len("updated body"))
	}
	var meta map[string]any
	if err := json.Unmarshal(object.Meta, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if int(meta["width"].(float64)) != 10 || int(meta["height"].(float64)) != 20 {
		t.Fatalf("meta = %#v, want width=10 height=20", meta)
	}
	rc, _, err := stor.Get(context.Background(), storageKey)
	if err != nil {
		t.Fatalf("get overwritten object: %v", err)
	}
	body, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("read overwritten object: %v", err)
	}
	if string(body) != "updated body" {
		t.Fatalf("body = %q, want %q", string(body), "updated body")
	}
	var derivedCount int64
	if err := db.Model(&database.CloudFile{}).Where("parent_id = ? AND deleted_at IS NULL", fileID).Count(&derivedCount).Error; err != nil {
		t.Fatalf("count derived files: %v", err)
	}
	if derivedCount != 0 {
		t.Fatalf("derived count = %d, want 0", derivedCount)
	}
	if _, err := os.Stat(filepath.Join(tmp, derivedStorageKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("derived remote object still exists, err = %v", err)
	}
}

func TestFastOverwriteFileFallsBackWhenObjectShared(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
	tmp := t.TempDir()
	poolID := seedDefaultPool(t, db, tmp)
	stor := storage.NewLocalBackend(tmp)
	svc := NewFileService(&database.DB{DB: db}, stor)
	svc.defaultPoolID = poolID

	accountID := uuid.New()
	objectID := database.NewID()
	storageKey := objectID
	firstFileID := database.NewID()
	secondFileID := database.NewID()
	if err := db.Create(&database.FileObject{ID: objectID, Size: 3, MimeType: "text/plain", Hash: "old-hash", StorageKey: &storageKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	for _, fileID := range []string{firstFileID, secondFileID} {
		if err := db.Create(&database.CloudFile{ID: fileID, Name: "shared.txt", AccountID: accountID, PoolID: ptr(poolID), StorageID: ptr(poolID), ObjectID: &objectID, StorageKey: &storageKey, Indexed: true}).Error; err != nil {
			t.Fatalf("create file %s: %v", fileID, err)
		}
	}
	sourcePath := filepath.Join(tmp, "updated.txt")
	if err := os.WriteFile(sourcePath, []byte("updated body"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	updated, applied, err := svc.FastOverwriteFile(firstFileID, sourcePath, nil)
	if err != nil {
		t.Fatalf("FastOverwriteFile() error = %v", err)
	}
	if applied {
		t.Fatal("FastOverwriteFile() unexpectedly applied to shared object")
	}
	if updated != nil {
		t.Fatalf("updated = %#v, want nil", updated)
	}
	var object database.FileObject
	if err := db.First(&object, "id = ?", objectID).Error; err != nil {
		t.Fatalf("reload object: %v", err)
	}
	if object.Hash != "old-hash" {
		t.Fatalf("object.Hash = %q, want unchanged", object.Hash)
	}
}

func TestRestoreBatch(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{})
	svc := NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()
	firstID := database.NewID()
	secondID := database.NewID()
	for _, fileID := range []string{firstID, secondID} {
		if err := db.Create(&database.CloudFile{ID: fileID, Name: "sample.txt", AccountID: accountID, IsMarkedRecycle: true}).Error; err != nil {
			t.Fatalf("create file %s: %v", fileID, err)
		}
	}

	count, err := svc.RestoreBatch([]string{firstID, secondID})
	if err != nil {
		t.Fatalf("RestoreBatch() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("RestoreBatch() count = %d, want 2", count)
	}
	for _, fileID := range []string{firstID, secondID} {
		var file database.CloudFile
		if err := db.First(&file, "id = ?", fileID).Error; err != nil {
			t.Fatalf("load file %s: %v", fileID, err)
		}
		if file.IsMarkedRecycle {
			t.Fatalf("file %s still marked recycled", fileID)
		}
	}
}

func TestMoveBatch(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{})
	svc := NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()
	parentID := database.NewID()
	childID := database.NewID()
	siblingID := database.NewID()
	cycleParentID := database.NewID()
	cycleChildID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: parentID, Name: "parent", AccountID: accountID, IsFolder: true}).Error; err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: childID, Name: "child", AccountID: accountID, ParentID: ptr(parentID)}).Error; err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: siblingID, Name: "sibling", AccountID: accountID, ParentID: ptr(parentID)}).Error; err != nil {
		t.Fatalf("create sibling: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: cycleParentID, Name: "cycle-parent", AccountID: accountID, IsFolder: true}).Error; err != nil {
		t.Fatalf("create cycle parent: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: cycleChildID, Name: "cycle-child", AccountID: accountID, ParentID: ptr(cycleParentID)}).Error; err != nil {
		t.Fatalf("create cycle child: %v", err)
	}

	count, err := svc.MoveBatch([]string{childID, siblingID}, nil, nil)
	if err != nil {
		t.Fatalf("MoveBatch() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("MoveBatch() count = %d, want 2", count)
	}
	for _, fileID := range []string{childID, siblingID} {
		var file database.CloudFile
		if err := db.First(&file, "id = ?", fileID).Error; err != nil {
			t.Fatalf("load file %s: %v", fileID, err)
		}
		if file.ParentID != nil {
			t.Fatalf("file %s parent_id = %v, want nil", fileID, *file.ParentID)
		}
	}

	if _, err := svc.MoveBatch([]string{cycleParentID}, ptr(cycleChildID), nil); err == nil {
		t.Fatal("MoveBatch() cycle error = nil, want error")
	}
	var parent database.CloudFile
	if err := db.First(&parent, "id = ?", cycleParentID).Error; err != nil {
		t.Fatalf("reload parent: %v", err)
	}
	if parent.ParentID != nil {
		t.Fatalf("parent moved unexpectedly: %+v", parent.ParentID)
	}
}

func TestMoveBatchSetsIndexedFlag(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{})
	svc := NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()
	parentID := database.NewID()
	fileID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: parentID, Name: "folder", AccountID: accountID, IsFolder: true, Indexed: true}).Error; err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "file.txt", AccountID: accountID, Indexed: false}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}

	boolPtr := func(v bool) *bool { return &v }

	count, err := svc.MoveBatch([]string{fileID}, &parentID, boolPtr(true))
	if err != nil {
		t.Fatalf("MoveBatch() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("MoveBatch() count = %d, want 1", count)
	}
	var file database.CloudFile
	if err := db.First(&file, "id = ?", fileID).Error; err != nil {
		t.Fatalf("load file: %v", err)
	}
	if !file.Indexed {
		t.Fatalf("file.Indexed = false, want true after moving to folder")
	}
	if file.ParentID == nil || *file.ParentID != parentID {
		t.Fatalf("file.ParentID = %v, want %v", file.ParentID, parentID)
	}

	count, err = svc.MoveBatch([]string{fileID}, nil, boolPtr(false))
	if err != nil {
		t.Fatalf("MoveBatch() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("MoveBatch() count = %d, want 1", count)
	}
	if err := db.First(&file, "id = ?", fileID).Error; err != nil {
		t.Fatalf("load file: %v", err)
	}
	if file.Indexed {
		t.Fatalf("file.Indexed = true, want false after moving to root")
	}
	if file.ParentID != nil {
		t.Fatalf("file.ParentID = %v, want nil", *file.ParentID)
	}

	count, err = svc.MoveBatch([]string{fileID}, &parentID, nil)
	if err != nil {
		t.Fatalf("MoveBatch() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("MoveBatch() count = %d, want 1", count)
	}
	if err := db.First(&file, "id = ?", fileID).Error; err != nil {
		t.Fatalf("load file: %v", err)
	}
	if file.Indexed {
		t.Fatalf("file.Indexed = true, want unchanged false when indexed param is nil")
	}
}

func TestQuotaUsageCountsBytes(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
	svc := NewQuotaService(&database.DB{DB: db})
	accountID := uuid.New()
	poolID := database.NewID()
	const mb = int64(1024 * 1024)
	if err := db.Create(&database.FilePool{ID: poolID, Name: "default", AccountID: accountID}).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}
	multiplier := 1.5
	if err := db.Model(&database.FilePool{}).Where("id = ?", poolID).Update("billing_config", datatypes.JSON([]byte(fmt.Sprintf(`{"cost_multiplier":%v}`, multiplier)))).Error; err != nil {
		t.Fatalf("update billing config: %v", err)
	}
	object1 := database.NewID()
	object2 := database.NewID()
	if err := db.Create(&database.FileObject{ID: object1, Size: 120 * mb, MimeType: "text/plain", Hash: "h1"}).Error; err != nil {
		t.Fatalf("create object1: %v", err)
	}
	if err := db.Create(&database.FileObject{ID: object2, Size: 80 * mb, MimeType: "text/plain", Hash: "h2"}).Error; err != nil {
		t.Fatalf("create object2: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "a.txt", AccountID: accountID, PoolID: &poolID, ObjectID: &object1}).Error; err != nil {
		t.Fatalf("create file1: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "b.txt", AccountID: accountID, PoolID: &poolID, ObjectID: &object2}).Error; err != nil {
		t.Fatalf("create file2: %v", err)
	}

	account := &gen.DyAccount{Id: accountID.String(), Profile: &gen.DyAccountProfile{Level: 60}, PerkLevel: func() *int32 { v := int32(1); return &v }()}
	svc.SetLevelingConfig(config.LevelingQuotaConfig{Level1: 512, Level10: 1024, Level60: 5 * 1024, Level120: 10 * 1024})
	summary, err := svc.GetUsage(account)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if summary.UsedQuota != 300 || summary.TotalQuota != 16*1024 || summary.TotalFileCount != 2 || summary.TotalUsageBytes != 200*mb {
		t.Fatalf("summary = %+v, want used=300 total=16GiB count=2 bytes=200MiB", summary)
	}

	usage, err := svc.GetPoolUsage(accountID, poolID)
	if err != nil {
		t.Fatalf("GetPoolUsage() error = %v", err)
	}
	if usage["total_quota"] != int64(300) {
		t.Fatalf("usage = %+v, want total_quota=300", usage)
	}
}

func TestCheckUploadQuota(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{})
	svc := NewQuotaService(&database.DB{DB: db})
	accountID := uuid.New()
	const mb = int64(1024 * 1024)
	objectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: objectID, Size: 120 * mb, MimeType: "text/plain", Hash: "h1"}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "used.txt", AccountID: accountID, ObjectID: &objectID}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	account := &gen.DyAccount{Id: accountID.String(), Profile: &gen.DyAccountProfile{Level: 60}, PerkLevel: func() *int32 { v := int32(1); return &v }()}
	svc.SetLevelingConfig(config.LevelingQuotaConfig{Level1: 512, Level10: 1024, Level60: 5 * 1024, Level120: 10 * 1024})
	summary, err := svc.GetSummary(account)
	if err != nil {
		t.Fatalf("GetSummary() error = %v", err)
	}
	// Extra quota lives in Valve now; without a workspace client the summary is
	// the local leveling+perk fallback (level 60 = 6 GiB leveling, perk 1 = 10 GiB).
	if summary.BasedQuota != 16*1024 || summary.LevelingQuota != 6*1024 || summary.PerkQuota != 10*1024 || summary.ExtraQuota != 0 || summary.TotalQuota != 16*1024 {
		t.Fatalf("summary = %+v, want base=16GiB leveling=6GiB perk=10GiB extra=0 total=16GiB", summary)
	}

	if err := svc.CheckUploadQuota(account, 17000*mb, 1); err == nil {
		t.Fatal("CheckUploadQuota() error = nil, want quota exceeded")
	} else if !errors.Is(err, ErrQuotaExceeded) || !strings.Contains(err.Error(), "used=120MB") || !strings.Contains(err.Error(), "total=16384MB") {
		t.Fatalf("CheckUploadQuota() error = %v, want used/total details", err)
	}
}

func TestStorageBytesFromPlanQuota(t *testing.T) {
	tests := []struct {
		name  string
		quota *gen.DyWorkspacePlanQuota
		want  int64
		ok    bool
	}{
		{
			name:  "configured",
			quota: &gen.DyWorkspacePlanQuota{Quotas: map[string]int64{"max_storage_bytes": 10 * 1024 * 1024 * 1024}},
			want:  10 * 1024 * 1024 * 1024,
			ok:    true,
		},
		{name: "missing", quota: &gen.DyWorkspacePlanQuota{}, ok: false},
		{
			name:  "zero",
			quota: &gen.DyWorkspacePlanQuota{Quotas: map[string]int64{"max_storage_bytes": 0}},
			ok:    false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := storageBytesFromPlanQuota(test.quota)
			if got != test.want || ok != test.ok {
				t.Fatalf("storageBytesFromPlanQuota() = (%d, %t), want (%d, %t)", got, ok, test.want, test.ok)
			}
		})
	}
}

// stubWorkspaceClient implements DyWorkspaceServiceClient for quota tests. It
// serves one workspace, membership, and a configurable plan quota; other RPCs
// panic if called.
type stubWorkspaceClient struct {
	workspace *gen.DyWorkspace
	member    bool
	planQuota *gen.DyWorkspacePlanQuota
	planErr   error
}

func (s *stubWorkspaceClient) GetWorkspace(context.Context, *gen.DyGetWorkspaceRequest, ...grpc.CallOption) (*gen.DyWorkspace, error) {
	return s.workspace, nil
}
func (s *stubWorkspaceClient) GetWorkspaceBatch(context.Context, *gen.DyGetWorkspaceBatchRequest, ...grpc.CallOption) (*gen.DyGetWorkspaceBatchResponse, error) {
	panic("unexpected call")
}
func (s *stubWorkspaceClient) GetUserWorkspaces(context.Context, *gen.DyGetUserWorkspacesRequest, ...grpc.CallOption) (*gen.DyGetUserWorkspacesResponse, error) {
	panic("unexpected call")
}
func (s *stubWorkspaceClient) GetIndividualWorkspace(context.Context, *gen.DyGetUserWorkspacesRequest, ...grpc.CallOption) (*gen.DyWorkspace, error) {
	return s.workspace, nil
}
func (s *stubWorkspaceClient) IsMemberWithRole(context.Context, *gen.DyIsWorkspaceMemberWithRoleRequest, ...grpc.CallOption) (*wrapperspb.BoolValue, error) {
	return wrapperspb.Bool(s.member), nil
}
func (s *stubWorkspaceClient) HasPermission(context.Context, *gen.DyHasWorkspacePermissionRequest, ...grpc.CallOption) (*wrapperspb.BoolValue, error) {
	panic("unexpected call")
}
func (s *stubWorkspaceClient) GetPlanQuota(context.Context, *gen.DyGetPlanQuotaRequest, ...grpc.CallOption) (*gen.DyWorkspacePlanQuota, error) {
	if s.planErr != nil {
		return nil, s.planErr
	}
	return s.planQuota, nil
}
func (s *stubWorkspaceClient) LoadMemberAccount(context.Context, *gen.DyLoadWorkspaceMemberRequest, ...grpc.CallOption) (*gen.DyWorkspaceMember, error) {
	panic("unexpected call")
}
func (s *stubWorkspaceClient) LoadMemberAccounts(context.Context, *gen.DyLoadWorkspaceMembersRequest, ...grpc.CallOption) (*gen.DyLoadWorkspaceMembersResponse, error) {
	panic("unexpected call")
}
func (s *stubWorkspaceClient) ListMembers(context.Context, *gen.DyListWorkspaceMembersRequest, ...grpc.CallOption) (*gen.DyListWorkspaceMembersResponse, error) {
	panic("unexpected call")
}

func TestIndividualWorkspaceUsesValveAccountQuota(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{})
	svc := NewQuotaService(&database.DB{DB: db})
	svc.SetLevelingConfig(config.LevelingQuotaConfig{Level1: 512, Level10: 1024, Level60: 5 * 1024, Level120: 10 * 1024})

	accountID := uuid.New()
	workspaceID := uuid.New()
	const mb = int64(1024 * 1024)

	// Personal file (30 MiB) + workspace file (20 MiB) share the pool.
	personalObject := database.NewID()
	workspaceObject := database.NewID()
	if err := db.Create(&database.FileObject{ID: personalObject, Size: 30 * mb, MimeType: "text/plain", Hash: "h1"}).Error; err != nil {
		t.Fatalf("create personal object: %v", err)
	}
	if err := db.Create(&database.FileObject{ID: workspaceObject, Size: 20 * mb, MimeType: "text/plain", Hash: "h2"}).Error; err != nil {
		t.Fatalf("create workspace object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "p.txt", AccountID: accountID, ObjectID: &personalObject}).Error; err != nil {
		t.Fatalf("create personal file: %v", err)
	}
	wsID := workspaceID.String()
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "w.txt", AccountID: accountID, WorkspaceID: &wsID, ObjectID: &workspaceObject}).Error; err != nil {
		t.Fatalf("create workspace file: %v", err)
	}

	// Valve serves the account quota: 2 GiB (Valve computes leveling+perk+extra).
	valveQuotaBytes := int64(2) * 1024 * 1024 * 1024
	svc.SetWorkspaceClient(&stubWorkspaceClient{
		workspace: &gen.DyWorkspace{
			Id:             workspaceID.String(),
			Type:           gen.DyWorkspaceType_INDIVIDUAL,
			Plan:           gen.DyWorkspacePlan_FREE,
			OwnerAccountId: accountID.String(),
		},
		member:    true,
		planQuota: &gen.DyWorkspacePlanQuota{Quotas: map[string]int64{"max_storage_bytes": valveQuotaBytes}},
	})

	summary, err := svc.GetWorkspaceUsage(context.Background(), workspaceID.String(), accountID.String())
	if err != nil {
		t.Fatalf("GetWorkspaceUsage() error = %v", err)
	}
	if summary.TotalBytes != valveQuotaBytes {
		t.Fatalf("total = %d, want Valve account quota %d", summary.TotalBytes, valveQuotaBytes)
	}
	if summary.UsedBytes != 50*mb {
		t.Fatalf("used = %d, want personal+workspace mix 50MiB", summary.UsedBytes)
	}
	if summary.TotalFileCount != 2 {
		t.Fatalf("file count = %d, want 2", summary.TotalFileCount)
	}
}

func TestIndividualWorkspaceFallsBackToLocalLevelingPerk(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{})
	svc := NewQuotaService(&database.DB{DB: db})
	svc.SetLevelingConfig(config.LevelingQuotaConfig{Level1: 512, Level10: 1024, Level60: 5 * 1024, Level120: 10 * 1024})
	svc.SetAccountClient(&stubAccountClient{account: &gen.DyAccount{
		Id:      uuid.New().String(),
		Profile: &gen.DyAccountProfile{Level: 60},
		PerkLevel: func() *int32 {
			v := int32(1)
			return &v
		}(),
	}})

	accountID := uuid.New()
	workspaceID := uuid.New()
	svc.SetWorkspaceClient(&stubWorkspaceClient{
		workspace: &gen.DyWorkspace{
			Id:             workspaceID.String(),
			Type:           gen.DyWorkspaceType_INDIVIDUAL,
			Plan:           gen.DyWorkspacePlan_FREE,
			OwnerAccountId: accountID.String(),
		},
		member:  true,
		planErr: errors.New("valve unavailable"),
	})

	summary, err := svc.GetWorkspaceUsage(context.Background(), workspaceID.String(), accountID.String())
	if err != nil {
		t.Fatalf("GetWorkspaceUsage() error = %v", err)
	}
	// Level 60 interpolates to 6 GiB leveling (60% of 10 GiB), perk 1 = 10 GiB
	// → 16 GiB total (the DysonFS fallback; extra quota is Valve-owned).
	want := int64(16) * 1024 * 1024 * 1024
	if summary.TotalBytes != want {
		t.Fatalf("total = %d, want fallback leveling+perk %d", summary.TotalBytes, want)
	}
}

func TestBaseQuotaFromAccount(t *testing.T) {
	tests := []struct {
		name    string
		account *gen.DyAccount
		want    int64
	}{
		{name: "level 0 no perk", account: &gen.DyAccount{Profile: &gen.DyAccountProfile{Level: 0}}, want: 512},
		{name: "level 10 no perk", account: &gen.DyAccount{Profile: &gen.DyAccountProfile{Level: 10}}, want: 1024},
		{name: "level 20 progressive", account: &gen.DyAccount{Profile: &gen.DyAccountProfile{Level: 20}}, want: 2 * 1024},
		{name: "level 60 no perk", account: &gen.DyAccount{Profile: &gen.DyAccountProfile{Level: 60}}, want: 6 * 1024},
		{name: "level 120 no perk", account: &gen.DyAccount{Profile: &gen.DyAccountProfile{Level: 120}}, want: 10 * 1024},
		{name: "level clamp high perk 2", account: &gen.DyAccount{Profile: &gen.DyAccountProfile{Level: 999}, PerkLevel: func() *int32 { v := int32(2); return &v }()}, want: 35 * 1024},
		{name: "level clamp low perk 3", account: &gen.DyAccount{Profile: &gen.DyAccountProfile{Level: -5}, PerkLevel: func() *int32 { v := int32(3); return &v }()}, want: 50*1024 + 512},
	}

	for _, tt := range tests {
		if got := baseQuotaFromAccount(tt.account); got != tt.want {
			t.Fatalf("%s: baseQuotaFromAccount() = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestEnrichedAccountRefreshesIncompleteCache(t *testing.T) {
	svc := NewQuotaService(nil)
	cache := sharedcache.NewMemoryCacheService(8)
	svc.SetCache(cache)
	client := &stubAccountClient{account: &gen.DyAccount{Id: "acct-1", Profile: &gen.DyAccountProfile{Level: 42, Experience: 12345}}}
	svc.SetAccountClient(client)

	ctx := context.Background()
	if err := cache.SetData(ctx, "quota:account:acct-1", &gen.DyAccount{Id: "acct-1"}, "DyAccount", time.Minute); err != nil {
		t.Fatalf("seed incomplete account cache: %v", err)
	}
	account := &gen.DyAccount{Id: "acct-1"}
	resolved1, err := svc.EnrichedAccount(ctx, account)
	if err != nil {
		t.Fatalf("EnrichedAccount() first call error = %v", err)
	}
	resolved2, err := svc.EnrichedAccount(ctx, account)
	if err != nil {
		t.Fatalf("EnrichedAccount() second call error = %v", err)
	}
	if resolved1.GetProfile().GetLevel() != 42 || resolved2.GetProfile().GetExperience() != 12345 {
		t.Fatalf("resolved accounts = %+v %+v, want fetched profile data", resolved1, resolved2)
	}
	if client.calls != 1 {
		t.Fatalf("profile client calls = %d, want 1 after refreshing incomplete cache", client.calls)
	}
}

func TestCreateFolderWithoutParentCreatesPrivatePermission(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FilePermission{})
	svc := NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()
	folder, err := svc.CreateFolder(accountID, "private-folder", nil)
	if err != nil {
		t.Fatalf("CreateFolder() error = %v", err)
	}
	if !folder.IsFolder || !folder.Indexed {
		t.Fatalf("folder = %+v, want indexed folder", folder)
	}
	var perm database.FilePermission
	if err := db.First(&perm, "file_id = ?", folder.ID).Error; err != nil {
		t.Fatalf("load permission: %v", err)
	}
	if perm.SubjectType != "private" || perm.Permission != "read" {
		t.Fatalf("permission = %+v, want private read", perm)
	}
}

func TestUpdateFilePermissionsAssignsIDs(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FilePermission{})
	svc := NewFileService(&database.DB{DB: db}, nil)
	fileID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "sample", AccountID: uuid.New(), Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := svc.UpdateFilePermissions(fileID, []database.FilePermission{{FileID: fileID, SubjectType: "private", Permission: "read"}}); err != nil {
		t.Fatalf("UpdateFilePermissions() error = %v", err)
	}
	var perm database.FilePermission
	if err := db.First(&perm, "file_id = ?", fileID).Error; err != nil {
		t.Fatalf("load permission: %v", err)
	}
	if perm.ID == "" {
		t.Fatalf("permission ID is empty")
	}
}

func TestLoadInheritedFilePermissionsBatchUsesCacheAndInvalidatesDescendants(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FilePermission{})
	svc := NewFileService(&database.DB{DB: db}, nil)
	svc.SetCache(sharedcache.NewMemoryCacheService(8))

	accountID := uuid.New()
	rootID := database.NewID()
	childID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: rootID, Name: "root", AccountID: accountID, Indexed: true, IsFolder: true}).Error; err != nil {
		t.Fatalf("create root: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: childID, Name: "child", AccountID: accountID, Indexed: true, IsFolder: true, ParentID: &rootID}).Error; err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := db.Create(&database.FilePermission{ID: database.NewID(), FileID: rootID, SubjectType: "private", Permission: "read"}).Error; err != nil {
		t.Fatalf("create permission: %v", err)
	}

	lookups, err := svc.loadInheritedFilePermissionsBatch([]string{childID}, "read")
	if err != nil {
		t.Fatalf("first loadInheritedFilePermissionsBatch() error = %v", err)
	}
	if lookup := lookups[childID]; !lookup.HasSource || lookup.SourceID != rootID || len(lookup.Perms) != 1 || lookup.Perms[0].SubjectType != "private" {
		t.Fatalf("first lookup = %+v, want inherited private permission from root", lookup)
	}

	if err := db.Where("file_id = ?", rootID).Delete(&database.FilePermission{}).Error; err != nil {
		t.Fatalf("delete permission directly: %v", err)
	}
	lookups, err = svc.loadInheritedFilePermissionsBatch([]string{childID}, "read")
	if err != nil {
		t.Fatalf("second loadInheritedFilePermissionsBatch() error = %v", err)
	}
	if lookup := lookups[childID]; len(lookup.Perms) != 1 || lookup.Perms[0].SubjectType != "private" {
		t.Fatalf("cached lookup = %+v, want cached private permission", lookup)
	}

	if err := svc.UpdateFilePermissions(rootID, []database.FilePermission{{FileID: rootID, SubjectType: "public", Permission: "read"}}); err != nil {
		t.Fatalf("UpdateFilePermissions() error = %v", err)
	}
	lookups, err = svc.loadInheritedFilePermissionsBatch([]string{childID}, "read")
	if err != nil {
		t.Fatalf("third loadInheritedFilePermissionsBatch() error = %v", err)
	}
	if lookup := lookups[childID]; !lookup.HasSource || lookup.SourceID != rootID || len(lookup.Perms) != 1 || lookup.Perms[0].SubjectType != "public" {
		t.Fatalf("invalidated lookup = %+v, want refreshed public permission from root", lookup)
	}
}

func TestRepairMissingReplicasCreatesReplicaOnlyForExistingRemoteObject(t *testing.T) {
	tmp := t.TempDir()
	stor := storage.NewLocalBackend(tmp)
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
	poolID := seedDefaultPool(t, db, tmp)
	svc := NewFileService(&database.DB{DB: db}, stor)
	svc.defaultPoolID = poolID
	accountID := uuid.New()
	objectID := database.NewID()
	storageKey := objectID
	if err := db.Create(&database.FileObject{ID: objectID, Size: 3, MimeType: "text/plain", Hash: "hash", Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "sample.txt", AccountID: accountID, PoolID: ptr(poolID), StorageID: ptr(poolID), ObjectID: &objectID, StorageKey: &storageKey, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := stor.Put(context.Background(), storageKey, strings.NewReader("abc"), int64(len("abc")), "text/plain"); err != nil {
		t.Fatalf("stor.Put() error = %v", err)
	}
	missingID := database.NewID()
	missingKey := missingID
	if err := db.Create(&database.FileObject{ID: missingID, Size: 4, MimeType: "text/plain", Hash: "hash2", Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
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
	var object database.FileObject
	if err := db.First(&object, "id = ?", objectID).Error; err != nil {
		t.Fatalf("load object: %v", err)
	}
	if object.StorageKey == nil || *object.StorageKey != storageKey {
		t.Fatalf("object.StorageKey = %v, want %q", object.StorageKey, storageKey)
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
	if err := local.Put(context.Background(), key, strings.NewReader("hello"), int64(len("hello")), "text/plain"); err != nil {
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

func TestListOwnedPageReturnsTopLevelFilesOnly(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FileObject{}, &database.FilePool{}); err != nil {
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
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "root-unindexed", AccountID: accountID, Indexed: false}).Error; err != nil {
		t.Fatalf("create root unindexed file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "nested", AccountID: accountID, ParentID: ptr("parent"), Indexed: false}).Error; err != nil {
		t.Fatalf("create nested file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "other", AccountID: otherID, Indexed: true}).Error; err != nil {
		t.Fatalf("create other file: %v", err)
	}

	files, total, err := svc.ListOwnedPage(accountID, FileListOptions{})
	if err != nil {
		t.Fatalf("ListOwnedPage() error = %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if got := len(files); got != 2 {
		t.Fatalf("len(ListOwnedPage()) = %d, want 2", got)
	}
	for _, f := range files {
		if f.ParentID != nil {
			t.Fatalf("expected only top-level files, got child %q", f.Name)
		}
	}

	files, total, err = svc.ListOwnedPage(accountID, FileListOptions{Offset: 1, Take: 1})
	if err != nil {
		t.Fatalf("ListOwnedPage() error = %v", err)
	}
	if total != 2 {
		t.Fatalf("paged total = %d, want 2", total)
	}
	if got := len(files); got != 1 {
		t.Fatalf("len(paged ListOwnedPage()) = %d, want 1", got)
	}
}

func TestListOwnedPagePopulatesChildrenCountAndInheritedPermissionStatus(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePermission{})
	svc := NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	rootID := database.NewID()
	childID := database.NewID()

	if err := db.Create(&database.CloudFile{ID: rootID, Name: "root", AccountID: accountID, Indexed: true}).Error; err != nil {
		t.Fatalf("create root file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: childID, Name: "child", AccountID: accountID, ParentID: ptr(rootID), Indexed: true}).Error; err != nil {
		t.Fatalf("create child file: %v", err)
	}
	perm := database.FilePermission{ID: database.NewID(), FileID: rootID, SubjectType: "private", Permission: "read"}
	if err := db.Create(&perm).Error; err != nil {
		t.Fatalf("create permission: %v", err)
	}

	files, total, err := svc.ListOwnedPage(accountID, FileListOptions{})
	if err != nil {
		t.Fatalf("ListOwnedPage() error = %v", err)
	}
	if total != 1 || len(files) != 1 || files[0].ID != rootID {
		t.Fatalf("ListOwnedPage() returned %d files (total %d), want only root", len(files), total)
	}
	if files[0].ChildrenCount != 1 {
		t.Fatalf("root ChildrenCount = %d, want 1", files[0].ChildrenCount)
	}
	if files[0].PermissionStatus.Visibility != "private" {
		t.Fatalf("root visibility = %q, want private", files[0].PermissionStatus.Visibility)
	}

	loaded, err := svc.GetFiles([]string{childID})
	if err != nil {
		t.Fatalf("GetFiles() error = %v", err)
	}
	if err := svc.populateFilesMetadata(loaded); err != nil {
		t.Fatalf("populateFilesMetadata() error = %v", err)
	}
	if loaded[0].ChildrenCount != 0 {
		t.Fatalf("child ChildrenCount = %d, want 0", loaded[0].ChildrenCount)
	}
	if loaded[0].PermissionStatus.Visibility != "private" {
		t.Fatalf("child visibility = %q, want private", loaded[0].PermissionStatus.Visibility)
	}
	if loaded[0].PermissionStatus.InheritedFrom == nil || *loaded[0].PermissionStatus.InheritedFrom != rootID {
		t.Fatalf("child inherited_from = %v, want %q", loaded[0].PermissionStatus.InheritedFrom, rootID)
	}
}

func TestListUnindexedExcludesChildren(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FileObject{}, &database.FilePool{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	if err := db.AutoMigrate(&database.FilePermission{}); err != nil {
		t.Fatalf("AutoMigrate() permission table error = %v", err)
	}

	svc := NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "root-unindexed", AccountID: accountID, Indexed: false}).Error; err != nil {
		t.Fatalf("create root unindexed file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "root-indexed", AccountID: accountID, Indexed: true}).Error; err != nil {
		t.Fatalf("create root indexed file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "child-unindexed", AccountID: accountID, ParentID: ptr("parent"), Indexed: false}).Error; err != nil {
		t.Fatalf("create child unindexed file: %v", err)
	}

	files, err := svc.ListUnindexed(accountID)
	if err != nil {
		t.Fatalf("ListUnindexed() error = %v", err)
	}
	if got := len(files); got != 1 {
		t.Fatalf("len(ListUnindexed()) = %d, want 1", got)
	}
	if files[0].ParentID != nil {
		t.Fatalf("expected only root files, got child %q", files[0].Name)
	}
	if files[0].Name != "root-unindexed" {
		t.Fatalf("ListUnindexed() returned %q, want root-unindexed", files[0].Name)
	}
}

func TestListRootOwnedDefaultsToRecentFirst(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FileObject{}, &database.FilePool{}); err != nil {
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

	files, total, err := svc.ListOwnedPage(accountID, FileListOptions{Take: 20, OrderDesc: true})
	if err != nil {
		t.Fatalf("ListOwnedPage() error = %v", err)
	}
	if total != 25 {
		t.Fatalf("total = %d, want 25", total)
	}
	if got := len(files); got != 20 {
		t.Fatalf("len(ListOwnedPage()) = %d, want 20", got)
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
	if err := db.AutoMigrate(&database.CloudFile{}, &database.FileObject{}, &database.FilePool{}); err != nil {
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
	if err := stor.Put(context.Background(), object.ID, imgFile, object.Size, object.MimeType); err != nil {
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
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
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
	if got, _ := meta["aspect_ratio"].(string); got != "16:9" {
		t.Fatalf("meta aspect_ratio = %q, want %q", got, "16:9")
	}
	if width, height := mediaDimensions(analysis.Media); width != 1920 || height != 1080 {
		t.Fatalf("mediaDimensions() = (%d, %d), want (1920, 1080)", width, height)
	}
	if !updated.Object.HasThumbnail || updated.Object.HasCompression {
		t.Fatalf("object flags = thumbnail %v compression %v, want true/false for a video", updated.Object.HasThumbnail, updated.Object.HasCompression)
	}
}

func TestRefreshStoredObjectAnalysisComputesHashAndPersists(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
	backend := storage.NewLocalBackend(t.TempDir())
	svc := NewFileService(&database.DB{DB: db}, backend)

	payload := []byte("direct-upload payload bytes for hash verification")
	key := database.NewID()
	ctx := context.Background()
	if err := backend.Put(ctx, key, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream"); err != nil {
		t.Fatalf("put object: %v", err)
	}
	objectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: objectID, Size: int64(len(payload)), MimeType: "application/octet-stream", Hash: "", Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}

	analysis, resolvedMime, err := svc.RefreshStoredObjectAnalysis(ctx, backend, objectID, key, "application/octet-stream")
	if err != nil {
		t.Fatalf("RefreshStoredObjectAnalysis() error = %v", err)
	}
	if analysis == nil {
		t.Fatal("expected a (possibly empty) analysis, got nil")
	}
	if !strings.HasPrefix(resolvedMime, "text/plain") {
		t.Fatalf("resolved mime = %q, want text/plain sniffed from payload bytes", resolvedMime)
	}
	var object database.FileObject
	if err := db.First(&object, "id = ?", objectID).Error; err != nil {
		t.Fatalf("reload object: %v", err)
	}
	sum := sha256.Sum256(payload)
	wantHash := hex.EncodeToString(sum[:])
	if object.Hash != wantHash {
		t.Fatalf("object hash = %q, want %q", object.Hash, wantHash)
	}
	if !strings.HasPrefix(object.MimeType, "text/plain") {
		t.Fatalf("object mime_type = %q, want text/plain persisted from byte sniffing", object.MimeType)
	}
	if string(object.Meta) != "{}" {
		t.Fatalf("object meta = %s, want {} for non-media payload", object.Meta)
	}
}

func TestRefreshStoredObjectAnalysisMissingObject(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
	backend := storage.NewLocalBackend(t.TempDir())
	svc := NewFileService(&database.DB{DB: db}, backend)
	if _, _, err := svc.RefreshStoredObjectAnalysis(context.Background(), backend, database.NewID(), "missing-key", "application/octet-stream"); err == nil {
		t.Fatal("expected error for missing object")
	}
}

// recordingMultipartBackend wraps a real local backend and records multipart
// aborts so tests can assert the expiry sweep released the session.
type recordingMultipartBackend struct {
	storage.Backend
	aborted []string
}

func (r *recordingMultipartBackend) PresignedPutURL(context.Context, string, time.Duration, string) (string, error) {
	return "", nil
}

func (r *recordingMultipartBackend) AbortMultipartUpload(_ context.Context, key, uploadID string) error {
	r.aborted = append(r.aborted, key+"|"+uploadID)
	return nil
}

func (r *recordingMultipartBackend) CreateMultipartUpload(context.Context, string, string) (string, error) {
	return "", nil
}

func (r *recordingMultipartBackend) PresignPartUpload(context.Context, string, string, int, time.Duration) (string, error) {
	return "", nil
}

func (r *recordingMultipartBackend) ListParts(context.Context, string, string) ([]storage.MultipartPart, error) {
	return nil, nil
}

func (r *recordingMultipartBackend) CompleteMultipartUpload(context.Context, string, string, []storage.MultipartPart) error {
	return nil
}

func seedUploadTask(t *testing.T, db *gorm.DB, taskID string, stale bool) *database.PersistentTask {
	t.Helper()
	task := &database.PersistentTask{
		ID: database.NewID(), TaskID: taskID, Name: "file.bin", Type: "file.upload",
		Status: "pending", UploadStatus: database.UploadStatusUploading, AccountID: uuid.New(),
		CreatedAt: time.Now(), UpdatedAt: time.Now(), LastActivity: time.Now(),
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("create upload task %s: %v", taskID, err)
	}
	if stale {
		// Raw SQL bypasses GORM's update-timestamp callback, which would
		// otherwise reset updated_at to now on any save.
		staleAt := time.Now().Add(-7 * time.Hour)
		if err := db.Exec("UPDATE persistent_tasks SET updated_at = ?, last_activity = ? WHERE task_id = ?", staleAt, staleAt, taskID).Error; err != nil {
			t.Fatalf("age upload task %s: %v", taskID, err)
		}
	}
	return task
}

func TestExpireStaleUploadTasks(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.PersistentTask{})
	inner := storage.NewLocalBackend(t.TempDir())
	backend := &recordingMultipartBackend{Backend: inner}
	svc := NewFileService(&database.DB{DB: db}, backend)
	ctx := context.Background()

	// Stale multipart task: session must be aborted and the orphaned object
	// removed.
	seedUploadTask(t, db, "multipart-stale", true)
	multipartKey := "uploads/multipart-stale/source"
	multipartUploadID := "upload-1"
	if err := db.Exec("UPDATE persistent_tasks SET source_key = ?, upload_id = ? WHERE task_id = ?", multipartKey, multipartUploadID, "multipart-stale").Error; err != nil {
		t.Fatalf("set multipart task keys: %v", err)
	}
	if err := inner.Put(ctx, multipartKey, bytes.NewReader([]byte("part-data")), 9, "application/octet-stream"); err != nil {
		t.Fatalf("put object: %v", err)
	}

	// Stale single-PUT task: no session to abort, object must be removed.
	seedUploadTask(t, db, "single-stale", true)
	singleKey := "uploads/single-stale/source"
	if err := db.Exec("UPDATE persistent_tasks SET source_key = ? WHERE task_id = ?", singleKey, "single-stale").Error; err != nil {
		t.Fatalf("set single task key: %v", err)
	}
	if err := inner.Put(ctx, singleKey, bytes.NewReader([]byte("whole-object")), 12, "application/octet-stream"); err != nil {
		t.Fatalf("put object: %v", err)
	}

	// Fresh task must be left alone.
	seedUploadTask(t, db, "fresh", false)

	expired, err := svc.ExpireStaleUploadTasks(ctx)
	if err != nil {
		t.Fatalf("ExpireStaleUploadTasks() error = %v", err)
	}
	if expired != 2 {
		t.Fatalf("expired = %d, want 2", expired)
	}

	var multipartReloaded database.PersistentTask
	if err := db.First(&multipartReloaded, "task_id = ?", "multipart-stale").Error; err != nil {
		t.Fatalf("reload multipart task: %v", err)
	}
	if multipartReloaded.UploadStatus != database.UploadStatusFailed || multipartReloaded.Status != "expired" {
		t.Fatalf("multipart task status = %s/%d, want expired/failed", multipartReloaded.Status, multipartReloaded.UploadStatus)
	}
	if multipartReloaded.ProcessingError == nil || !strings.Contains(*multipartReloaded.ProcessingError, "expired") {
		t.Fatalf("multipart task processing error = %v", multipartReloaded.ProcessingError)
	}
	if len(backend.aborted) != 1 || backend.aborted[0] != multipartKey+"|"+multipartUploadID {
		t.Fatalf("aborted sessions = %v, want [%s]", backend.aborted, multipartKey+"|"+multipartUploadID)
	}
	if _, err := inner.Stat(ctx, multipartKey); err == nil {
		t.Fatalf("multipart object still exists after expiry")
	}

	var singleReloaded database.PersistentTask
	if err := db.First(&singleReloaded, "task_id = ?", "single-stale").Error; err != nil {
		t.Fatalf("reload single task: %v", err)
	}
	if singleReloaded.UploadStatus != database.UploadStatusFailed {
		t.Fatalf("single task upload_status = %d, want failed", singleReloaded.UploadStatus)
	}
	if len(backend.aborted) != 1 {
		t.Fatalf("aborted sessions = %v, want only the multipart session", backend.aborted)
	}
	if _, err := inner.Stat(ctx, singleKey); err == nil {
		t.Fatalf("single-PUT object still exists after expiry")
	}

	var freshReloaded database.PersistentTask
	if err := db.First(&freshReloaded, "task_id = ?", "fresh").Error; err != nil {
		t.Fatalf("reload fresh task: %v", err)
	}
	if freshReloaded.UploadStatus != database.UploadStatusUploading {
		t.Fatalf("fresh task upload_status = %d, want uploading", freshReloaded.UploadStatus)
	}
}

func TestExpireStaleUploadTasksSkipsCompletedAndConcurrentClaim(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.PersistentTask{})
	backend := &recordingMultipartBackend{Backend: storage.NewLocalBackend(t.TempDir())}
	svc := NewFileService(&database.DB{DB: db}, backend)

	// Completed tasks are never expired even when old.
	seedUploadTask(t, db, "completed", true)
	if err := db.Exec("UPDATE persistent_tasks SET upload_status = ?, status = ? WHERE task_id = ?", database.UploadStatusCompleted, "completed", "completed").Error; err != nil {
		t.Fatalf("mark completed task: %v", err)
	}

	// A task whose status changed between the sweep's query and its claim
	// (simulated by marking it failed first) must not be touched again.
	seedUploadTask(t, db, "already-failed", true)
	if err := db.Exec("UPDATE persistent_tasks SET upload_status = ?, status = ? WHERE task_id = ?", database.UploadStatusFailed, "expired", "already-failed").Error; err != nil {
		t.Fatalf("mark failed task: %v", err)
	}

	expired, err := svc.ExpireStaleUploadTasks(context.Background())
	if err != nil {
		t.Fatalf("ExpireStaleUploadTasks() error = %v", err)
	}
	if expired != 0 {
		t.Fatalf("expired = %d, want 0", expired)
	}
	if len(backend.aborted) != 0 {
		t.Fatalf("aborted sessions = %v, want none", backend.aborted)
	}
}

func TestExpireStaleUploadTasksNeverDeletesFileObjects(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.PersistentTask{})
	inner := storage.NewLocalBackend(t.TempDir())
	backend := &recordingMultipartBackend{Backend: inner}
	svc := NewFileService(&database.DB{DB: db}, backend)
	ctx := context.Background()

	// A completed file whose object lives at its own task-derived key.
	completedKey := "uploads/completed-task/source"
	completedPayload := []byte("historic file bytes")
	if err := inner.Put(ctx, completedKey, bytes.NewReader(completedPayload), int64(len(completedPayload)), "application/octet-stream"); err != nil {
		t.Fatalf("put completed object: %v", err)
	}
	objectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: objectID, Size: int64(len(completedPayload)), MimeType: "application/octet-stream", Hash: "h", StorageKey: &completedKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create file object: %v", err)
	}
	fileID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "old.bin", AccountID: uuid.New(), ObjectID: &objectID, Indexed: true}).Error; err != nil {
		t.Fatalf("create cloud file: %v", err)
	}

	// A stale in-flight upload of the same file hash, with its own key.
	seedUploadTask(t, db, "stale-inflight", true)
	staleKey := "uploads/stale-inflight/source"
	if err := db.Exec("UPDATE persistent_tasks SET source_key = ? WHERE task_id = ?", staleKey, "stale-inflight").Error; err != nil {
		t.Fatalf("set stale task key: %v", err)
	}
	if err := inner.Put(ctx, staleKey, bytes.NewReader(completedPayload), int64(len(completedPayload)), "application/octet-stream"); err != nil {
		t.Fatalf("put stale object: %v", err)
	}

	expired, err := svc.ExpireStaleUploadTasks(ctx)
	if err != nil {
		t.Fatalf("ExpireStaleUploadTasks() error = %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}

	// The stale upload's object is gone, but the completed file's object
	// survives untouched.
	if _, err := inner.Stat(ctx, staleKey); err == nil {
		t.Fatalf("stale upload object still exists after expiry")
	}
	rc, _, err := inner.Get(ctx, completedKey)
	if err != nil {
		t.Fatalf("completed file object deleted by sweep: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, completedPayload) {
		t.Fatalf("completed file object corrupted: %q", got)
	}
}

func TestExpireStaleUploadTasksIgnoresNullUploadStatus(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.PersistentTask{})
	inner := storage.NewLocalBackend(t.TempDir())
	backend := &recordingMultipartBackend{Backend: inner}
	svc := NewFileService(&database.DB{DB: db}, backend)
	ctx := context.Background()

	// A legacy row whose upload_status is NULL (old code / raw inserts) with
	// an object under its source key, idle far beyond the expiry window.
	legacyKey := "uploads/legacy-null/source"
	staleAt := time.Now().Add(-48 * time.Hour)
	if err := db.Exec("INSERT INTO persistent_tasks (id, task_id, name, type, status, upload_status, account_id, source_key, created_at, updated_at, last_activity) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		database.NewID(), "legacy-null", "old.bin", "file.upload", "pending", nil, uuid.New(), legacyKey, time.Now(), staleAt, staleAt).Error; err != nil {
		t.Fatalf("insert legacy task: %v", err)
	}
	payload := []byte("legacy bytes")
	if err := inner.Put(ctx, legacyKey, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream"); err != nil {
		t.Fatalf("put legacy object: %v", err)
	}

	// SQL three-valued logic: upload_status = 1 never matches NULL, and the
	// claim guard repeats the same predicate, so the row and its object are
	// untouched.
	expired, err := svc.ExpireStaleUploadTasks(ctx)
	if err != nil {
		t.Fatalf("ExpireStaleUploadTasks() error = %v", err)
	}
	if expired != 0 {
		t.Fatalf("expired = %d, want 0 for NULL upload_status rows", expired)
	}
	if _, err := inner.Stat(ctx, legacyKey); err != nil {
		t.Fatalf("legacy object deleted by sweep: %v", err)
	}
	var task database.PersistentTask
	if err := db.First(&task, "task_id = ?", "legacy-null").Error; err != nil {
		t.Fatalf("reload legacy task: %v", err)
	}
	if task.UploadStatus != database.UploadStatusUnknown {
		t.Fatalf("legacy task upload_status = %d, want unchanged (0)", task.UploadStatus)
	}
}

func TestListReanalysisCandidatesIncludesVideoMetadataGaps(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
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
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{})
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
	if err := stor.Put(context.Background(), object.ID, imgFile, object.Size, object.MimeType); err != nil {
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

func TestCreateUploadTaskPersistsOverwriteID(t *testing.T) {
	db := openTestDB(t, &database.PersistentTask{}, &database.UploadChunk{})
	tasks := NewTaskService(&database.DB{DB: db})
	accountID := uuid.New()
	overwriteID := database.NewID()
	payload := &database.PersistentTask{OverwriteID: &overwriteID, FastMode: true, Indexed: true}

	task, err := tasks.CreateUploadTask(accountID, "upload", payload, 123, nil, "file.txt", "text/plain", 64, 2)
	if err != nil {
		t.Fatalf("CreateUploadTask() error = %v", err)
	}
	if task.OverwriteID == nil || *task.OverwriteID != overwriteID {
		t.Fatalf("task.OverwriteID = %v, want %q", task.OverwriteID, overwriteID)
	}

	loaded, err := tasks.GetUploadTaskWithChunks(task.TaskID)
	if err != nil {
		t.Fatalf("GetUploadTask() error = %v", err)
	}
	if loaded.OverwriteID == nil || *loaded.OverwriteID != overwriteID {
		t.Fatalf("loaded.OverwriteID = %v, want %q", loaded.OverwriteID, overwriteID)
	}
	if !loaded.FastMode {
		t.Fatal("loaded.FastMode = false, want true")
	}
}

func TestUpdateUploadedChunkIsIdempotent(t *testing.T) {
	db := openTestDB(t, &database.PersistentTask{}, &database.UploadChunk{})
	tasks := NewTaskService(&database.DB{DB: db})
	task, err := tasks.CreateUploadTask(uuid.New(), "upload", nil, 10, nil, "file.txt", "text/plain", 5, 2)
	if err != nil {
		t.Fatalf("CreateUploadTask() error = %v", err)
	}
	if err := tasks.UpdateUploadedChunk(task.TaskID, 0); err != nil {
		t.Fatalf("first chunk update: %v", err)
	}
	if err := tasks.UpdateUploadedChunk(task.TaskID, 0); err != nil {
		t.Fatalf("duplicate chunk update: %v", err)
	}
	loaded, err := tasks.GetUploadTaskWithChunks(task.TaskID)
	if err != nil {
		t.Fatalf("GetUploadTask() error = %v", err)
	}
	if loaded.ChunksUploaded != 1 || string(loaded.UploadedChunks) != "[0]" {
		t.Fatalf("task progress = count=%d chunks=%s, want count=1 chunks=[0]", loaded.ChunksUploaded, loaded.UploadedChunks)
	}
}

func TestWriteUploadChunkWritesFinalSourceAtOffsets(t *testing.T) {
	tempDir := t.TempDir()
	taskID := "upload-task"
	if _, err := WriteUploadChunk(tempDir, taskID, 2, 5, 11, strings.NewReader("!")); err != nil {
		t.Fatalf("write final chunk: %v", err)
	}
	if _, err := WriteUploadChunk(tempDir, taskID, 1, 5, 11, strings.NewReader("world")); err != nil {
		t.Fatalf("write second chunk: %v", err)
	}
	path, err := WriteUploadChunk(tempDir, taskID, 0, 5, 11, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("write first chunk: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read staged upload: %v", err)
	}
	if got, want := string(body), "helloworld!"; got != want {
		t.Fatalf("staged upload = %q, want %q", got, want)
	}
}

func TestCreateUploadedObjectIncludesSourceAnalysis(t *testing.T) {
	db := openTestDB(t, &database.FileObject{})
	svc := NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(t.TempDir()))
	info := &StagedFileInfo{Size: 42, ContentType: "image/jpeg", Hash: "abc"}
	object, err := svc.CreateUploadedObject("object-1", info, &SourceAnalysis{Image: &ImageAnalysis{Width: 80, Height: 60, Blurhash: "hash"}})
	if err != nil {
		t.Fatalf("CreateUploadedObject() error = %v", err)
	}
	if object.StorageKey == nil || *object.StorageKey != "object-1" {
		t.Fatalf("storage key = %v, want object-1", object.StorageKey)
	}
	var meta map[string]any
	if err := json.Unmarshal(object.Meta, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if meta["width"] != float64(80) || meta["height"] != float64(60) || meta["blurhash"] != "hash" {
		t.Fatalf("object metadata = %#v, want source analysis", meta)
	}
	if !object.HasCompression || object.HasThumbnail {
		t.Fatalf("object flags = compression %v thumbnail %v, want true/false for an image", object.HasCompression, object.HasThumbnail)
	}
}

func TestCreateUploadedObjectFlagsFollowMediaType(t *testing.T) {
	db := openTestDB(t, &database.FileObject{})
	svc := NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(t.TempDir()))
	cases := []struct {
		mime                   string
		thumbnail, compression bool
	}{
		{"image/jpeg", false, true},
		{"video/mp4", true, false},
		{"application/pdf", false, false},
	}
	for _, tc := range cases {
		info := &StagedFileInfo{Size: 42, ContentType: tc.mime, Hash: "abc"}
		object, err := svc.CreateUploadedObject("object-"+tc.mime, info, nil)
		if err != nil {
			t.Fatalf("CreateUploadedObject(%s) error = %v", tc.mime, err)
		}
		if object.HasThumbnail != tc.thumbnail || object.HasCompression != tc.compression {
			t.Fatalf("CreateUploadedObject(%s) flags = thumbnail %v compression %v, want %v/%v", tc.mime, object.HasThumbnail, object.HasCompression, tc.thumbnail, tc.compression)
		}
	}
}

func TestCheckUploadQuotaEnrichesAccountOnce(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{})
	svc := NewQuotaService(&database.DB{DB: db})
	accountID := uuid.New()
	client := &stubAccountClient{account: &gen.DyAccount{Id: accountID.String(), Profile: &gen.DyAccountProfile{Level: 1}}}
	svc.SetAccountClient(client)
	if err := svc.CheckUploadQuota(&gen.DyAccount{Id: accountID.String()}, 1, 1); err != nil {
		t.Fatalf("CheckUploadQuota() error = %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("profile client calls = %d, want 1", client.calls)
	}
}

// TestListPoolsIncludesNullAccountPool covers config-seeded pools whose
// account_id column is literally NULL (not the zero UUID), as produced by
// SeedPools against older schemas.
func TestListPoolsIncludesNullAccountPool(t *testing.T) {
	db := openTestDB(t, &database.FilePool{}, &database.PoolPermission{})
	svc := NewFileService(&database.DB{DB: db}, nil)
	poolID := database.NewID()
	now := time.Now()
	if err := db.Exec(
		"INSERT INTO file_pools (id, name, account_id, storage_config, billing_config, policy_config, is_hidden, created_at, updated_at) VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?)",
		poolID, "Solar Network Shared",
		datatypes.JSON([]byte(`{"bucket":"solar-network"}`)),
		datatypes.JSON([]byte(`{}`)),
		datatypes.JSON([]byte(`{"public_usable":true}`)),
		false, now, now,
	).Error; err != nil {
		t.Fatalf("insert null-account pool: %v", err)
	}

	for _, ctx := range []AccessContext{
		{},
		{Account: &gen.DyAccount{Id: uuid.New().String()}},
		{Account: &gen.DyAccount{Id: uuid.New().String(), IsSuperuser: true}},
	} {
		pools, err := svc.ListPools(ctx)
		if err != nil {
			t.Fatalf("ListPools(%#v) error = %v", ctx, err)
		}
		if len(pools) != 1 || pools[0].ID != poolID {
			t.Fatalf("ListPools(%#v) = %#v, want the null-account pool %q", ctx, pools, poolID)
		}
	}
}

func TestListPoolsReturnsAvailablePools(t *testing.T) {
	db := openTestDB(t, &database.FilePool{}, &database.PoolPermission{})
	svc := NewFileService(&database.DB{DB: db}, nil)
	ownerID := uuid.New()
	otherID := uuid.New()

	hiddenOwnedID := database.NewID() // owner-owned, hidden, public
	publicOtherID := database.NewID() // other-owned, public
	sharedOtherID := database.NewID() // other-owned, granted to owner
	plainOtherID := database.NewID()  // other-owned, private, not hidden
	systemID := database.NewID()      // config pool: zero account, hidden (like config.example.toml)

	for _, pool := range []database.FilePool{
		{ID: hiddenOwnedID, Name: "hidden-owned", AccountID: ownerID, PolicyConfig: datatypes.JSON([]byte(`{"public_usable":true}`)), IsHidden: true},
		{ID: publicOtherID, Name: "public", AccountID: otherID, PolicyConfig: datatypes.JSON([]byte(`{"public_usable":true}`))},
		{ID: sharedOtherID, Name: "shared", AccountID: otherID, PolicyConfig: datatypes.JSON([]byte(`{}`))},
		{ID: plainOtherID, Name: "private", AccountID: otherID, PolicyConfig: datatypes.JSON([]byte(`{}`))},
		{ID: systemID, Name: "system", AccountID: uuid.Nil, PolicyConfig: datatypes.JSON([]byte(`{"public_usable":true}`)), IsHidden: true},
	} {
		if err := db.Create(&pool).Error; err != nil {
			t.Fatalf("create pool %q: %v", pool.Name, err)
		}
	}
	if err := db.Create(&database.PoolPermission{
		ID: database.NewID(), PoolID: sharedOtherID, SubjectType: "account", SubjectID: ownerID.String(), Permission: "read",
	}).Error; err != nil {
		t.Fatalf("create pool permission: %v", err)
	}

	wantPools := func(t *testing.T, ctx AccessContext, want ...string) {
		t.Helper()
		pools, err := svc.ListPools(ctx)
		if err != nil {
			t.Fatalf("ListPools(%#v) error = %v", ctx, err)
		}
		got := make(map[string]bool, len(pools))
		for _, p := range pools {
			got[p.ID] = true
		}
		for _, id := range want {
			if !got[id] {
				t.Fatalf("ListPools(%#v) missing %q; got %#v", ctx, id, pools)
			}
			delete(got, id)
		}
		if len(got) != 0 {
			t.Fatalf("ListPools(%#v) returned unexpected pools: %#v", ctx, pools)
		}
	}

	// Owner sees own pools (even hidden), public pools, granted pools, and
	// the global config pool — but not other users' private pools.
	wantPools(t, AccessContext{Account: &gen.DyAccount{Id: ownerID.String()}}, hiddenOwnedID, publicOtherID, sharedOtherID, systemID)
	// Another user sees what they own plus the global pool.
	wantPools(t, AccessContext{Account: &gen.DyAccount{Id: otherID.String()}}, publicOtherID, sharedOtherID, plainOtherID, systemID)
	// Anonymous callers only see public pools and the global config pool.
	wantPools(t, AccessContext{}, publicOtherID, systemID)
	// Superusers see the same set as their account — never other users'
	// private pools; ListAllPools is the full view.
	wantPools(t, AccessContext{Account: &gen.DyAccount{Id: ownerID.String(), IsSuperuser: true}}, hiddenOwnedID, publicOtherID, sharedOtherID, systemID)

	// The global config pool must also pass the write check used by uploads
	// and getPool, despite being marked hidden in config.
	systemPool, err := svc.GetPool(systemID)
	if err != nil {
		t.Fatalf("GetPool() error = %v", err)
	}
	if !svc.CanUsePool(AccessContext{Account: &gen.DyAccount{Id: otherID.String()}}, systemPool, "write") {
		t.Fatal("non-owner cannot use global config pool")
	}

	all, err := svc.ListAllPools()
	if err != nil {
		t.Fatalf("ListAllPools() error = %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("all pools = %#v, want all five pools", all)
	}
}

func openTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
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
