package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"golang.org/x/net/webdav"
	"gorm.io/datatypes"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/dispatch"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

type webdavFile struct {
	// Read path (streaming from storage)
	file     *database.CloudFile
	fs       *webdavFS
	ctx      context.Context
	loadErr  error
	s3Reader io.ReadCloser // underlying storage reader, opened lazily
	fileSize int64         // from Object.Size, known before fetch
	seekSkip int64         // bytes to discard before first Read (Range seek)

	reader   io.ReadCloser
	tempFile *os.File
	info     os.FileInfo
	winfo    *webdavFileInfo
	isWrite  bool
	isNew    bool
	existing *database.CloudFile
	parentID *string
	accountID string
	files    *service.FileService
	bus      *eventbus.Bus
	dispatcher dispatch.Dispatcher
	mu       sync.Mutex
	closed   bool
}

func (f *webdavFile) Read(p []byte) (int, error) {
	if f.tempFile != nil {
		return f.tempFile.Read(p)
	}
	if f.reader != nil {
		return f.reader.Read(p)
	}
	// ponytail: lazy open + stream from storage on first read.
	// No buffering — data flows S3 → client without an in-memory copy.
	if err := f.ensureOpen(); err != nil {
		return 0, err
	}
	if f.s3Reader == nil {
		return 0, io.EOF
	}
	return f.s3Reader.Read(p)
}

func (f *webdavFile) Write(p []byte) (int, error) {
	if !f.isWrite {
		return 0, os.ErrPermission
	}
	if f.tempFile == nil {
		return 0, os.ErrClosed
	}
	return f.tempFile.Write(p)
}

func (f *webdavFile) Seek(offset int64, whence int) (int64, error) {
	if f.tempFile != nil {
		return f.tempFile.Seek(offset, whence)
	}

	// http.ServeContent calls Seek(0, SeekEnd) for content length,
	// then Seek(0, SeekStart) before reading. Both are metadata-only:
	// we know the size from Object.Size without opening a storage reader.
	switch whence {
	case io.SeekEnd:
		if offset == 0 {
			return f.fileSize, nil
		}
		// ponytail: non-zero SeekEnd offset is never used by http.ServeContent
		return 0, os.ErrInvalid
	case io.SeekStart:
		if offset == 0 {
			// If we already have an open reader, close + reopen on next Read.
			if f.s3Reader != nil {
				f.s3Reader.Close()
				f.s3Reader = nil
			}
			return 0, nil
		}
		// ponytail: Range requests seek to offset — re-fetch from start and
		// discard the skipped bytes on the next Read. Not ideal for large
		// offsets but acceptable (Range requests are uncommon).
		f.seekSkip = offset
		return offset, nil
	default:
		return 0, os.ErrInvalid
	}
}

// ponytail: lazy open + stream from storage. Called by Read on first byte.
// No buffering — data flows S3 → client without an in-memory copy.
func (f *webdavFile) ensureOpen() error {
	if f.loadErr != nil {
		return f.loadErr
	}
	if f.s3Reader != nil {
		return nil
	}
	if f.file == nil || f.fs == nil {
		return nil
	}

	key := storageKeyForFile(f.file)
	if key == "" {
		return nil // metadata-only file, Read returns EOF
	}

	reader, err := f.fs.openFileContent(f.ctx, f.file)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			f.loadErr = os.ErrPermission
		} else {
			f.loadErr = err
		}
		return f.loadErr
	}

	// ponytail: discard bytes for Range requests (Seek to offset before Read).
	// Avoids needing a storage-backend Range method.
	if f.seekSkip > 0 {
		skip := f.seekSkip
		f.seekSkip = 0
		if _, err := io.CopyN(io.Discard, reader, skip); err != nil {
			reader.Close()
			f.loadErr = err
			return err
		}
	}

	f.s3Reader = reader
	return nil
}

func (f *webdavFile) Readdir(count int) ([]os.FileInfo, error) {
	return nil, os.ErrInvalid
}

func (f *webdavFile) Stat() (os.FileInfo, error) {
	if f.isWrite && f.tempFile != nil {
		st, err := f.tempFile.Stat()
		if err != nil {
			return nil, err
		}
		f.winfo.size = st.Size()
	}
	return f.info, nil
}

func (f *webdavFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return nil
	}
	f.closed = true

	if !f.isWrite {
		if f.s3Reader != nil {
			return f.s3Reader.Close()
		}
		if f.reader != nil {
			return f.reader.Close()
		}
		return nil
	}

	if f.tempFile == nil {
		return nil
	}

	defer os.Remove(f.tempFile.Name())

	if _, err := f.tempFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek temp file: %w", err)
	}

	if f.isNew {
		return f.closeNewFile()
	}
	return f.closeOverwriteFile()
}

func (f *webdavFile) closeNewFile() error {
	st, err := f.tempFile.Stat()
	if err != nil {
		return fmt.Errorf("stat temp file: %w", err)
	}

	if st.Size() == 0 {
		return f.createEmptyFile()
	}

	if _, err := f.tempFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek temp file: %w", err)
	}

	object, err := f.files.StreamToStorage(context.Background(), f.tempFile, "")
	if err != nil {
		return fmt.Errorf("stream to storage: %w", err)
	}

	accountUUID := parseUUID(f.accountID)
	created, err := f.files.CreateUploadedFile(
		accountUUID, f.winfo.name, nil, &object.Hash, nil, nil,
		f.parentID, object.ID, nil, nil, &object.ID, true,
	)
	if err != nil {
		return fmt.Errorf("create file record: %w", err)
	}

	f.winfo.fileID = created.ID
	f.winfo.size = object.Size
	f.winfo.modTime = created.CreatedAt

	f.publishUpload(created.ID)
	return nil
}

func (f *webdavFile) createEmptyFile() error {
	object := &database.FileObject{
		ID:       database.NewID(),
		Size:     0,
		MimeType: "application/octet-stream",
		Hash:     "",
		Meta:     datatypes.JSON([]byte(`{}`)),
	}
	if err := f.files.DB().Create(object).Error; err != nil {
		return fmt.Errorf("create empty file object: %w", err)
	}

	accountUUID := parseUUID(f.accountID)
	created, err := f.files.CreateUploadedFile(
		accountUUID, f.winfo.name, nil, nil, nil, nil,
		f.parentID, object.ID, nil, nil, &object.ID, true,
	)
	if err != nil {
		return fmt.Errorf("create empty file record: %w", err)
	}

	f.winfo.fileID = created.ID
	f.winfo.modTime = created.CreatedAt
	return nil
}

func (f *webdavFile) closeOverwriteFile() error {
	if f.existing == nil {
		return fmt.Errorf("no existing file for overwrite")
	}

	if _, err := f.tempFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek temp file: %w", err)
	}

	updated, err := f.files.OverwriteInPlace(context.Background(), f.existing.ID, f.tempFile)
	if err != nil {
		return fmt.Errorf("overwrite in place: %w", err)
	}

	if updated.Object != nil {
		f.winfo.size = updated.Object.Size
	}
	f.winfo.modTime = updated.UpdatedAt
	f.publishUpload(updated.ID)
	return nil
}

func (f *webdavFile) publishUpload(fileID string) {
	if f.bus == nil && f.dispatcher == nil {
		return
	}
	evt := eventbus.FileUploadedEvent{
		FileID: fileID,
	}
	if err := publishFileUploaded(context.Background(), f.bus, f.dispatcher, evt); err != nil {
		fmt.Printf("webdav: publish upload event: %v\n", err)
	}
}

type webdavDirFile struct {
	fs    *webdavFS
	file  *database.CloudFile
	ctx   context.Context
	items []os.FileInfo
	pos   int
}

func (d *webdavDirFile) Read(p []byte) (int, error) {
	return 0, os.ErrInvalid
}

func (d *webdavDirFile) Write(p []byte) (int, error) {
	return 0, os.ErrPermission
}

func (d *webdavDirFile) Seek(offset int64, whence int) (int64, error) {
	return 0, os.ErrInvalid
}

func (d *webdavDirFile) Close() error {
	return nil
}

func (d *webdavDirFile) Stat() (os.FileInfo, error) {
	return d.fs.fileToInfo(d.file)
}

func (d *webdavDirFile) Readdir(count int) ([]os.FileInfo, error) {
	if d.items == nil {
		children, err := d.fs.files.GetChildren(d.file.ID)
		if err != nil {
			return nil, err
		}
		d.items = make([]os.FileInfo, 0, len(children))
		for i := range children {
			info, err := d.fs.fileToInfo(&children[i])
			if err != nil {
				continue
			}
			d.items = append(d.items, info)
		}
	}
	if count <= 0 {
		items := d.items[d.pos:]
		d.pos = len(d.items)
		return items, nil
	}
	end := d.pos + count
	if end > len(d.items) {
		end = len(d.items)
	}
	items := d.items[d.pos:end]
	d.pos = end
	return items, nil
}

var _ webdav.File = (*webdavFile)(nil)
var _ webdav.File = (*webdavDirFile)(nil)
