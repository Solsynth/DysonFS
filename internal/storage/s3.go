package storage

import (
	"context"
	"fmt"
	"io"
	"time"
)

type S3Backend struct{}

func NewS3Backend() *S3Backend { return &S3Backend{} }

func (b *S3Backend) Put(context.Context, string, io.Reader, string) error { return nil }
func (b *S3Backend) Get(context.Context, string) (io.ReadCloser, ObjectInfo, error) {
	return nil, ObjectInfo{}, fmt.Errorf("s3 backend not configured")
}
func (b *S3Backend) Delete(context.Context, string) error { return nil }
func (b *S3Backend) Stat(context.Context, string) (ObjectInfo, error) {
	return ObjectInfo{}, fmt.Errorf("s3 backend not configured")
}
func (b *S3Backend) SignedURL(context.Context, string, time.Duration, string, bool) (string, error) {
	return "", fmt.Errorf("s3 backend not configured")
}
