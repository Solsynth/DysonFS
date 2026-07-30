package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/gabriel-vasile/mimetype"
	ffmpeg "github.com/u2takey/ffmpeg-go"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

type Worker struct {
	bus     *eventbus.Bus
	files   *service.FileService
	stor    storage.Backend
	db      *database.DB
	tempDir string
}

const compressedImageTargetBytes = 100 * 1024

func New(bus *eventbus.Bus, files *service.FileService, stor storage.Backend, db *database.DB, tempDir string) *Worker {
	return &Worker{bus: bus, files: files, stor: stor, db: db, tempDir: tempDir}
}

func (w *Worker) Start(ctx context.Context) error {
	if w.bus != nil {
		if _, err := w.bus.SubscribeFileUploaded(func(evt eventbus.FileUploadedEvent) error {
			return w.ProcessUploadedFile(context.Background(), evt)
		}); err != nil {
			return err
		}
		if _, err := w.bus.SubscribeFileAction(func(evt eventbus.FileActionEvent) error {
			return w.handleFileAction(evt)
		}); err != nil {
			return err
		}
	}
	go w.processPoolMigrations(ctx)
	go w.runMaintenance(ctx)
	logging.Log.Info().Msg("worker loop started")
	return nil
}

func (w *Worker) runMaintenance(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	rehashTicker := time.NewTicker(30 * time.Second)
	defer rehashTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.cleanupTempArtifacts()
			w.cleanupStaleTasks()
		case <-rehashTicker.C:
			w.processRehashQueue(ctx)
			w.processPoolMigrations(ctx)
		}
	}
}

func (w *Worker) processPoolMigrations(ctx context.Context) {
	if w.db == nil || w.files == nil {
		return
	}
	_ = w.db.Model(&database.PersistentTask{}).Where("type = ? AND status = ? AND last_activity < ?", service.PoolMigrationTaskType, "processing", time.Now().Add(-15*time.Minute)).Updates(map[string]any{"status": "pending", "last_activity": time.Now()}).Error
	for {
		var candidates []database.PersistentTask
		if err := w.db.Where("type = ? AND status = ?", service.PoolMigrationTaskType, "pending").Order("created_at asc").Limit(10).Find(&candidates).Error; err != nil {
			logging.Log.Error().Err(err).Msg("failed to query pool migration tasks")
			return
		}
		if len(candidates) == 0 {
			return
		}
		claimed := false
		for _, task := range candidates {
			result := w.db.Model(&database.PersistentTask{}).Where("task_id = ? AND status = ?", task.TaskID, "pending").Updates(map[string]any{"status": "processing", "last_activity": time.Now()})
			if result.Error != nil || result.RowsAffected == 0 {
				continue
			}
			claimed = true
			if err := w.runPoolMigration(ctx, &task); err != nil {
				message := err.Error()
				_ = w.db.Model(&database.PersistentTask{}).Where("task_id = ?", task.TaskID).Updates(map[string]any{"status": "failed", "error_message": message, "last_activity": time.Now()}).Error
				logging.Log.Error().Err(err).Str("taskId", task.TaskID).Msg("pool migration failed")
			}
			break
		}
		if !claimed {
			return
		}
	}
}

func (w *Worker) runPoolMigration(ctx context.Context, task *database.PersistentTask) error {
	var params service.PoolMigrationParameters
	if err := json.Unmarshal(task.Parameters, &params); err != nil || params.SourcePoolID == "" || params.TargetPoolID == "" || params.SourcePoolID == params.TargetPoolID {
		return fmt.Errorf("invalid pool migration parameters")
	}
	sourcePool, err := w.files.GetPool(params.SourcePoolID)
	if err != nil {
		return fmt.Errorf("get source pool: %w", err)
	}
	targetPool, err := w.files.GetPool(params.TargetPoolID)
	if err != nil {
		return fmt.Errorf("get target pool: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var file database.CloudFile
		query := w.db.Preload("Object").Where("pool_id = ?", params.SourcePoolID)
		if len(params.FileIDs) > 0 {
			query = query.Where("id IN ?", params.FileIDs)
		}
		err := query.Order("id asc").First(&file).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return w.db.Model(&database.PersistentTask{}).Where("task_id = ?", task.TaskID).Updates(map[string]any{"status": "completed", "progress": 1.0, "last_activity": time.Now()}).Error
		}
		if err != nil {
			return err
		}
		if err := w.moveFileToPool(ctx, &file, sourcePool, targetPool); err != nil {
			return fmt.Errorf("move file %s: %w", file.ID, err)
		}
		if err := w.db.Model(&database.PersistentTask{}).Where("task_id = ?", task.TaskID).Updates(map[string]any{
			"chunks_uploaded": gorm.Expr("chunks_uploaded + 1"),
			"progress":        gorm.Expr("CASE WHEN chunks_count > 0 THEN CAST(chunks_uploaded + 1 AS REAL) / chunks_count ELSE 1 END"),
			"last_activity":   time.Now(),
		}).Error; err != nil {
			return err
		}
	}
}

func (w *Worker) moveFileToPool(ctx context.Context, file *database.CloudFile, sourcePool, targetPool *service.Pool) error {
	key := ""
	if file.StorageKey != nil {
		key = strings.TrimSpace(*file.StorageKey)
	}
	if key == "" && file.Object != nil && file.Object.StorageKey != nil {
		key = strings.TrimSpace(*file.Object.StorageKey)
	}
	if key == "" && file.ObjectID != nil {
		key = strings.TrimSpace(*file.ObjectID)
	}
	if key != "" && !reflect.DeepEqual(sourcePool.StorageConfig, targetPool.StorageConfig) {
		source, err := w.files.BackendForPoolID(&sourcePool.ID)
		if err != nil {
			return err
		}
		target, err := w.files.BackendForPoolID(&targetPool.ID)
		if err != nil {
			return err
		}
		reader, info, err := source.Get(ctx, key)
		if err != nil {
			return err
		}
		defer reader.Close()
		contentType := info.MimeType
		if file.Object != nil && file.Object.MimeType != "" {
			contentType = file.Object.MimeType
		}
		if err := target.Put(ctx, key, reader, info.Size, contentType); err != nil {
			return err
		}
	}
	if err := w.db.Model(&database.CloudFile{}).Where("id = ?", file.ID).Updates(map[string]any{"pool_id": targetPool.ID, "storage_id": targetPool.ID}).Error; err != nil {
		return err
	}
	if key != "" && !reflect.DeepEqual(sourcePool.StorageConfig, targetPool.StorageConfig) {
		var refs int64
		if err := w.db.Model(&database.CloudFile{}).Where("storage_id = ? AND storage_key = ?", sourcePool.ID, key).Count(&refs).Error; err != nil {
			return err
		}
		if refs == 0 {
			source, err := w.files.BackendForPoolID(&sourcePool.ID)
			if err != nil {
				return err
			}
			if err := source.Delete(ctx, key); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func (w *Worker) cleanupTempArtifacts() {
	if w.tempDir == "" {
		return
	}
	entries, err := os.ReadDir(w.tempDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-30 * time.Minute)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(w.tempDir, entry.Name()))
	}
}

func (w *Worker) cleanupStaleTasks() {
	if w.files == nil {
		return
	}
	_ = w.files.DB().Where("status IN ? AND last_activity < now() - interval '30 days'", []string{"completed", "failed", "cancelled", "expired"}).Delete(&database.PersistentTask{}).Error
}

func (w *Worker) processRehashQueue(ctx context.Context) {
	if w.files == nil || w.stor == nil {
		return
	}
	cutoff := time.Now().Add(-30 * time.Second)
	var objects []database.FileObject
	if err := w.db.Where("needs_rehash = true AND updated_at < ?", cutoff).Limit(20).Find(&objects).Error; err != nil {
		logging.Log.Error().Err(err).Msg("failed to query rehash queue")
		return
	}
	for i := range objects {
		obj := &objects[i]
		if err := w.rehashObject(ctx, obj); err != nil {
			logging.Log.Error().Err(err).Str("objectId", obj.ID).Msg("failed to rehash object")
			continue
		}
	}
}

func (w *Worker) rehashObject(ctx context.Context, obj *database.FileObject) error {
	storageKey := ""
	if obj.StorageKey != nil && strings.TrimSpace(*obj.StorageKey) != "" {
		storageKey = strings.TrimSpace(*obj.StorageKey)
	} else {
		storageKey = obj.ID
	}

	rc, _, err := w.stor.Get(ctx, storageKey)
	if err != nil {
		return fmt.Errorf("read from storage: %w", err)
	}
	defer rc.Close()

	path, err := writeTempFile(rc, obj.ID)
	if err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read temp: %w", err)
	}
	hash := service.ComputeHash(data)
	mimeType := "application/octet-stream"
	if len(data) > 0 {
		mimeType = mimetype.Detect(data).String()
	}

	updates := map[string]any{
		"hash":         hash,
		"mime_type":    mimeType,
		"needs_rehash": false,
		"updated_at":   time.Now(),
	}
	if err := w.db.Model(&database.FileObject{}).Where("id = ?", obj.ID).Updates(updates).Error; err != nil {
		return fmt.Errorf("update object: %w", err)
	}

	var file database.CloudFile
	if err := w.db.Where("object_id = ? AND deleted_at IS NULL", obj.ID).First(&file).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	evt := eventbus.FileUploadedEvent{
		FileID:             file.ID,
		ContentType:        mimeType,
		StorageKey:         storageKey,
		ProcessingFilePath: path,
		IsTempFile:         false,
	}
	return w.ProcessUploadedFile(ctx, evt)
}

func (w *Worker) handleFileAction(evt eventbus.FileActionEvent) error {
	if evt.FileID == "" {
		return nil
	}
	switch evt.Action {
	case "delete", "purge":
		_ = w.files.PurgeFile(evt.FileID)
	case "recycle":
		_ = w.files.RecycleFile(evt.FileID)
	case "restore":
		_ = w.files.RestoreFile(evt.FileID)
	}
	return nil
}

func (w *Worker) HandleFileAction(evt eventbus.FileActionEvent) error {
	return w.handleFileAction(evt)
}

func (w *Worker) ProcessUploadedFile(_ context.Context, evt eventbus.FileUploadedEvent) error {
	if w.files == nil {
		return fmt.Errorf("file service not configured")
	}
	logging.Log.Info().
		Str("fileId", evt.FileID).
		Str("taskId", evt.TaskID).
		Str("contentType", evt.ContentType).
		Str("processingPath", evt.ProcessingFilePath).
		Bool("isTempFile", evt.IsTempFile).
		Msg("processing uploaded file")
	parent, err := w.files.GetFile(evt.FileID)
	if err != nil {
		return err
	}
	if parent.Object == nil {
		return fmt.Errorf("file object missing")
	}
	path := evt.ProcessingFilePath
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			logging.Log.Warn().Err(err).Str("fileId", evt.FileID).Str("path", path).Msg("processing file path unavailable, falling back to storage")
			path = ""
		}
	}
	if path == "" {
		rc, err := w.openSourceObject(context.Background(), parent)
		if err != nil {
			return err
		}
		defer rc.Close()
		path, err = writeTempFile(rc, parent.ID)
		if err != nil {
			return err
		}
		defer os.Remove(path)
	}
	if path == "" {
		return fmt.Errorf("processing file path missing")
	}
	if err := w.processDerived(path, evt, parent); err != nil {
		return err
	}
	if evt.IsTempFile && evt.ProcessingFilePath != "" {
		_ = os.Remove(path)
	}
	logging.Log.Info().
		Str("fileId", evt.FileID).
		Str("taskId", evt.TaskID).
		Msg("uploaded file processing completed")
	return nil
}

func (w *Worker) openSourceObject(ctx context.Context, file *database.CloudFile) (io.ReadCloser, error) {
	if file == nil || file.Object == nil {
		return nil, fmt.Errorf("file object missing")
	}
	storageKey := firstNonEmptyPtr(file.StorageKey, file.Object.StorageKey)
	if storageKey == nil || *storageKey == "" {
		if file.ObjectID == nil || strings.TrimSpace(*file.ObjectID) == "" {
			return nil, fmt.Errorf("storage key missing")
		}
		storageKey = file.ObjectID
	}
	backend, err := w.files.BackendForFile(file)
	if err != nil {
		return nil, err
	}
	rc, _, err := backend.Get(ctx, *storageKey)
	if err != nil {
		return nil, err
	}
	return rc, nil
}

func firstNonEmptyPtr(values ...*string) *string {
	for _, value := range values {
		if value != nil && strings.TrimSpace(*value) != "" {
			resolved := strings.TrimSpace(*value)
			return &resolved
		}
	}
	return nil
}

func (w *Worker) processDerived(path string, evt eventbus.FileUploadedEvent, parent *database.CloudFile) error {
	if parent.Object == nil {
		return nil
	}
	mimeType := evt.ContentType
	if mimeType == "" && parent.Object != nil {
		mimeType = parent.Object.MimeType
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	if strings.HasPrefix(mimeType, "image/") {
		if err := w.processImage(path, evt, parent, mimeType); err != nil {
			return err
		}
	} else if strings.HasPrefix(mimeType, "video/") {
		if err := w.processVideo(path, evt, parent, mimeType); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) processImage(path string, evt eventbus.FileUploadedEvent, parent *database.CloudFile, mimeType string) error {
	if w.stor == nil {
		return nil
	}

	img, err := vips.NewImageFromFile(path)
	if err != nil {
		return err
	}
	defer img.Close()

	if err := img.AutoRotate(); err != nil {
		return err
	}
	if err := img.RemoveMetadata(); err != nil {
		return err
	}
	if img.Pages() > 1 {
		return nil
	}
	return w.processImageStill(path, evt, parent, img)
}

func (w *Worker) processImageStill(path string, evt eventbus.FileUploadedEvent, parent *database.CloudFile, img *vips.ImageRef) error {
	origBuf, _, err := img.ExportWebp(&vips.WebpExportParams{Lossless: true, StripMetadata: true})
	if err != nil {
		return err
	}

	compBuf, err := exportCompressedWebp(img, origBuf, compressedImageTargetBytes)
	if err != nil {
		return err
	}
	if len(compBuf) > 0 {
		compKey := storageKey(parent.ID, ".compressed")
		if err := w.stor.Put(context.Background(), compKey, bytes.NewReader(compBuf), int64(len(compBuf)), "image/webp"); err != nil {
			return err
		}
		if err := w.upsertChild(parent, evt, "system.compression.low", compKey, "image/webp", compBuf); err != nil {
			return err
		}
	}

	if err := w.files.TouchCompatibilityFlags(parent.ID); err != nil {
		return err
	}
	return nil
}

func exportCompressedWebp(img *vips.ImageRef, original []byte, targetBytes int) ([]byte, error) {
	if img == nil {
		return nil, nil
	}
	maxEdge := img.Width()
	if img.Height() > maxEdge {
		maxEdge = img.Height()
	}
	if maxEdge <= 0 {
		return nil, nil
	}

	steps := []struct {
		maxEdge int
		quality int
	}{
		{maxEdge, 82},
		{1920, 80},
		{1600, 76},
		{1280, 72},
		{960, 68},
		{720, 64},
		{512, 60},
		{384, 55},
	}
	var smallest []byte
	for _, step := range steps {
		candidate, err := img.Copy()
		if err != nil {
			return nil, err
		}
		if step.maxEdge > 0 && maxEdge > step.maxEdge {
			scale := float64(step.maxEdge) / float64(maxEdge)
			if err := candidate.Resize(scale, vips.KernelLanczos3); err != nil {
				candidate.Close()
				return nil, err
			}
		}
		buf, _, err := candidate.ExportWebp(&vips.WebpExportParams{Quality: step.quality, StripMetadata: true})
		candidate.Close()
		if err != nil {
			return nil, err
		}
		if len(smallest) == 0 || len(buf) < len(smallest) {
			smallest = buf
		}
		if len(buf) <= targetBytes {
			return buf, nil
		}
	}
	if len(original) <= targetBytes {
		return original, nil
	}
	return smallest, nil
}

func (w *Worker) processVideo(path string, evt eventbus.FileUploadedEvent, parent *database.CloudFile, mimeType string) error {
	if w.stor == nil {
		return nil
	}

	thumbKey := storageKey(parent.ID, ".thumbnail")
	thumbPath := filepath.Join(os.TempDir(), parent.ID+".thumb.jpg")
	stream := ffmpeg.Input(path).
		Output(thumbPath, ffmpeg.KwArgs{"vframes": 1, "q:v": 2}).
		OverWriteOutput()
	if err := stream.Run(); err != nil {
		logging.Log.Error().Err(err).Str("fileId", parent.ID).Str("path", path).Msg("video thumbnail extraction failed")
		return err
	}
	defer os.Remove(thumbPath)
	thumbBytes, err := os.ReadFile(thumbPath)
	if err != nil {
		return err
	}
	if err := w.stor.Put(context.Background(), thumbKey, bytes.NewReader(thumbBytes), int64(len(thumbBytes)), "image/jpeg"); err != nil {
		return err
	}
	if err := w.upsertChild(parent, evt, "system.thumbnail", thumbKey, "image/jpeg", thumbBytes); err != nil {
		return err
	}
	_ = mimeType
	if err := w.files.TouchCompatibilityFlags(parent.ID); err != nil {
		return err
	}
	return nil
}

func writeTempFile(r io.Reader, prefix string) (string, error) {
	file, err := os.CreateTemp("", prefix+"-*")
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err := io.Copy(file, r); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

func (w *Worker) upsertChild(parent *database.CloudFile, evt eventbus.FileUploadedEvent, appType, storageKey, mimeType string, body []byte) error {
	if w.db == nil {
		return fmt.Errorf("database not configured")
	}
	obj := &database.FileObject{ID: database.NewID(), MimeType: mimeType, Hash: service.ComputeHash(body), StorageKey: &storageKey, Meta: datatypes.JSON([]byte(`{}`)), HasCompression: false, HasThumbnail: false}
	obj.Size = int64(len(body))
	if err := w.db.Create(obj).Error; err != nil {
		return err
	}
	_, err := w.files.CreateDerivedFile(parent.AccountID, parent.ID, parent.Name, obj.ID, appType, &storageKey)
	return err
}

func storageKey(parentID, suffix string) string {
	return parentID + suffix
}
