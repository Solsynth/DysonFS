package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

// TestProxiedUploadStoresBytesInRequestedPool drives the proxied chunked upload
// with an explicit non-default pool_id. The uploaded bytes must land in that
// pool's backend: the file row records pool_id, and every read resolves the
// backend from it, so bytes written to the default pool would be unreachable.
func TestProxiedUploadStoresBytesInRequestedPool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.PersistentTask{}, &database.UploadChunk{}, &database.PoolPermission{})
	defaultDir := t.TempDir()
	poolDir := t.TempDir()
	defaultPoolID := database.NewID()
	poolID := database.NewID()
	accountID := uuid.New()
	cfg := &config.Config{
		Pools: []config.PoolConfig{
			{ID: defaultPoolID, Name: "default", Default: true, Storage: config.StoragePoolConfig{Endpoint: defaultDir}},
			{ID: poolID, Name: "explicit", Storage: config.StoragePoolConfig{Endpoint: poolDir}},
		},
	}
	cfg.Storage.TempDir = t.TempDir()

	// Mirrors app.New: SeedPools resolves the default, then the service's
	// ambient backend is the default pool's.
	files := service.NewFileService(&database.DB{DB: db}, nil)
	if _, err := files.SeedPools(cfg); err != nil {
		t.Fatalf("SeedPools: %v", err)
	}
	defaultBackend, err := files.BackendForPoolID(&defaultPoolID)
	if err != nil {
		t.Fatalf("resolve default backend: %v", err)
	}
	files.SetStorage(defaultBackend)
	tasks := service.NewTaskService(&database.DB{DB: db})
	quota := service.NewQuotaService(&database.DB{DB: db})
	if err := db.Create(&database.PoolPermission{ID: database.NewID(), PoolID: poolID, SubjectType: "account", SubjectID: accountID.String(), Permission: "write"}).Error; err != nil {
		t.Fatalf("grant pool write: %v", err)
	}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, cfg, files, nil, tasks, quota, nil, &recordingDispatcher{})

	content := []byte("hello")

	body := fmt.Sprintf(`{"file_name":"x.txt","file_size":%d,"content_type":"text/plain","pool_id":%q,"chunk_size":%d}`, len(content), poolID, len(content))
	req := httptest.NewRequest(http.MethodPost, "/api/files/upload/create", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", w.Code, w.Body.String())
	}
	var created struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.TaskID == "" {
		t.Fatalf("create response missing task_id: %s", w.Body.String())
	}

	var chunk bytes.Buffer
	mw := multipart.NewWriter(&chunk)
	part, err := mw.CreateFormFile("chunk", "chunk")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write chunk: %v", err)
	}
	mw.Close()
	req = httptest.NewRequest(http.MethodPost, "/api/files/upload/chunk/"+created.TaskID+"/0", &chunk)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("chunk status = %d, body = %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/files/upload/complete/"+created.TaskID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", w.Code, w.Body.String())
	}
	var completed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &completed); err != nil {
		t.Fatalf("decode complete: %v", err)
	}

	var file database.CloudFile
	if err := db.Preload("Object").First(&file, "id = ?", completed.ID).Error; err != nil {
		t.Fatalf("load file: %v", err)
	}
	if file.PoolID == nil || *file.PoolID != poolID {
		t.Fatalf("file.PoolID = %v, want %q", file.PoolID, poolID)
	}
	if file.Object == nil || file.Object.StorageKey == nil {
		t.Fatalf("file = %+v, want a stored object", file)
	}
	key := *file.Object.StorageKey

	if _, err := os.Stat(filepath.Join(defaultDir, key)); !os.IsNotExist(err) {
		t.Fatalf("bytes for pool %s landed in the default pool backend (stat err = %v)", poolID, err)
	}
	stored, err := os.Stat(filepath.Join(poolDir, key))
	if err != nil {
		t.Fatalf("bytes missing from the requested pool: %v", err)
	}
	if stored.Size() != int64(len(content)) {
		t.Fatalf("stored size = %d, want %d", stored.Size(), len(content))
	}

	// Reads resolve through the file's pool, so the object must be reachable
	// from the backend the file claims.
	backend, err := files.BackendForFile(&file)
	if err != nil {
		t.Fatalf("resolve backend: %v", err)
	}
	rc, info, err := backend.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("file claims pool %s but its object is unreadable there: %v", poolID, err)
	}
	defer rc.Close()
	if info.Size != int64(len(content)) {
		t.Fatalf("object size = %d, want %d", info.Size, len(content))
	}

	// And the download endpoint must sign against that same pool.
	req = httptest.NewRequest(http.MethodGet, "/api/files/"+file.ID, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("open status = %d, want 307, body = %s", w.Code, w.Body.String())
	}
	if location := w.Header().Get("Location"); !bytes.Contains([]byte(location), []byte("?name=x.txt")) {
		t.Fatalf("signed url = %q, want a redirect for the stored object", location)
	}
}

// TestProxiedUploadOverwriteKeepsTargetPool verifies an overwrite writes the
// replacement bytes into the pool the existing file already belongs to, since
// OverwriteFile leaves the file row's pool untouched.
func TestProxiedUploadOverwriteKeepsTargetPool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.PersistentTask{}, &database.UploadChunk{}, &database.PoolPermission{})
	defaultDir := t.TempDir()
	poolDir := t.TempDir()
	defaultPoolID := database.NewID()
	poolID := database.NewID()
	accountID := uuid.New()
	cfg := &config.Config{
		Pools: []config.PoolConfig{
			{ID: defaultPoolID, Name: "default", Default: true, Storage: config.StoragePoolConfig{Endpoint: defaultDir}},
			{ID: poolID, Name: "explicit", Storage: config.StoragePoolConfig{Endpoint: poolDir}},
		},
	}
	cfg.Storage.TempDir = t.TempDir()

	files := service.NewFileService(&database.DB{DB: db}, nil)
	if _, err := files.SeedPools(cfg); err != nil {
		t.Fatalf("SeedPools: %v", err)
	}
	defaultBackend, err := files.BackendForPoolID(&defaultPoolID)
	if err != nil {
		t.Fatalf("resolve default backend: %v", err)
	}
	files.SetStorage(defaultBackend)
	tasks := service.NewTaskService(&database.DB{DB: db})
	quota := service.NewQuotaService(&database.DB{DB: db})

	// An existing file that already lives in the non-default pool.
	oldObjectID := database.NewID()
	oldKey := "uploads/" + oldObjectID
	var oldKeyPtr = &oldKey
	if err := db.Create(&database.FileObject{ID: oldObjectID, Size: 3, MimeType: "text/plain", Hash: "old", StorageKey: oldKeyPtr, Meta: []byte(`{}`)}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	fileID := database.NewID()
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "doc.txt", AccountID: accountID, PoolID: &poolID, ObjectID: &oldObjectID, StorageKey: oldKeyPtr, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	poolBackend, err := files.BackendForPoolID(&poolID)
	if err != nil {
		t.Fatalf("resolve pool backend: %v", err)
	}
	if err := poolBackend.Put(context.Background(), oldKey, bytes.NewReader([]byte("old")), 3, "text/plain"); err != nil {
		t.Fatalf("seed old object: %v", err)
	}
	if err := db.Create(&database.PoolPermission{ID: database.NewID(), PoolID: poolID, SubjectType: "account", SubjectID: accountID.String(), Permission: "write"}).Error; err != nil {
		t.Fatalf("grant pool write: %v", err)
	}

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, cfg, files, nil, tasks, quota, nil, &recordingDispatcher{})

	content := []byte("replacement")
	body := fmt.Sprintf(`{"file_name":"ignored.txt","file_size":%d,"content_type":"text/plain","overwrite_id":%q,"chunk_size":%d}`, len(content), fileID, len(content))
	req := httptest.NewRequest(http.MethodPost, "/api/files/upload/create", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", w.Code, w.Body.String())
	}
	var created struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	var chunk bytes.Buffer
	mw := multipart.NewWriter(&chunk)
	part, err := mw.CreateFormFile("chunk", "chunk")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write chunk: %v", err)
	}
	mw.Close()
	req = httptest.NewRequest(http.MethodPost, "/api/files/upload/chunk/"+created.TaskID+"/0", &chunk)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("chunk status = %d, body = %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/files/upload/complete/"+created.TaskID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", w.Code, w.Body.String())
	}

	var file database.CloudFile
	if err := db.Preload("Object").First(&file, "id = ?", fileID).Error; err != nil {
		t.Fatalf("reload file: %v", err)
	}
	if file.PoolID == nil || *file.PoolID != poolID {
		t.Fatalf("overwritten file.PoolID = %v, want %q", file.PoolID, poolID)
	}
	if file.Object == nil || file.Object.StorageKey == nil {
		t.Fatalf("file = %+v, want the replacement object", file)
	}
	key := *file.Object.StorageKey
	if _, err := os.Stat(filepath.Join(poolDir, key)); err != nil {
		t.Fatalf("replacement bytes missing from the file's pool: %v", err)
	}
	if _, err := os.Stat(filepath.Join(defaultDir, key)); !os.IsNotExist(err) {
		t.Fatalf("replacement bytes leaked into the default pool (stat err = %v)", err)
	}
}
