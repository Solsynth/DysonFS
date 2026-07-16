package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"gorm.io/datatypes"
	"src.solsynth.dev/sosys/filesystem/internal/database"
)

type countingWriter struct {
	w io.Writer
	n int64
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n += int64(n)
	return n, err
}

type stagedFileInfo struct {
	size        int64
	contentType string
	hash        string
}

// StagedFileInfo describes a fully written local upload source. It is kept
// separate from storage so callers can inspect a source once, then overlap
// storage I/O with synchronous metadata analysis.
type StagedFileInfo struct {
	Size        int64
	ContentType string
	Hash        string
}

func detectSourceMime(path, contentType string) string {
	resolved := strings.TrimSpace(contentType)
	if resolved != "" && !strings.EqualFold(resolved, "application/octet-stream") {
		return resolved
	}
	detected, err := mimetype.DetectFile(path)
	if err == nil && detected != nil {
		return detected.String()
	}
	if resolved != "" {
		return resolved
	}
	return "application/octet-stream"
}

func inspectStagedFile(path, contentType string) (*stagedFileInfo, error) {
	stage, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open staged file: %w", err)
	}
	defer stage.Close()

	hasher := sha256.New()
	size, err := io.Copy(hasher, stage)
	if err != nil {
		return nil, fmt.Errorf("hash staged file: %w", err)
	}

	return &stagedFileInfo{
		size:        size,
		contentType: detectSourceMime(path, contentType),
		hash:        hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func InspectStagedFile(path, contentType string) (*StagedFileInfo, error) {
	info, err := inspectStagedFile(path, contentType)
	if err != nil {
		return nil, err
	}
	return &StagedFileInfo{Size: info.size, ContentType: info.contentType, Hash: info.hash}, nil
}

// NewStagedFileInfo reuses hash and size collected while a direct upload is
// being staged, avoiding another full read before the storage transfer.
func NewStagedFileInfo(path, contentType string, size int64, hash string) *StagedFileInfo {
	return &StagedFileInfo{Size: size, ContentType: detectSourceMime(path, contentType), Hash: hash}
}

func (s *FileService) UploadStagedFile(ctx context.Context, path string, info *StagedFileInfo) (string, error) {
	if info == nil {
		return "", fmt.Errorf("staged file info is required")
	}
	stage, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open temp for upload: %w", err)
	}
	defer stage.Close()

	storageKey := database.NewID()
	if err := s.Storage().Put(ctx, storageKey, stage, info.Size, info.ContentType); err != nil {
		return "", fmt.Errorf("upload to storage: %w", err)
	}
	return storageKey, nil
}

func (s *FileService) CreateUploadedObject(storageKey string, info *StagedFileInfo, analysis *SourceAnalysis) (*database.FileObject, error) {
	if info == nil {
		return nil, fmt.Errorf("staged file info is required")
	}
	meta := datatypes.JSON([]byte(`{}`))
	if analysis != nil {
		var err error
		meta, err = mergeJSONMeta(meta, sourceAnalysisUpdates(analysis))
		if err != nil {
			return nil, fmt.Errorf("merge source analysis: %w", err)
		}
	}
	object := &database.FileObject{
		ID:             storageKey,
		Size:           info.Size,
		MimeType:       info.ContentType,
		Hash:           info.Hash,
		StorageKey:     &storageKey,
		Meta:           meta,
		HasCompression: false,
		HasThumbnail:   false,
	}
	if err := s.db.Create(object).Error; err != nil {
		return nil, fmt.Errorf("create file object: %w", err)
	}
	return object, nil
}

func (s *FileService) createFileObject(storageKey string, info *stagedFileInfo) (*database.FileObject, error) {
	object := &database.FileObject{
		ID:             storageKey,
		Size:           info.size,
		MimeType:       info.contentType,
		Hash:           info.hash,
		Meta:           datatypes.JSON([]byte(`{}`)),
		HasCompression: false,
		HasThumbnail:   false,
	}
	if err := s.db.Create(object).Error; err != nil {
		return nil, fmt.Errorf("create file object: %w", err)
	}
	return object, nil
}

func (s *FileService) StreamFileToStorage(ctx context.Context, path, contentType string) (*database.FileObject, error) {
	info, err := InspectStagedFile(path, contentType)
	if err != nil {
		return nil, err
	}
	storageKey, err := s.UploadStagedFile(ctx, path, info)
	if err != nil {
		return nil, err
	}
	return s.CreateUploadedObject(storageKey, info, nil)
}

// StreamToStorage reads from r, writes to a temp file while computing SHA-256 hash
// and byte count, detects MIME type from the first 512 bytes, uploads to the storage
// backend, and creates a FileObject record in the database.
//
// If contentType is empty, MIME type is auto-detected.
// The caller is responsible for creating the CloudFile record and cleaning up temp files.
func (s *FileService) StreamToStorage(ctx context.Context, r io.Reader, contentType string) (*database.FileObject, error) {
	tempFile, err := os.CreateTemp("", "dysonfs-stream-*")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	hasher := sha256.New()
	tee := io.TeeReader(r, hasher)
	cw := &countingWriter{w: tempFile}
	size, err := io.Copy(cw, tee)
	if err != nil {
		_ = tempFile.Close()
		return nil, fmt.Errorf("stream to temp: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return nil, fmt.Errorf("close temp file: %w", err)
	}

	contentType = detectSourceMime(tempPath, contentType)

	storageKey := database.NewID()
	stage, err := os.Open(tempPath)
	if err != nil {
		return nil, fmt.Errorf("open temp for upload: %w", err)
	}
	defer stage.Close()
	if err := s.Storage().Put(ctx, storageKey, stage, size, contentType); err != nil {
		return nil, fmt.Errorf("upload to storage: %w", err)
	}

	return s.createFileObject(storageKey, &stagedFileInfo{
		size:        size,
		contentType: contentType,
		hash:        hex.EncodeToString(hasher.Sum(nil)),
	})
}
