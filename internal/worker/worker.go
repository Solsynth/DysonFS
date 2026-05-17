package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	blurhash "github.com/bbrks/go-blurhash"
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
	if _, err := os.Stat(evt.ProcessingFilePath); err != nil {
		return err
	}
	parent, err := w.files.GetFile(evt.FileID)
	if err != nil {
		return err
	}
	if parent.Object == nil {
		return fmt.Errorf("file object missing")
	}
	if err := w.processDerived(evt, parent); err != nil {
		return err
	}
	if evt.IsTempFile {
		_ = os.Remove(evt.ProcessingFilePath)
	}
	return nil
}

func (w *Worker) processDerived(evt eventbus.FileUploadedEvent, parent *database.CloudFile) error {
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

	if err := w.files.TouchCompatibilityFlags(parent.ID); err != nil {
		return err
	}

	if strings.HasPrefix(mimeType, "image/") {
		if err := w.processImage(evt, parent, mimeType); err != nil {
			return err
		}
	} else if strings.HasPrefix(mimeType, "video/") {
		if err := w.processVideo(evt, parent, mimeType); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) processImage(evt eventbus.FileUploadedEvent, parent *database.CloudFile, mimeType string) error {
	if w.stor == nil {
		return nil
	}

	img, err := vips.NewImageFromFile(evt.ProcessingFilePath)
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
	width, height := img.Width(), img.Height()

	blurHash, err := w.computeBlurHash(evt.ProcessingFilePath)
	if err != nil {
		return err
	}
	if err := w.storeBlurHash(parent, blurHash); err != nil {
		return err
	}
	if err := w.storeImageDimensions(parent, width, height); err != nil {
		return err
	}

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

	thumb, err := vips.NewThumbnailFromFile(evt.ProcessingFilePath, 512, 512, vips.InterestingAttention)
	if err != nil {
		return err
	}
	defer thumb.Close()
	thumbBuf, _, err := thumb.ExportWebp(&vips.WebpExportParams{Quality: 82, StripMetadata: true})
	if err != nil {
		return err
	}
	thumbKey := storageKey(parent.ID, "thumbnail.webp")
	if err := w.stor.Put(context.Background(), thumbKey, bytes.NewReader(thumbBuf), "image/webp"); err != nil {
		return err
	}
	if err := w.upsertChild(parent, evt, "system.thumbnail", thumbKey, "image/webp", thumbBuf); err != nil {
		return err
	}

	if img.Width() >= 1024 || img.Height() >= 1024 {
		compressed, err := vips.NewImageFromFile(evt.ProcessingFilePath)
		if err != nil {
			return err
		}
		defer compressed.Close()
		if err := compressed.Resize(0.5, vips.KernelLanczos3); err != nil {
			return err
		}
		compBuf, _, err := compressed.ExportWebp(&vips.WebpExportParams{Quality: 80, StripMetadata: true})
		if err != nil {
			return err
		}
		compKey := storageKey(parent.ID, "compression.low.webp")
		if err := w.stor.Put(context.Background(), compKey, bytes.NewReader(compBuf), "image/webp"); err != nil {
			return err
		}
		if err := w.upsertChild(parent, evt, "system.compression.low", compKey, "image/webp", compBuf); err != nil {
			return err
		}
	}

	_ = mimeType
	return nil
}

func (w *Worker) processVideo(evt eventbus.FileUploadedEvent, parent *database.CloudFile, mimeType string) error {
	if w.stor == nil {
		return nil
	}

	thumbKey := storageKey(parent.ID, "thumbnail.jpg")
	thumbPath := filepath.Join(os.TempDir(), parent.ID+".thumb.jpg")
	stream := ffmpeg.Input(evt.ProcessingFilePath).
		Output(thumbPath, ffmpeg.KwArgs{"vframes": 1, "q:v": 2}).
		OverWriteOutput()
	if err := stream.Run(); err != nil {
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
	return nil
}

func (w *Worker) computeBlurHash(path string) (string, error) {
	img, err := vips.NewImageFromFile(path)
	if err != nil {
		return "", err
	}
	defer img.Close()

	buf, _, err := img.ExportPng(&vips.PngExportParams{StripMetadata: true})
	if err != nil {
		return "", err
	}
	decoded, err := png.Decode(bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	return blurhash.Encode(4, 3, decoded)
}

func (w *Worker) storeBlurHash(parent *database.CloudFile, hash string) error {
	if w.db == nil || parent.Object == nil {
		return nil
	}
	var object database.FileObject
	if err := w.db.First(&object, "id = ?", *parent.ObjectID).Error; err != nil {
		return err
	}
	meta := map[string]any{}
	if len(object.Meta) > 0 {
		_ = json.Unmarshal(object.Meta, &meta)
	}
	meta["blurhash"] = hash
	raw, _ := json.Marshal(meta)
	return w.db.Model(&database.FileObject{}).Where("id = ?", object.ID).Update("meta", datatypes.JSON(raw)).Error
}

func (w *Worker) storeImageDimensions(parent *database.CloudFile, width, height int) error {
	if w.db == nil || parent.Object == nil {
		return nil
	}
	var object database.FileObject
	if err := w.db.First(&object, "id = ?", *parent.ObjectID).Error; err != nil {
		return err
	}
	meta := map[string]any{}
	if len(object.Meta) > 0 {
		_ = json.Unmarshal(object.Meta, &meta)
	}
	meta["width"] = width
	meta["height"] = height
	raw, _ := json.Marshal(meta)
	return w.db.Model(&database.FileObject{}).Where("id = ?", object.ID).Update("meta", datatypes.JSON(raw)).Error
}

func (w *Worker) upsertChild(parent *database.CloudFile, evt eventbus.FileUploadedEvent, appType, storageKey, mimeType string, body []byte) error {
	if w.db == nil {
		return fmt.Errorf("database not configured")
	}
	obj := &database.FileObject{ID: database.NewID(), MimeType: mimeType, Hash: "", StorageKey: &storageKey, Meta: datatypes.JSON([]byte(`{}`)), HasCompression: false, HasThumbnail: false}
	obj.Size = int64(len(body))
	if err := w.db.Create(obj).Error; err != nil {
		return err
	}
	_, err := w.files.CreateDerivedFile(parent.AccountID, parent.ID, parent.Name, obj.ID, appType, &storageKey)
	return err
}

func storageKey(parentID, suffix string) string {
	return parentID + "/" + suffix
}
