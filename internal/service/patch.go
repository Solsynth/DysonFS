package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kr/binarydist"
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

// ApplyPatch reads the original file content from storage, applies a binary diff patch
// (bsdiff format), and streams the result to storage as a new object. The file's DB
// record is updated to point to the new object. Old derived children (thumbnails,
// compressed variants) are cleaned up.
//
// The file must have an active lock. Pass the lock token to verify ownership.
// If lockToken is empty, any active lock on the file is accepted (useful for WebDAV
// which manages locks separately).
func (s *FileService) ApplyPatch(ctx context.Context, fileID string, patchData io.Reader, lockToken string) (*database.CloudFile, error) {
	file, err := s.GetFile(fileID)
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}
	if file.IsFolder {
		return nil, fmt.Errorf("cannot patch a folder")
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

	pr, pw := io.Pipe()
	patchErr := make(chan error, 1)
	go func() {
		err := binarydist.Patch(original, pw, patchData)
		pw.CloseWithError(err)
		patchErr <- err
	}()

	object, err := s.StreamToStorage(ctx, pr, "")
	if err != nil {
		_ = pr.Close()
		return nil, fmt.Errorf("stream patched content: %w", err)
	}

	if pErr := <-patchErr; pErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrPatchFailed, pErr)
	}

	storageKey := &object.ID
	updated, err := s.OverwriteFile(fileID, object.ID, storageKey)
	if err != nil {
		return nil, fmt.Errorf("overwrite file record: %w", err)
	}

	return updated, nil
}
