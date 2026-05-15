package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type LocalBackend struct {
	base string
}

func NewLocalBackend(base string) *LocalBackend { return &LocalBackend{base: base} }

func (b *LocalBackend) path(key string) string { return filepath.Join(b.base, filepath.Clean(key)) }

func (b *LocalBackend) Put(_ context.Context, key string, reader io.Reader, _ string) error {
	if err := os.MkdirAll(filepath.Dir(b.path(key)), 0o755); err != nil {
		return err
	}
	file, err := os.Create(b.path(key))
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, reader)
	return err
}

func (b *LocalBackend) Get(_ context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	file, err := os.Open(b.path(key))
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, ObjectInfo{}, err
	}
	return file, ObjectInfo{Size: stat.Size(), ModTime: stat.ModTime()}, nil
}

func (b *LocalBackend) Delete(_ context.Context, key string) error { return os.Remove(b.path(key)) }

func (b *LocalBackend) Stat(_ context.Context, key string) (ObjectInfo, error) {
	stat, err := os.Stat(b.path(key))
	if err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{Size: stat.Size(), ModTime: stat.ModTime()}, nil
}

func (b *LocalBackend) SignedURL(_ context.Context, key string, _ time.Duration, filename string, download bool) (string, error) {
	mode := "inline"
	if download {
		mode = "download"
	}
	return fmt.Sprintf("file://%s?name=%s&mode=%s", b.path(key), filename, mode), nil
}
