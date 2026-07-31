package storage

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

// DirectUploadBackend is implemented by object-storage backends that can issue
// browser-upload URLs without proxying the file through DysonFS.
type DirectUploadBackend interface {
	Backend
	PresignedPutURL(ctx context.Context, key string, ttl time.Duration, contentType string) (string, error)
}

type Backend interface {
	Put(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)
	Delete(ctx context.Context, key string) error
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	List(ctx context.Context, prefix string) ([]string, error)
	SignedURL(ctx context.Context, key string, ttl time.Duration, filename string, download bool) (string, error)
}
