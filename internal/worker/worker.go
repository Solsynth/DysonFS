package worker

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"gorm.io/datatypes"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

type Worker struct {
	bus   *eventbus.Bus
	files *service.FileService
	stor  storage.Backend
	db    *database.DB
}

func New(bus *eventbus.Bus, files *service.FileService, stor storage.Backend, db *database.DB) *Worker {
	return &Worker{bus: bus, files: files, stor: stor, db: db}
}

func (w *Worker) Start(ctx context.Context) error {
	go func() { <-ctx.Done() }()
	logging.Log.Info().Msg("worker loop started")
	return nil
}

func (w *Worker) ProcessUploadedFile(_ context.Context, evt eventbus.FileUploadedEvent) error {
	if w.files == nil {
		return fmt.Errorf("file service not configured")
	}
	if _, err := os.Stat(evt.ProcessingFilePath); err != nil {
		return err
	}
	_, err := os.Stat(evt.ProcessingFilePath)
	if err != nil {
		return err
	}
	mimeType := evt.ContentType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	parent, err := w.files.GetFile(evt.FileID)
	if err != nil {
		return err
	}
	if parent.Object == nil {
		return fmt.Errorf("file object missing")
	}
	if err := w.processDerived(evt, parent, mimeType); err != nil {
		return err
	}
	if evt.IsTempFile {
		_ = os.Remove(evt.ProcessingFilePath)
	}
	return nil
}

func (w *Worker) processDerived(evt eventbus.FileUploadedEvent, parent *database.CloudFile, mimeType string) error {
	_ = mimeType
	if parent.Object == nil {
		return nil
	}
	if err := w.files.TouchCompatibilityFlags(parent.ID); err != nil {
		return err
	}
	if err := w.createSystemChildren(evt, parent); err != nil {
		return err
	}
	return nil
}

func (w *Worker) createSystemChildren(evt eventbus.FileUploadedEvent, parent *database.CloudFile) error {
	if w.files == nil || parent.Object == nil {
		return nil
	}
	if err := w.createChild(evt, parent, "system.thumbnail", ".thumbnail"); err != nil {
		return err
	}
	if err := w.createChild(evt, parent, "system.compression.low", ".compressed.low"); err != nil {
		return err
	}
	return nil
}

func (w *Worker) createChild(evt eventbus.FileUploadedEvent, parent *database.CloudFile, appType, suffix string) error {
	if w.stor == nil {
		return nil
	}
	key := parent.ID + suffix
	if err := w.stor.Put(context.Background(), key, bytes.NewReader([]byte{}), evt.ContentType); err != nil {
		return err
	}
	childObject := &database.FileObject{ID: database.NewID(), MimeType: evt.ContentType, Hash: "", Meta: datatypes.JSON([]byte(`{}`))}
	if err := w.db.Create(childObject).Error; err != nil {
		return err
	}
	_, err := w.files.CreateDerivedFile(parent.AccountID, parent.ID, parent.Name+suffix, childObject.ID, appType)
	return err
}
