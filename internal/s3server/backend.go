package s3server

import (
	"context"
	"io"
	"time"
)

type ObjectInfo struct {
	Size     int64
	ModTime  time.Time
	MimeType string
	ETag     string
}

type BucketInfo struct {
	Name         string
	CreationDate time.Time
}

type Backend interface {
	ListBuckets(ctx context.Context) ([]BucketInfo, error)
	HeadBucket(ctx context.Context, bucket string) error
	CreateBucket(ctx context.Context, bucket string) error
	DeleteBucket(ctx context.Context, bucket string) error
	ListObjects(ctx context.Context, bucket, prefix, marker string, maxKeys int) ([]ObjectEntry, bool, error)
	GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, ObjectInfo, error)
	PutObject(ctx context.Context, bucket, key string, reader io.Reader, contentType string) error
	DeleteObject(ctx context.Context, bucket, key string) error
	StatObject(ctx context.Context, bucket, key string) (ObjectInfo, error)
	SignedURL(ctx context.Context, bucket, key string, ttl time.Duration, filename string, download bool) (string, error)
}
