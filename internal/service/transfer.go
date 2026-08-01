package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"gorm.io/datatypes"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
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

func (s *FileService) CreateStoredObject(storageKey string, info *StagedFileInfo) (*database.FileObject, error) {
	if info == nil || strings.TrimSpace(storageKey) == "" {
		return nil, fmt.Errorf("storage key and file info are required")
	}
	object := &database.FileObject{
		ID: database.NewID(), Size: info.Size, MimeType: info.ContentType,
		Hash: info.Hash, StorageKey: &storageKey, Meta: datatypes.JSON([]byte(`{}`)),
	}
	if err := s.db.Create(object).Error; err != nil {
		return nil, fmt.Errorf("create file object: %w", err)
	}
	return object, nil
}

// RefreshStoredObjectAnalysis downloads an object that was written directly to
// storage via a presigned URL (so it never touched DysonFS disk), computes its
// SHA-256 hash, extracts source metadata, and overwrites the local FileObject
// record so direct uploads end up with the same metadata as proxied ones.
//
// The hash is persisted whenever the download succeeds; if analysis itself
// fails, the caller still receives the error with the hash already stored.
func (s *FileService) RefreshStoredObjectAnalysis(ctx context.Context, backend storage.Backend, objectID, storageKey, contentType string) (*SourceAnalysis, error) {
	if backend == nil || strings.TrimSpace(storageKey) == "" {
		return nil, fmt.Errorf("backend and storage key are required")
	}
	rc, _, err := backend.Get(ctx, storageKey)
	if err != nil {
		return nil, fmt.Errorf("read object from storage: %w", err)
	}
	defer rc.Close()

	tempFile, err := os.CreateTemp("", "dysonfs-analyze-*")
	if err != nil {
		return nil, fmt.Errorf("create analysis temp file: %w", err)
	}
	defer os.Remove(tempFile.Name())

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tempFile, hasher), rc); err != nil {
		_ = tempFile.Close()
		return nil, fmt.Errorf("download object for analysis: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return nil, fmt.Errorf("close analysis temp file: %w", err)
	}

	if err := s.db.Model(&database.FileObject{}).Where("id = ?", objectID).Update("hash", hex.EncodeToString(hasher.Sum(nil))).Error; err != nil {
		return nil, fmt.Errorf("store object hash: %w", err)
	}

	analysis, err := s.AnalyzeSourceFile(ctx, tempFile.Name(), contentType)
	if err != nil {
		return nil, err
	}
	return analysis, nil
}

// uploadTaskExpiryAge is how long an upload may sit without any activity
// (prepare, part presigns) before the hourly sweep expires it. Multipart part
// presigns refresh the task's activity, so live uploads never expire; six
// hours is generous relative to the 15-minute presigned URL lifetime.
const uploadTaskExpiryAge = 6 * time.Hour

// ExpireStaleUploadTasks finds direct-upload tasks that never completed and
// releases their storage: multipart sessions are aborted (S3 discards the
// uploaded parts) and orphaned single-PUT objects are deleted. Each task is
// claimed atomically first, so a concurrent completion either wins the claim
// and leaves the task untouched, or the sweep wins and the completion's own
// status guard rejects it. Returns the number of tasks expired.
func (s *FileService) ExpireStaleUploadTasks(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-uploadTaskExpiryAge)
	var tasks []database.PersistentTask
	if err := s.db.Where("upload_status = ? AND updated_at < ?", database.UploadStatusUploading, cutoff).Find(&tasks).Error; err != nil {
		return 0, fmt.Errorf("query stale upload tasks: %w", err)
	}
	expired := 0
	for i := range tasks {
		task := &tasks[i]
		if err := s.expireStaleUploadTask(ctx, task, cutoff); err != nil {
			logging.Log.Error().Err(err).Str("taskId", task.TaskID).Msg("failed to expire stale upload task")
			continue
		}
		expired++
	}
	return expired, nil
}

func (s *FileService) expireStaleUploadTask(ctx context.Context, task *database.PersistentTask, cutoff time.Time) error {
	// Claim the task so a concurrent completion cannot race the cleanup. A
	// completion that already moved the task out of Uploading makes the claim
	// a no-op.
	claim := s.db.Model(&database.PersistentTask{}).
		Where("task_id = ? AND upload_status = ? AND updated_at < ?", task.TaskID, database.UploadStatusUploading, cutoff).
		Updates(map[string]any{
			"upload_status":    database.UploadStatusFailed,
			"status":           "expired",
			"processing_error": fmt.Sprintf("upload expired: no activity for %d hours", int(uploadTaskExpiryAge.Hours())),
			"updated_at":       time.Now(),
			"last_activity":    time.Now(),
		})
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected == 0 {
		return nil // completed, failed, or already claimed concurrently
	}

	backend, err := s.BackendForPoolID(task.PoolID)
	if err != nil {
		return fmt.Errorf("resolve pool backend: %w", err)
	}
	if task.SourceKey == nil || strings.TrimSpace(*task.SourceKey) == "" {
		return nil
	}
	if task.UploadID != nil && strings.TrimSpace(*task.UploadID) != "" {
		if multipartBackend, ok := backend.(storage.MultipartDirectUploadBackend); ok {
			if abortErr := multipartBackend.AbortMultipartUpload(ctx, *task.SourceKey, *task.UploadID); abortErr != nil {
				logging.Log.Warn().Err(abortErr).Str("taskId", task.TaskID).Str("uploadId", *task.UploadID).Msg("failed to abort expired multipart upload")
			}
		}
	}
	// Single-PUT direct uploads write the object before completion, so remove
	// it here; for multipart sessions the key does not exist until complete
	// and Delete is a no-op.
	if deleteErr := backend.Delete(ctx, *task.SourceKey); deleteErr != nil {
		logging.Log.Warn().Err(deleteErr).Str("taskId", task.TaskID).Str("sourceKey", *task.SourceKey).Msg("failed to delete expired upload object")
	}
	logging.Log.Info().Str("taskId", task.TaskID).Msg("expired stale upload task")
	return nil
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
