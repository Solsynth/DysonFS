package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"golang.org/x/net/webdav"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/dispatch"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/go/pkg/auth"
)

const WebDAVAccountIDKey = "webdav_account_id"

func handleWebDAV(c *gin.Context, files *service.FileService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher, prefix string) {
	accountID := ""

	if id, ok := c.Get(WebDAVAccountIDKey); ok {
		accountID = id.(string)
	} else {
		result, _, ok := auth.GetAuth(c)
		if !ok {
			c.Header("WWW-Authenticate", `Basic realm="DysonFS"`)
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		var err error
		accountID, err = parseAccountID(result)
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
	}

	h := &webdav.Handler{
		Prefix:     prefix,
		FileSystem: &webdavFS{files: files, bus: bus, dispatcher: dispatcher, accountID: accountID},
		LockSystem: &webdavLockSystem{files: files, accountID: accountID},
		Logger: func(r *http.Request, err error) {
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Error().Err(err).
					Str("method", r.Method).
					Str("path", r.URL.Path).
					Str("accountId", accountID).
					Msg("webdav error")
			} else {
				log.Debug().
					Str("method", r.Method).
					Str("path", r.URL.Path).
					Str("accountId", accountID).
					Msg("webdav request")
			}
		},
	}
	h.ServeHTTP(c.Writer, c.Request)
}

func parseAccountID(result *auth.AuthResult) (string, error) {
	if result == nil || result.Account == nil {
		return "", errors.New("no account in auth result")
	}
	return result.Account.GetId(), nil
}

type webdavFS struct {
	files      *service.FileService
	bus        *eventbus.Bus
	dispatcher dispatch.Dispatcher
	accountID  string
}

func (fs *webdavFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	if name == "/" || name == "" {
		return &webdavFileInfo{name: "/", isDir: true, modTime: time.Now()}, nil
	}
	f, err := fs.resolvePath(ctx, name)
	if err != nil {
		return nil, os.ErrNotExist
	}
	return fs.fileToInfo(f)
}

func (fs *webdavFS) ReadDir(ctx context.Context, name string, _ int) ([]os.FileInfo, error) {
	if name == "/" || name == "" {
		return fs.listRoot(ctx)
	}
	f, err := fs.resolvePath(ctx, name)
	if err != nil {
		return nil, os.ErrNotExist
	}
	if !f.IsFolder {
		return nil, os.ErrInvalid
	}
	children, err := fs.files.GetChildren(f.ID)
	if err != nil {
		return nil, err
	}
	infos := make([]os.FileInfo, 0, len(children))
	for i := range children {
		info, err := fs.fileToInfo(&children[i])
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (fs *webdavFS) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	parentPath, dirName := path.Split(path.Clean(name))
	dirName = strings.TrimSuffix(dirName, "/")
	if dirName == "" {
		return os.ErrInvalid
	}
	var parentID *string
	if parentPath != "/" && parentPath != "" {
		parent, err := fs.resolvePath(ctx, strings.TrimSuffix(parentPath, "/"))
		if err != nil {
			return os.ErrNotExist
		}
		if !parent.IsFolder {
			return os.ErrInvalid
		}
		parentID = &parent.ID
	}
	_, err := fs.files.CreateFolder(
		parseUUID(fs.accountID),
		dirName,
		parentID,
	)
	return err
}

func (fs *webdavFS) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	isWrite := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) != 0

	if isWrite {
		return fs.openForWrite(ctx, name, flag)
	}
	return fs.openForRead(ctx, name)
}

func (fs *webdavFS) openForRead(ctx context.Context, name string) (webdav.File, error) {
	f, err := fs.resolvePath(ctx, name)
	if err != nil {
		return nil, os.ErrNotExist
	}
	if f.IsFolder {
		return &webdavDirFile{fs: fs, file: f, ctx: ctx}, nil
	}
	reader, err := fs.openFileContent(ctx, f)
	if err != nil {
		return nil, err
	}
	info, err := fs.fileToInfo(f)
	if err != nil {
		reader.Close()
		return nil, err
	}
	winfo := info.(*webdavFileInfo)
	return &webdavFile{
		reader:  reader,
		info:    info,
		winfo:   winfo,
		isWrite: false,
	}, nil
}

func (fs *webdavFS) openForWrite(ctx context.Context, name string, flag int) (webdav.File, error) {
	cleanName := path.Clean(name)
	parentPath, fileName := path.Split(cleanName)
	fileName = strings.TrimSuffix(fileName, "/")
	if fileName == "" {
		return nil, os.ErrInvalid
	}

	var parentID *string
	if parentPath != "/" && parentPath != "" {
		parent, err := fs.resolvePath(ctx, strings.TrimSuffix(parentPath, "/"))
		if err != nil {
			return nil, os.ErrNotExist
		}
		if !parent.IsFolder {
			return nil, os.ErrInvalid
		}
		parentID = &parent.ID
	}

	var existingFile *database.CloudFile
	if flag&os.O_CREATE == 0 || flag&os.O_TRUNC != 0 {
		resolved, err := fs.resolvePath(ctx, cleanName)
		if err == nil {
			existingFile = resolved
		}
	}

	if existingFile != nil && existingFile.IsFolder {
		return nil, os.ErrInvalid
	}

	tempFile, err := os.CreateTemp("", "dysonfs-webdav-*")
	if err != nil {
		return nil, err
	}

	winfo := &webdavFileInfo{name: fileName, isDir: false}
	return &webdavFile{
		tempFile:    tempFile,
		info:        winfo,
		winfo:       winfo,
		isWrite:     true,
		isNew:       existingFile == nil,
		existing:    existingFile,
		parentID:    parentID,
		accountID:   fs.accountID,
		files:       fs.files,
		bus:         fs.bus,
		dispatcher:  fs.dispatcher,
		mu:          sync.Mutex{},
	}, nil
}

func (fs *webdavFS) RemoveAll(ctx context.Context, name string) error {
	f, err := fs.resolvePath(ctx, name)
	if err != nil {
		return os.ErrNotExist
	}
	return fs.files.DeleteFile(f.ID)
}

func (fs *webdavFS) Rename(ctx context.Context, oldName, newName string) error {
	src, err := fs.resolvePath(ctx, oldName)
	if err != nil {
		return os.ErrNotExist
	}

	cleanDst := path.Clean(newName)
	dstParentPath, dstName := path.Split(cleanDst)
	dstName = strings.TrimSuffix(dstName, "/")
	if dstName == "" {
		return os.ErrInvalid
	}

	var dstParentID *string
	if dstParentPath != "/" && dstParentPath != "" {
		dstParent, err := fs.resolvePath(ctx, strings.TrimSuffix(dstParentPath, "/"))
		if err != nil {
			return os.ErrNotExist
		}
		if !dstParent.IsFolder {
			return os.ErrInvalid
		}
		dstParentID = &dstParent.ID
	}

	if _, err := fs.files.MoveBatch([]string{src.ID}, dstParentID, nil); err != nil {
		return err
	}

	if dstName != src.Name {
		if err := fs.files.DB().Model(&database.CloudFile{}).Where("id = ?", src.ID).Update("name", dstName).Error; err != nil {
			return err
		}
	}
	return nil
}

func (fs *webdavFS) resolvePath(ctx context.Context, p string) (*database.CloudFile, error) {
	p = path.Clean(p)
	if p == "/" || p == "" {
		return nil, errors.New("root is not a file")
	}
	segments := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(segments) == 0 {
		return nil, errors.New("empty path")
	}

	rootFiles, err := fs.files.ListRoot(parseUUID(fs.accountID))
	if err != nil {
		return nil, err
	}

	var current *database.CloudFile
	for _, seg := range segments {
		found := false
		if current == nil {
			for i := range rootFiles {
				if rootFiles[i].Name == seg {
					current = &rootFiles[i]
					found = true
					break
				}
			}
		} else {
			children, err := fs.files.GetChildren(current.ID)
			if err != nil {
				return nil, err
			}
			for i := range children {
				if children[i].Name == seg {
					current = &children[i]
					found = true
					break
				}
			}
		}
		if !found {
			return nil, os.ErrNotExist
		}
	}
	return current, nil
}

func (fs *webdavFS) listRoot(ctx context.Context) ([]os.FileInfo, error) {
	rootFiles, err := fs.files.ListRoot(parseUUID(fs.accountID))
	if err != nil {
		return nil, err
	}
	infos := make([]os.FileInfo, 0, len(rootFiles))
	for i := range rootFiles {
		info, err := fs.fileToInfo(&rootFiles[i])
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (fs *webdavFS) openFileContent(ctx context.Context, f *database.CloudFile) (io.ReadCloser, error) {
	key := storageKeyForFile(f)
	if key == "" {
		return nil, fmt.Errorf("file storage key missing")
	}
	backend, err := fs.files.BackendForFile(f)
	if err != nil {
		return nil, err
	}
	reader, _, err := backend.Get(ctx, key)
	return reader, err
}

func (fs *webdavFS) fileToInfo(f *database.CloudFile) (os.FileInfo, error) {
	var size int64
	var modTime time.Time
	if f.Object != nil {
		size = f.Object.Size
		modTime = f.CreatedAt
	} else {
		modTime = f.CreatedAt
	}
	return &webdavFileInfo{
		name:    f.Name,
		size:    size,
		modTime: modTime,
		isDir:   f.IsFolder,
		fileID:  f.ID,
	}, nil
}

func storageKeyForFile(f *database.CloudFile) string {
	if f.StorageKey != nil && strings.TrimSpace(*f.StorageKey) != "" {
		return strings.TrimSpace(*f.StorageKey)
	}
	if f.Object != nil && f.Object.StorageKey != nil && strings.TrimSpace(*f.Object.StorageKey) != "" {
		return strings.TrimSpace(*f.Object.StorageKey)
	}
	if f.ObjectID != nil && strings.TrimSpace(*f.ObjectID) != "" {
		return strings.TrimSpace(*f.ObjectID)
	}
	return ""
}

func parseUUID(s string) uuid.UUID {
	return uuid.MustParse(s)
}

type webdavFileInfo struct {
	name    string
	size    int64
	modTime time.Time
	isDir   bool
	fileID  string
}

func (fi *webdavFileInfo) Name() string      { return fi.name }
func (fi *webdavFileInfo) Size() int64        { return fi.size }
func (fi *webdavFileInfo) Mode() os.FileMode  { return 0644 }
func (fi *webdavFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *webdavFileInfo) IsDir() bool        { return fi.isDir }
func (fi *webdavFileInfo) Sys() any           { return nil }
