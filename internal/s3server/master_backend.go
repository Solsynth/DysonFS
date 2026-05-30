package s3server

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	gen "src.solsynth.dev/sosys/go/proto"

	"github.com/google/uuid"
)

type MasterBackend struct {
	files     *service.FileService
	accountID uuid.UUID
	tempDir   string
}

func NewMasterBackend(files *service.FileService, accountID uuid.UUID, tempDir string) *MasterBackend {
	return &MasterBackend{files: files, accountID: accountID, tempDir: tempDir}
}

func (b *MasterBackend) ListBuckets(ctx context.Context) ([]BucketInfo, error) {
	pools, err := b.files.ListPools(service.AccessContext{
		Account: &gen.DyAccount{Id: b.accountID.String(), IsSuperuser: true},
	})
	if err != nil {
		return nil, err
	}

	buckets := []BucketInfo{
		{Name: "auto", CreationDate: time.Now()},
		{Name: "unindexed", CreationDate: time.Now()},
	}
	for _, p := range pools {
		buckets = append(buckets, BucketInfo{Name: p.ID, CreationDate: time.Now()})
	}
	return buckets, nil
}

func (b *MasterBackend) HeadBucket(ctx context.Context, bucket string) error {
	switch bucket {
	case "auto", "unindexed":
		return nil
	default:
		_, err := b.files.GetPool(bucket)
		return err
	}
}

func (b *MasterBackend) CreateBucket(ctx context.Context, bucket string) error {
	_, err := b.files.CreatePool(b.accountID, bucket, "", service.PoolStorageConfig{}, service.PoolBillingConfig{}, service.PoolConfig{}, false)
	return err
}

func (b *MasterBackend) DeleteBucket(ctx context.Context, bucket string) error {
	switch bucket {
	case "auto", "unindexed":
		return fmt.Errorf("cannot delete special bucket")
	default:
		return b.files.DeletePool(bucket)
	}
}

func (b *MasterBackend) ListObjects(ctx context.Context, bucket, prefix, marker string, maxKeys int) ([]ObjectEntry, bool, error) {
	var files []database.CloudFile
	var err error

	switch bucket {
	case "auto":
		files, err = b.files.ListRoot(b.accountID)
	case "unindexed":
		files, err = b.files.ListUnindexed(b.accountID)
	default:
		files, err = b.listFilesInPool(bucket)
	}
	if err != nil {
		return nil, false, err
	}

	entries := make([]ObjectEntry, 0, len(files))
	for _, f := range files {
		if f.IsFolder {
			continue
		}
		key := f.ID
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			continue
		}
		if marker != "" && key <= marker {
			continue
		}
		if len(entries) >= maxKeys {
			return entries, true, nil
		}
		size := int64(0)
		modTime := f.UpdatedAt
		etag := "\"" + f.ID + "\""
		if f.Object != nil {
			size = f.Object.Size
			if f.Object.Hash != "" {
				etag = "\"" + f.Object.Hash + "\""
			}
		}
		entries = append(entries, ObjectEntry{
			Key:          key,
			LastModified: modTime.UTC().Format("2006-01-02T15:04:05.000Z"),
			Size:         size,
			ETag:         etag,
			StorageClass: "STANDARD",
		})
	}
	return entries, false, nil
}

func (b *MasterBackend) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, ObjectInfo, error) {
	file, err := b.resolveFile(bucket, key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	storageKey := b.fileStorageKey(file)
	if storageKey == "" {
		return nil, ObjectInfo{}, fmt.Errorf("file has no storage key")
	}
	backend, err := b.files.BackendForFile(file)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	reader, info, err := backend.Get(ctx, storageKey)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	return reader, ObjectInfo{Size: info.Size, ModTime: info.ModTime, MimeType: info.MimeType, ETag: info.ETag}, nil
}

func (b *MasterBackend) PutObject(ctx context.Context, bucket, key string, reader io.Reader, contentType string) error {
	var poolID *string
	switch bucket {
	case "auto":
		poolID = nil
	case "unindexed":
		return fmt.Errorf("cannot upload to unindexed bucket")
	default:
		poolID = &bucket
	}

	if err := os.MkdirAll(b.tempDir, 0o755); err != nil {
		return err
	}
	tempPath := filepath.Join(b.tempDir, database.NewID()+".s3upload")
	f, err := os.Create(tempPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, reader); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Close()
	defer os.Remove(tempPath)

	stage, err := os.Open(tempPath)
	if err != nil {
		return err
	}
	object, err := b.files.StreamToStorage(ctx, stage, contentType)
	_ = stage.Close()
	if err != nil {
		return err
	}

	storageKey := &object.ID
	fileName := key
	if fileName == "" {
		fileName = "upload-" + object.ID
	}

	indexed := bucket != "unindexed"
	_, err = b.files.CreateUploadedFile(b.accountID, fileName, nil, nil, nil, nil, nil, object.ID, poolID, nil, storageKey, indexed)
	return err
}

func (b *MasterBackend) DeleteObject(ctx context.Context, bucket, key string) error {
	file, err := b.resolveFile(bucket, key)
	if err != nil {
		return err
	}
	return b.files.PurgeFile(file.ID)
}

func (b *MasterBackend) StatObject(ctx context.Context, bucket, key string) (ObjectInfo, error) {
	file, err := b.resolveFile(bucket, key)
	if err != nil {
		return ObjectInfo{}, err
	}
	size := int64(0)
	modTime := file.UpdatedAt
	mimeType := file.ResponseMimeType()
	etag := "\"" + file.ID + "\""
	if file.Object != nil {
		size = file.Object.Size
		if file.Object.Hash != "" {
			etag = "\"" + file.Object.Hash + "\""
		}
	}
	return ObjectInfo{Size: size, ModTime: modTime, MimeType: mimeType, ETag: etag}, nil
}

func (b *MasterBackend) SignedURL(ctx context.Context, bucket, key string, ttl time.Duration, filename string, download bool) (string, error) {
	file, err := b.resolveFile(bucket, key)
	if err != nil {
		return "", err
	}
	storageKey := b.fileStorageKey(file)
	if storageKey == "" {
		return "", fmt.Errorf("file has no storage key")
	}
	backend, err := b.files.BackendForFile(file)
	if err != nil {
		return "", err
	}
	return backend.SignedURL(ctx, storageKey, ttl, filename, download)
}

func (b *MasterBackend) resolveFile(bucket, key string) (*database.CloudFile, error) {
	file, err := b.files.GetFile(key)
	if err != nil {
		return nil, err
	}
	switch bucket {
	case "auto":
		if file.AccountID != b.accountID || !file.Indexed {
			return nil, fmt.Errorf("file not found in auto bucket")
		}
	case "unindexed":
		if file.AccountID != b.accountID || file.Indexed {
			return nil, fmt.Errorf("file not found in unindexed bucket")
		}
	default:
		if file.PoolID == nil || *file.PoolID != bucket {
			return nil, fmt.Errorf("file not found in pool %s", bucket)
		}
	}
	return file, nil
}

func (b *MasterBackend) fileStorageKey(file *database.CloudFile) string {
	if file.StorageKey != nil && strings.TrimSpace(*file.StorageKey) != "" {
		return strings.TrimSpace(*file.StorageKey)
	}
	if file.Object != nil && file.Object.StorageKey != nil && strings.TrimSpace(*file.Object.StorageKey) != "" {
		return strings.TrimSpace(*file.Object.StorageKey)
	}
	return ""
}

func (b *MasterBackend) listFilesInPool(poolID string) ([]database.CloudFile, error) {
	var files []database.CloudFile
	if err := b.files.DB().Preload("Object").
		Where("pool_id = ? AND deleted_at IS NULL AND is_folder = false", poolID).
		Find(&files).Error; err != nil {
		return nil, err
	}
	return files, nil
}

var _ Backend = (*MasterBackend)(nil)
