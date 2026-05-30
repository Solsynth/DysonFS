package s3server

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

type StorageBackend struct {
	inner storage.Backend
}

func NewStorageBackend(inner storage.Backend) *StorageBackend {
	return &StorageBackend{inner: inner}
}

func (b *StorageBackend) ListBuckets(ctx context.Context) ([]BucketInfo, error) {
	keys, err := b.inner.List(ctx, "")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var buckets []BucketInfo
	for _, key := range keys {
		parts := strings.SplitN(key, "/", 2)
		if len(parts) == 0 {
			continue
		}
		name := parts[0]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		buckets = append(buckets, BucketInfo{Name: name, CreationDate: time.Now()})
	}
	if len(buckets) == 0 {
		buckets = []BucketInfo{{Name: "default", CreationDate: time.Now()}}
	}
	return buckets, nil
}

func (b *StorageBackend) HeadBucket(ctx context.Context, bucket string) error {
	return nil
}

func (b *StorageBackend) CreateBucket(ctx context.Context, bucket string) error {
	return nil
}

func (b *StorageBackend) DeleteBucket(ctx context.Context, bucket string) error {
	keys, err := b.inner.List(ctx, "")
	if err != nil {
		return err
	}
	for _, key := range keys {
		if strings.HasPrefix(key, bucket+"/") || key == bucket {
			return fmt.Errorf("bucket is not empty")
		}
	}
	return nil
}

func (b *StorageBackend) ListObjects(ctx context.Context, bucket, prefix, marker string, maxKeys int) ([]ObjectEntry, bool, error) {
	fullPrefix := bucket + "/"
	if prefix != "" {
		fullPrefix = bucket + "/" + prefix
	}
	keys, err := b.inner.List(ctx, fullPrefix)
	if err != nil {
		return nil, false, err
	}
	sort.Strings(keys)

	if marker != "" {
		fullMarker := bucket + "/" + marker
		filtered := make([]string, 0, len(keys))
		for _, k := range keys {
			if k > fullMarker {
				filtered = append(filtered, k)
			}
		}
		keys = filtered
	}

	entries := make([]ObjectEntry, 0)
	for _, fullKey := range keys {
		if len(entries) >= maxKeys {
			break
		}
		key := strings.TrimPrefix(fullKey, bucket+"/")
		info, err := b.inner.Stat(ctx, fullKey)
		if err != nil {
			continue
		}
		etag := info.ETag
		if etag == "" {
			etag = "\"" + key + "\""
		}
		entries = append(entries, ObjectEntry{
			Key:          key,
			LastModified: info.ModTime.UTC().Format("2006-01-02T15:04:05.000Z"),
			Size:         info.Size,
			ETag:         etag,
			StorageClass: "STANDARD",
		})
	}
	return entries, false, nil
}

func (b *StorageBackend) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, ObjectInfo, error) {
	fullKey := bucket + "/" + key
	reader, info, err := b.inner.Get(ctx, fullKey)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	return reader, ObjectInfo{Size: info.Size, ModTime: info.ModTime, MimeType: info.MimeType, ETag: info.ETag}, nil
}

func (b *StorageBackend) PutObject(ctx context.Context, bucket, key string, reader io.Reader, contentType string) error {
	fullKey := bucket + "/" + key
	return b.inner.Put(ctx, fullKey, reader, contentType)
}

func (b *StorageBackend) DeleteObject(ctx context.Context, bucket, key string) error {
	fullKey := bucket + "/" + key
	return b.inner.Delete(ctx, fullKey)
}

func (b *StorageBackend) StatObject(ctx context.Context, bucket, key string) (ObjectInfo, error) {
	fullKey := bucket + "/" + key
	info, err := b.inner.Stat(ctx, fullKey)
	if err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{Size: info.Size, ModTime: info.ModTime, MimeType: info.MimeType, ETag: info.ETag}, nil
}

func (b *StorageBackend) SignedURL(ctx context.Context, bucket, key string, ttl time.Duration, filename string, download bool) (string, error) {
	fullKey := bucket + "/" + key
	return b.inner.SignedURL(ctx, fullKey, ttl, filename, download)
}
