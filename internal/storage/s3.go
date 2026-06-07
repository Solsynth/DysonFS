package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"sort"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3Backend struct {
	client *minio.Client
	bucket string
	secure bool
}

func NewS3Backend(endpoint, accessKey, secretKey, bucket string, secure bool) (*S3Backend, error) {
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: secure})
	if err != nil {
		return nil, err
	}
	return &S3Backend{client: client, bucket: bucket, secure: secure}, nil
}

func (b *S3Backend) Put(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	if b == nil || b.client == nil {
		return fmt.Errorf("s3 backend not configured")
	}
	_, err := b.client.PutObject(ctx, b.bucket, key, reader, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (b *S3Backend) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if b == nil || b.client == nil {
		return nil, ObjectInfo{}, fmt.Errorf("s3 backend not configured")
	}
	obj, err := b.client.GetObject(ctx, b.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	stat, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, ObjectInfo{}, err
	}
	return obj, ObjectInfo{Size: stat.Size, ModTime: stat.LastModified, MimeType: stat.ContentType, ETag: stat.ETag}, nil
}

func (b *S3Backend) Delete(ctx context.Context, key string) error {
	if b == nil || b.client == nil {
		return fmt.Errorf("s3 backend not configured")
	}
	return b.client.RemoveObject(ctx, b.bucket, key, minio.RemoveObjectOptions{})
}

func (b *S3Backend) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if b == nil || b.client == nil {
		return ObjectInfo{}, fmt.Errorf("s3 backend not configured")
	}
	stat, err := b.client.StatObject(ctx, b.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{Size: stat.Size, ModTime: stat.LastModified, MimeType: stat.ContentType, ETag: stat.ETag}, nil
}

func (b *S3Backend) List(ctx context.Context, prefix string) ([]string, error) {
	if b == nil || b.client == nil {
		return nil, fmt.Errorf("s3 backend not configured")
	}
	ch := b.client.ListObjects(ctx, b.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})
	keys := make([]string, 0)
	for obj := range ch {
		if obj.Err != nil {
			return nil, obj.Err
		}
		keys = append(keys, obj.Key)
	}
	sort.Strings(keys)
	return keys, nil
}

func (b *S3Backend) SignedURL(ctx context.Context, key string, ttl time.Duration, filename string, download bool) (string, error) {
	if b == nil || b.client == nil {
		return "", fmt.Errorf("s3 backend not configured")
	}
	headers := url.Values{}
	mode := "inline"
	if download {
		mode = "attachment"
		headers.Set("response-content-disposition", fmt.Sprintf(`%s; filename=%q`, mode, filename))
	}
	params := make(url.Values)
	for k, v := range headers {
		for _, item := range v {
			params.Add(k, item)
		}
	}
	urlObj, err := b.client.PresignedGetObject(ctx, b.bucket, path.Clean(key), ttl, params)
	if err != nil {
		return "", err
	}
	return urlObj.String(), nil
}
