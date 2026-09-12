package handler

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/media"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

func mediaTestConfig() config.MediaConfig {
	return config.MediaConfig{
		Enable:             true,
		MaxSourceBytes:     10 * 1024 * 1024,
		MaxWidth:           4096,
		MaxHeight:          4096,
		MaxPixels:          40_000_000,
		MaxOutputBytes:     32 * 1024 * 1024,
		QualityMin:         40,
		QualityMax:         90,
		DefaultQuality:     80,
		AllowedFormats:     []string{"jpeg", "png", "webp", "avif"},
		CacheControlMaxAge: time.Hour,
	}
}

func mediaTestRouter(t *testing.T, cfg *config.Config, files *service.FileService, db *gorm.DB) *gin.Engine {
	t.Helper()
	r := gin.New()
	RegisterRoutes(r, cfg, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)
	return r
}

// seedMediaTestFile creates a CloudFile + FileObject pair and stores data in
// the backend under the object's storage key.
func seedMediaTestFile(t *testing.T, db *gorm.DB, stor storage.Backend, data []byte, mimeType, hash string) string {
	t.Helper()
	objectID := database.NewID()
	fileID := database.NewID()
	if err := db.Create(&database.FileObject{ID: objectID, Size: int64(len(data)), MimeType: mimeType, Hash: hash, StorageKey: &objectID}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "photo.jpg", AccountID: uuid.New(), ObjectID: &objectID, StorageKey: &objectID, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := stor.Put(context.Background(), objectID, bytes.NewReader(data), int64(len(data)), mimeType); err != nil {
		t.Fatalf("put object: %v", err)
	}
	return fileID
}

func decodeWebpDims(t *testing.T, data []byte) (int, int) {
	t.Helper()
	img, err := vips.NewImageFromBuffer(data)
	if err != nil {
		t.Fatalf("decode webp body: %v", err)
	}
	defer img.Close()
	return img.Width(), img.Height()
}

func TestOpenFileTransformJpegToPngResized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	jpeg := generateTestJPEG(t)
	fileID := seedMediaTestFile(t, db, stor, jpeg, "image/jpeg", "hash-1")

	cfg := &config.Config{Media: mediaTestConfig()}
	med, err := media.New(cfg.Media, stor)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	files.SetMedia(med)
	r := mediaTestRouter(t, cfg, files, db)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?width=32&format=png", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", ct)
	}
	img, err := png.Decode(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode png body: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
		t.Fatalf("decoded size = %dx%d, want 32x32", b.Dx(), b.Dy())
	}
	if etag := w.Header().Get("ETag"); etag == "" {
		t.Fatal("ETag header missing")
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "public") {
		t.Fatalf("Cache-Control = %q, want public", cc)
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Fatalf("unexpected redirect Location %q", loc)
	}
}

func TestOpenFileTransformPreset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	jpeg := generateTestJPEG(t)
	fileID := seedMediaTestFile(t, db, stor, jpeg, "image/jpeg", "hash-1")

	cfg := &config.Config{Media: mediaTestConfig()}
	cfg.Media.Presets = map[string]config.MediaPresetConfig{
		"thumb": {Width: 20, Height: 20, Fit: "cover", Gravity: "center", Format: "webp", Quality: 80},
	}
	med, err := media.New(cfg.Media, stor)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	files.SetMedia(med)
	r := mediaTestRouter(t, cfg, files, db)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?preset=thumb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preset status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/webp" {
		t.Fatalf("preset Content-Type = %q, want image/webp", ct)
	}
	width, height := decodeWebpDims(t, w.Body.Bytes())
	if width != 20 || height != 20 {
		t.Fatalf("preset decoded size = %dx%d, want 20x20", width, height)
	}

	// Explicit query values override the preset.
	req = httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?preset=thumb&width=40", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("override status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	width, _ = decodeWebpDims(t, w.Body.Bytes())
	if width != 40 {
		t.Fatalf("override decoded width = %d, want 40", width)
	}
}

func TestOpenFileTransformRejectsOutOfRangeWidth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	fileID := seedMediaTestFile(t, db, stor, generateTestJPEG(t), "image/jpeg", "hash-1")

	cfg := &config.Config{Media: mediaTestConfig()}
	med, err := media.New(cfg.Media, stor)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	files.SetMedia(med)
	r := mediaTestRouter(t, cfg, files, db)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?width=99999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "exceeds limit") {
		t.Fatalf("body = %q, want mention of limit", w.Body.String())
	}
}

func TestOpenFileTransformRejectsUnsupportedFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	fileID := seedMediaTestFile(t, db, stor, generateTestJPEG(t), "image/jpeg", "hash-1")

	cfg := &config.Config{Media: mediaTestConfig()}
	med, err := media.New(cfg.Media, stor)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	files.SetMedia(med)
	r := mediaTestRouter(t, cfg, files, db)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?format=bmp", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestOpenFileTransformRejectsDisallowedFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	fileID := seedMediaTestFile(t, db, stor, generateTestJPEG(t), "image/jpeg", "hash-1")

	cfg := &config.Config{Media: mediaTestConfig()}
	cfg.Media.AllowedFormats = []string{"webp"}
	med, err := media.New(cfg.Media, stor)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	files.SetMedia(med)
	r := mediaTestRouter(t, cfg, files, db)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?format=png", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not allowed") {
		t.Fatalf("body = %q, want mention of not allowed", w.Body.String())
	}
}

func TestOpenFileTransformRejectsOversizeSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	fileID := seedMediaTestFile(t, db, stor, generateTestJPEG(t), "image/jpeg", "hash-1")

	cfg := &config.Config{Media: mediaTestConfig()}
	cfg.Media.MaxSourceBytes = 1
	med, err := media.New(cfg.Media, stor)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	files.SetMedia(med)
	r := mediaTestRouter(t, cfg, files, db)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?width=32", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusRequestEntityTooLarge, w.Body.String())
	}
}

func TestOpenFileTransformRejectsNonImage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	fileID := seedMediaTestFile(t, db, stor, []byte("plain text"), "text/plain", "hash-1")

	cfg := &config.Config{Media: mediaTestConfig()}
	med, err := media.New(cfg.Media, stor)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	files.SetMedia(med)
	r := mediaTestRouter(t, cfg, files, db)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?width=32", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusUnsupportedMediaType, w.Body.String())
	}
}

func TestOpenFileTransformDisabledWhenMediaOff(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	jpeg := generateTestJPEG(t)
	fileID := seedMediaTestFile(t, db, stor, jpeg, "image/jpeg", "hash-1")

	// Thumbnail child so the no-transform path can serve a genuine redirect.
	thumbObjectID := database.NewID()
	thumbKey := fileID + ".thumbnail"
	thumbType := "system.thumbnail"
	if err := db.Create(&database.FileObject{ID: thumbObjectID, Size: int64(len(jpeg)), MimeType: "image/jpeg", StorageKey: &thumbKey}).Error; err != nil {
		t.Fatalf("create thumb object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), Name: "photo.jpg", AccountID: uuid.New(), ObjectID: &thumbObjectID, ParentID: &fileID, StorageKey: &thumbKey, ApplicationType: &thumbType, Indexed: false}).Error; err != nil {
		t.Fatalf("create thumb file: %v", err)
	}
	if err := stor.Put(context.Background(), thumbKey, bytes.NewReader(jpeg), int64(len(jpeg)), "image/jpeg"); err != nil {
		t.Fatalf("put thumb object: %v", err)
	}

	cfg := &config.Config{Media: config.MediaConfig{Enable: false}}
	r := mediaTestRouter(t, cfg, files, db)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?width=32", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "disabled") {
		t.Fatalf("status = %d, body = %s, want 400 disabled", w.Code, w.Body.String())
	}

	// Regression: no-param behavior (thumbnail variant) is untouched.
	req = httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?thumbnail=1", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("thumbnail status = %d, want %d, body = %s", w.Code, http.StatusTemporaryRedirect, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, thumbKey) {
		t.Fatalf("location = %q, want it to contain %q", loc, thumbKey)
	}
}

func TestOpenFileTransformCacheDiskSecondHit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	fileID := seedMediaTestFile(t, db, stor, generateTestJPEG(t), "image/jpeg", "hash-1")

	cacheDir := t.TempDir()
	cfg := &config.Config{Media: mediaTestConfig()}
	cfg.Media.Cache = config.MediaCacheConfig{Kind: "disk", Dir: cacheDir, MaxBytes: 10 * 1024 * 1024}
	med, err := media.New(cfg.Media, stor)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	files.SetMedia(med)
	r := mediaTestRouter(t, cfg, files, db)

	var firstBody, firstETag string
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?width=16&format=webp", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want %d, body = %s", i, w.Code, http.StatusOK, w.Body.String())
		}
		body := w.Body.String()
		etag := w.Header().Get("ETag")
		if etag == "" {
			t.Fatalf("request %d: ETag missing", i)
		}
		if i == 0 {
			firstBody, firstETag = body, etag
		} else {
			if body != firstBody {
				t.Fatal("second request body differs from first (cache not used)")
			}
			if etag != firstETag {
				t.Fatalf("second request ETag %q differs from first %q", etag, firstETag)
			}
		}
	}

	// The derivative must exist on disk under the sharded cache layout.
	cacheKey := strings.Trim(firstETag, `"`)
	cachePath := filepath.Join(cacheDir, cacheKey[:2], cacheKey[2:4], cacheKey)
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache file missing at %s: %v", cachePath, err)
	}
}

func TestOpenFileTransformCacheMemorySecondHit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	fileID := seedMediaTestFile(t, db, stor, generateTestJPEG(t), "image/jpeg", "hash-1")

	cfg := &config.Config{Media: mediaTestConfig()}
	cfg.Media.Cache = config.MediaCacheConfig{Kind: "memory", MaxBytes: 10 * 1024 * 1024}
	med, err := media.New(cfg.Media, stor)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	files.SetMedia(med)
	r := mediaTestRouter(t, cfg, files, db)

	var firstBody, firstETag string
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?width=16&format=webp", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want %d, body = %s", i, w.Code, http.StatusOK, w.Body.String())
		}
		body := w.Body.String()
		etag := w.Header().Get("ETag")
		if etag == "" {
			t.Fatalf("request %d: ETag missing", i)
		}
		if i == 0 {
			firstBody, firstETag = body, etag
		} else {
			if body != firstBody {
				t.Fatal("second request body differs from first (memory cache not used)")
			}
			if etag != firstETag {
				t.Fatalf("second request ETag %q differs from first %q", etag, firstETag)
			}
		}
	}
}

func TestOpenFileTransformCacheS3(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	base := t.TempDir()
	stor := storage.NewLocalBackend(base)
	files := service.NewFileService(&database.DB{DB: db}, stor)
	fileID := seedMediaTestFile(t, db, stor, generateTestJPEG(t), "image/jpeg", "hash-1")

	cfg := &config.Config{Media: mediaTestConfig()}
	// poolId identifies the dedicated cache pool; the app resolves its backend
	// (here the test passes the local backend directly).
	cfg.Media.Cache = config.MediaCacheConfig{Kind: "s3", PoolID: "01CACHEPOOL0000000000000000000", MaxBytes: 10 * 1024 * 1024}
	med, err := media.New(cfg.Media, stor)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	files.SetMedia(med)
	r := mediaTestRouter(t, cfg, files, db)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"?width=16&format=jpeg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", ct)
	}

	// The derivative must be stored under the media-cache/ prefix in the
	// default pool backend.
	cacheKey := strings.Trim(w.Header().Get("ETag"), `"`)
	cachePath := filepath.Join(base, "media-cache", cacheKey)
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("storage cache object missing at %s: %v", cachePath, err)
	}
}
