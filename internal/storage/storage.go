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

type Backend interface {
	Put(ctx context.Context, key string, reader io.Reader, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)
	Delete(ctx context.Context, key string) error
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	SignedURL(ctx context.Context, key string, ttl time.Duration, filename string, download bool) (string, error)
}
