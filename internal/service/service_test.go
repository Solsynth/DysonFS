package service

import (
	"context"
	"encoding/json"
	"errors"
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

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	sharedcache "src.solsynth.dev/sosys/go/pkg/cache"
	gen "src.solsynth.dev/sosys/go/proto"
)

type stubProfileClient struct {
	account *gen.DyAccount
	calls   int
}

func (s *stubProfileClient) GetAccount(context.Context, *gen.DyGetAccountRequest, ...grpc.CallOption) (*gen.DyAccount, error) {
	s.calls++
	return s.account, nil
}

func (s *stubProfileClient) GetAccountBatch(context.Context, *gen.DyGetAccountBatchRequest, ...grpc.CallOption) (*gen.DyGetAccountBatchResponse, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) GetBotAccountBatch(context.Context, *gen.DyGetBotAccountBatchRequest, ...grpc.CallOption) (*gen.DyGetAccountBatchResponse, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) GetBotAccount(context.Context, *gen.DyGetBotAccountRequest, ...grpc.CallOption) (*gen.DyAccount, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) LookupAccountBatch(context.Context, *gen.DyLookupAccountBatchRequest, ...grpc.CallOption) (*gen.DyGetAccountBatchResponse, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) SearchAccount(context.Context, *gen.DySearchAccountRequest, ...grpc.CallOption) (*gen.DyGetAccountBatchResponse, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) ListAccounts(context.Context, *gen.DyListAccountsRequest, ...grpc.CallOption) (*gen.DyListAccountsResponse, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) GetProfile(context.Context, *gen.DyGetProfileRequest, ...grpc.CallOption) (*gen.DyAccountProfile, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) UpdateProfile(context.Context, *gen.DyUpdateProfileRequest, ...grpc.CallOption) (*gen.DyAccountProfile, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) ListBadges(context.Context, *gen.DyListBadgesRequest, ...grpc.CallOption) (*gen.DyListBadgesResponse, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) GrantBadge(context.Context, *gen.DyGrantBadgeRequest, ...grpc.CallOption) (*gen.DyGrantBadgeResponse, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) GetBadge(context.Context, *gen.DyGetBadgeRequest, ...grpc.CallOption) (*gen.DyGetBadgeResponse, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) UpdateBadge(context.Context, *gen.DyUpdateBadgeRequest, ...grpc.CallOption) (*gen.DyUpdateBadgeResponse, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) GetRelationship(context.Context, *gen.DyGetRelationshipRequest, ...grpc.CallOption) (*gen.DyGetRelationshipResponse, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) HasRelationship(context.Context, *gen.DyGetRelationshipRequest, ...grpc.CallOption) (*wrapperspb.BoolValue, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) ListFriends(context.Context, *gen.DyListRelationshipSimpleRequest, ...grpc.CallOption) (*gen.DyListRelationshipSimpleResponse, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) ListBlocked(context.Context, *gen.DyListRelationshipSimpleRequest, ...grpc.CallOption) (*gen.DyListRelationshipSimpleResponse, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) GetAccountStatus(context.Context, *gen.DyGetAccountRequest, ...grpc.CallOption) (*gen.DyAccountStatus, error) {
	panic("unexpected call")
}

func (s *stubProfileClient) GetAccountStatusBatch(context.Context, *gen.DyGetAccountBatchRequest, ...grpc.CallOption) (*gen.DyGetAccountStatusBatchResponse, error) {
	panic("unexpected call")
}

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
	storageKey := parentID + ".thumbnail"
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
	file, err := svc.CreateUploadedFile(uuid.New(), "sample.txt", &description, nil, nil, nil, nil, objectID, nil, nil, &storageKey, false)
	if err != nil {
		t.Fatalf("CreateUploadedFile() error = %v", err)
	}
	if file.Description == nil || *file.Description != description {
		t.Fatalf("file.Description = %v, want %q", file.Description, description)
	}
}

func TestPurgeFileDeletesDereferencedObjectAndRemote(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{}, &database.FilePermission{})
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
	if err := db.Create(&database.FileReplica{ID: database.NewID(), ObjectID: objectID, PoolID: ptr(poolID), StorageID: ptr(poolID), Status: "primary", IsPrimary: true}).Error; err != nil {
		t.Fatalf("create replica: %v", err)
	}
	if err := db.Create(&database.FilePermission{ID: database.NewID(), FileID: fileID, SubjectType: "private", Permission: "read"}).Error; err != nil {
		t.Fatalf("create permission: %v", err)
	}
	if err := stor.Put(context.Background(), storageKey, strings.NewReader("hello"), "text/plain"); err != nil {
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
	var replicaCount int64
	if err := db.Unscoped().Model(&database.FileReplica{}).Where("object_id = ?", objectID).Count(&replicaCount).Error; err != nil {
		t.Fatalf("count replicas: %v", err)
	}
	if replicaCount != 0 {
		t.Fatalf("replica count = %d, want 0", replicaCount)
	}
	if _, err := os.Stat(filepath.Join(tmp, storageKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remote object still exists, stat err = %v", err)
	}
}

func TestPurgeFileKeepsSharedObjectAndRemote(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FileReplica{}, &database.FilePool{}, &database.FilePermission{})
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
	if err := db.Create(&database.FileReplica{ID: database.NewID(), ObjectID: objectID, PoolID: ptr(poolID), StorageID: ptr(poolID), Status: "primary", IsPrimary: true}).Error; err != nil {
		t.Fatalf("create replica: %v", err)
	}
	if err := stor.Put(context.Background(), storageKey, strings.NewReader("hello"), "text/plain"); err != nil {
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

	count, err := svc.MoveBatch([]string{childID, siblingID}, nil)
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

	if _, err := svc.MoveBatch([]string{cycleParentID}, ptr(cycleChildID)); err == nil {
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

func TestQuotaUsageCountsBytes(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.QuotaRecord{})
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
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.QuotaRecord{})
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
	if err := db.Create(&database.QuotaRecord{ID: database.NewID(), AccountID: accountID, Name: "bonus", Description: "bonus", Quota: 25}).Error; err != nil {
		t.Fatalf("create quota record: %v", err)
	}
	account := &gen.DyAccount{Id: accountID.String(), Profile: &gen.DyAccountProfile{Level: 60}, PerkLevel: func() *int32 { v := int32(1); return &v }()}
	svc.SetLevelingConfig(config.LevelingQuotaConfig{Level1: 512, Level10: 1024, Level60: 5 * 1024, Level120: 10 * 1024})
	summary, err := svc.GetSummary(account)
	if err != nil {
		t.Fatalf("GetSummary() error = %v", err)
	}
	if summary.BasedQuota != 16*1024 || summary.LevelingQuota != 6*1024 || summary.PerkQuota != 10*1024 || summary.ExtraQuota != 25 || summary.TotalQuota != 16*1024+25 {
		t.Fatalf("summary = %+v, want base=16GiB leveling=6GiB perk=10GiB extra=25 total=16GiB+25MB", summary)
	}

	if err := svc.CheckUploadQuota(account, 17000*mb, 1); err == nil {
		t.Fatal("CheckUploadQuota() error = nil, want quota exceeded")
	} else if !errors.Is(err, ErrQuotaExceeded) || !strings.Contains(err.Error(), "used=120MB") || !strings.Contains(err.Error(), "total=16409MB") {
		t.Fatalf("CheckUploadQuota() error = %v, want used/total details", err)
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

func TestEnrichedAccountUsesCache(t *testing.T) {
	svc := NewQuotaService(nil)
	svc.SetCache(sharedcache.NewMemoryCacheService(8))
	client := &stubProfileClient{account: &gen.DyAccount{Id: "acct-1", Profile: &gen.DyAccountProfile{Level: 42, Experience: 12345}}}
	svc.SetProfileClient(client)

	account := &gen.DyAccount{Id: "acct-1"}
	resolved1, err := svc.EnrichedAccount(context.Background(), account)
	if err != nil {
		t.Fatalf("EnrichedAccount() first call error = %v", err)
	}
	resolved2, err := svc.EnrichedAccount(context.Background(), account)
	if err != nil {
		t.Fatalf("EnrichedAccount() second call error = %v", err)
	}
	if resolved1.GetProfile().GetLevel() != 42 || resolved2.GetProfile().GetExperience() != 12345 {
		t.Fatalf("resolved accounts = %+v %+v, want fetched profile data", resolved1, resolved2)
	}
	if client.calls != 1 {
		t.Fatalf("profile client calls = %d, want 1", client.calls)
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

func TestListOwnedPopulatesChildrenCountAndInheritedPermissionStatus(t *testing.T) {
	db := openTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePermission{})
	svc := NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	rootID := database.NewID()
	childID := database.NewID()
	grandchildID := database.NewID()

	if err := db.Create(&database.CloudFile{ID: rootID, Name: "root", AccountID: accountID, Indexed: true}).Error; err != nil {
		t.Fatalf("create root file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: childID, Name: "child", AccountID: accountID, ParentID: ptr(rootID), Indexed: true}).Error; err != nil {
		t.Fatalf("create child file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: grandchildID, Name: "grandchild", AccountID: accountID, ParentID: ptr(childID), Indexed: true}).Error; err != nil {
		t.Fatalf("create grandchild file: %v", err)
	}
	perm := database.FilePermission{ID: database.NewID(), FileID: rootID, SubjectType: "private", Permission: "read"}
	if err := db.Create(&perm).Error; err != nil {
		t.Fatalf("create permission: %v", err)
	}

	files, err := svc.ListOwned(accountID)
	if err != nil {
		t.Fatalf("ListOwned() error = %v", err)
	}

	byID := make(map[string]database.CloudFile, len(files))
	for _, file := range files {
		byID[file.ID] = file
	}
	if byID[rootID].ChildrenCount != 1 {
		t.Fatalf("root ChildrenCount = %d, want 1", byID[rootID].ChildrenCount)
	}
	if byID[childID].ChildrenCount != 1 {
		t.Fatalf("child ChildrenCount = %d, want 1", byID[childID].ChildrenCount)
	}
	if byID[grandchildID].ChildrenCount != 0 {
		t.Fatalf("grandchild ChildrenCount = %d, want 0", byID[grandchildID].ChildrenCount)
	}
	if byID[childID].PermissionStatus.Visibility != "private" {
		t.Fatalf("child visibility = %q, want private", byID[childID].PermissionStatus.Visibility)
	}
	if byID[childID].PermissionStatus.InheritedFrom == nil || *byID[childID].PermissionStatus.InheritedFrom != rootID {
		t.Fatalf("child inherited_from = %v, want %q", byID[childID].PermissionStatus.InheritedFrom, rootID)
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

func TestListUnindexedExcludesChildren(t *testing.T) {
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
