package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kr/binarydist"
	"gorm.io/gorm"
	"src.solsynth.dev/sosys/filesystem/internal/database"
)

var (
	ErrNotLocked   = errors.New("file is not locked; acquire a lock before patching")
	ErrPatchFailed = errors.New("failed to apply binary patch")
)

func (s *FileService) ResolveStorageKey(file *database.CloudFile) string {
	if file.StorageKey != nil && strings.TrimSpace(*file.StorageKey) != "" {
		return strings.TrimSpace(*file.StorageKey)
	}
	if file.Object != nil && file.Object.StorageKey != nil && strings.TrimSpace(*file.Object.StorageKey) != "" {
		return strings.TrimSpace(*file.Object.StorageKey)
	}
	if file.ObjectID != nil && strings.TrimSpace(*file.ObjectID) != "" {
		return strings.TrimSpace(*file.ObjectID)
	}
	return ""
}

// OverwriteInPlace uploads new content to the existing storage key, updates the
// FileObject's size, and marks it for deferred rehash (hash, MIME, derived content).
// No new FileObject is created. This is the fast path for WebDAV PUT, WOPI save,
// and binary patch.
func (s *FileService) OverwriteInPlace(ctx context.Context, fileID string, reader io.Reader) (*database.CloudFile, error) {
	file, err := s.GetFile(fileID)
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}
	if file.IsFolder {
		return nil, fmt.Errorf("cannot overwrite a folder")
	}
	if file.ObjectID == nil || strings.TrimSpace(*file.ObjectID) == "" {
		return nil, fmt.Errorf("file has no backing object")
	}

	key := s.ResolveStorageKey(file)
	if key == "" {
		return nil, fmt.Errorf("file storage key missing")
	}
	backend, err := s.BackendForFile(file)
	if err != nil {
		return nil, fmt.Errorf("resolve backend: %w", err)
	}

	tempFile, err := os.CreateTemp("", "dysonfs-overwrite-*")
	if err != nil {
		return nil, fmt.Errorf("create temp: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	size, err := io.Copy(tempFile, reader)
	if err != nil {
		_ = tempFile.Close()
		return nil, fmt.Errorf("write temp: %w", err)
	}
	_ = tempFile.Close()

	stage, err := os.Open(tempPath)
	if err != nil {
		return nil, fmt.Errorf("open for storage: %w", err)
	}
	defer stage.Close()
	if err := backend.Put(ctx, key, stage, "application/octet-stream"); err != nil {
		return nil, fmt.Errorf("upload to storage: %w", err)
	}

	objectID := strings.TrimSpace(*file.ObjectID)
	if err := s.db.DB.Transaction(func(tx *gorm.DB) error {
		return tx.Model(&database.FileObject{}).Where("id = ?", objectID).Updates(map[string]any{
			"size":         size,
			"needs_rehash": true,
			"updated_at":   time.Now(),
		}).Error
	}); err != nil {
		return nil, fmt.Errorf("update object: %w", err)
	}

	return s.GetFile(fileID)
}

// ApplyPatch reads the original file content from storage, applies a binary diff patch
// (bsdiff format), and overwrites the file in-place via OverwriteInPlace.
// The file must have an active lock.
func (s *FileService) ApplyPatch(ctx context.Context, fileID string, patchData io.Reader, lockToken string) (*database.CloudFile, error) {
	file, err := s.GetFile(fileID)
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}
	if file.IsFolder {
		return nil, fmt.Errorf("cannot patch a folder")
	}
	if file.ObjectID == nil || strings.TrimSpace(*file.ObjectID) == "" {
		return nil, fmt.Errorf("file has no backing object")
	}

	lock, err := s.GetLock(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("check lock: %w", err)
	}
	if lock == nil {
		return nil, ErrNotLocked
	}
	if lockToken != "" && lock.LockToken != lockToken {
		return nil, ErrNotLocked
	}

	key := s.ResolveStorageKey(file)
	if key == "" {
		return nil, fmt.Errorf("file storage key missing")
	}
	backend, err := s.BackendForFile(file)
	if err != nil {
		return nil, fmt.Errorf("resolve backend: %w", err)
	}

	original, _, err := backend.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("read original: %w", err)
	}
	defer original.Close()

	tempFile, err := os.CreateTemp("", "dysonfs-patch-*")
	if err != nil {
		return nil, fmt.Errorf("create temp: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	patchErr := make(chan error, 1)
	go func() {
		err := binarydist.Patch(original, tempFile, patchData)
		_ = tempFile.Close()
		patchErr <- err
	}()

	if pErr := <-patchErr; pErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrPatchFailed, pErr)
	}

	patchedFile, err := os.Open(tempPath)
	if err != nil {
		return nil, fmt.Errorf("open patched: %w", err)
	}
	defer patchedFile.Close()

	return s.OverwriteInPlace(ctx, fileID, patchedFile)
}
