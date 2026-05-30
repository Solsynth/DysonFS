package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/gabriel-vasile/mimetype"
	"gorm.io/datatypes"
	"src.solsynth.dev/sosys/filesystem/internal/database"
)

type countingWriter struct {
	w    io.Writer
	n    int64
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n += int64(n)
	return n, err
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

	if contentType == "" {
		detected, err := mimetype.DetectFile(tempPath)
		if err == nil && detected != nil {
			contentType = detected.String()
		}
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	hash := hex.EncodeToString(hasher.Sum(nil))

	storageKey := database.NewID()
	stage, err := os.Open(tempPath)
	if err != nil {
		return nil, fmt.Errorf("open temp for upload: %w", err)
	}
	defer stage.Close()
	if err := s.Storage().Put(ctx, storageKey, stage, contentType); err != nil {
		return nil, fmt.Errorf("upload to storage: %w", err)
	}

	object := &database.FileObject{
		ID:             storageKey,
		Size:           size,
		MimeType:       contentType,
		Hash:           hash,
		Meta:           datatypes.JSON([]byte(`{}`)),
		HasCompression: false,
		HasThumbnail:   false,
	}
	if err := s.db.Create(object).Error; err != nil {
		return nil, fmt.Errorf("create file object: %w", err)
	}
	return object, nil
}
