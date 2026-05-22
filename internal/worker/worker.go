package worker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/davidbyttow/govips/v2/vips"
	ffmpeg "github.com/u2takey/ffmpeg-go"
	"gorm.io/datatypes"
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
	go w.runMaintenance(ctx)
	logging.Log.Info().Msg("worker loop started")
	return nil
}

func (w *Worker) runMaintenance(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.cleanupTempArtifacts()
			w.cleanupStaleTasks()
		}
	}
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
	origKey := storageKey(parent.ID, "original.webp")
	if err := w.stor.Put(context.Background(), origKey, bytes.NewReader(origBuf), "image/webp"); err != nil {
		return err
	}
	if err := w.upsertChild(parent, evt, "system.original", origKey, "image/webp", origBuf); err != nil {
		return err
	}

	thumb, err := vips.NewThumbnailFromFile(path, 512, 512, vips.InterestingAttention)
	if err != nil {
		return err
	}
	defer thumb.Close()
	thumbBuf, _, err := thumb.ExportWebp(&vips.WebpExportParams{Quality: 82, StripMetadata: true})
	if err != nil {
		return err
	}
	thumbKey := storageKey(parent.ID, ".thumbnail")
	if err := w.stor.Put(context.Background(), thumbKey, bytes.NewReader(thumbBuf), "image/webp"); err != nil {
		return err
	}
	if err := w.upsertChild(parent, evt, "system.thumbnail", thumbKey, "image/webp", thumbBuf); err != nil {
		return err
	}

	compBuf, err := exportCompressedWebp(img, origBuf, compressedImageTargetBytes)
	if err != nil {
		return err
	}
	if len(compBuf) > 0 {
		compKey := storageKey(parent.ID, ".compressed")
		if err := w.stor.Put(context.Background(), compKey, bytes.NewReader(compBuf), "image/webp"); err != nil {
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
	if err := w.stor.Put(context.Background(), thumbKey, bytes.NewReader(thumbBytes), "image/jpeg"); err != nil {
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
