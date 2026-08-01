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

// MultipartPart is a single uploaded part of a multipart direct upload.
type MultipartPart struct {
	PartNumber int
	ETag       string
	Size       int64
}

// MultipartDirectUploadBackend is implemented by DirectUploadBackends that can
// additionally coordinate S3 multipart uploads: a server-side session is
// created, each part is presigned individually, and the session is completed
// (or aborted) server-side. Pools that only implement DirectUploadBackend keep
// serving single-PUT direct uploads.
type MultipartDirectUploadBackend interface {
	DirectUploadBackend
	// CreateMultipartUpload initiates a multipart session for key and returns
	// the opaque upload id the client references when requesting part URLs.
	CreateMultipartUpload(ctx context.Context, key, contentType string) (string, error)
	// PresignPartUpload issues a presigned PUT URL for one part of a multipart
	// session.
	PresignPartUpload(ctx context.Context, key, uploadID string, partNumber int, ttl time.Duration) (string, error)
	// ListParts returns the parts uploaded so far for a session, sorted by
	// part number.
	ListParts(ctx context.Context, key, uploadID string) ([]MultipartPart, error)
	// CompleteMultipartUpload assembles the listed parts into the final object.
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []MultipartPart) error
	// AbortMultipartUpload discards the session and any uploaded parts.
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error
}

type Backend interface {
	Put(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)
	Delete(ctx context.Context, key string) error
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	List(ctx context.Context, prefix string) ([]string, error)
	SignedURL(ctx context.Context, key string, ttl time.Duration, filename string, download bool) (string, error)
}
