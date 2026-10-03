package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

type downloadURLResponse struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
	MimeType  string `json:"mime_type"`
	Size      int64  `json:"size"`
	Name      string `json:"name"`
}

func decodeDownloadURL(t *testing.T, w *httptest.ResponseRecorder) downloadURLResponse {
	t.Helper()
	var resp downloadURLResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return resp
}

func TestFileDownloadURLReturnsSignedURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)

	payload := []byte("hello world")
	fileID := seedMediaTestFile(t, db, stor, payload, "text/plain", "hash-url")

	r := gin.New()
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID+"/url", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	resp := decodeDownloadURL(t, w)
	if resp.URL == "" {
		t.Fatal("url is empty")
	}
	if resp.ID != fileID {
		t.Fatalf("id = %q, want %q", resp.ID, fileID)
	}
	if resp.MimeType != "text/plain" {
		t.Fatalf("mime_type = %q, want text/plain", resp.MimeType)
	}
	if resp.Size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", resp.Size, len(payload))
	}
	if resp.Name == "" {
		t.Fatal("name is empty")
	}
	expiresAt, err := time.Parse(time.RFC3339, resp.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expires_at %q: %v", resp.ExpiresAt, err)
	}
	if !expiresAt.After(time.Now()) {
		t.Fatalf("expires_at = %s, want in the future", resp.ExpiresAt)
	}

	// The legacy redirect route must be unchanged for the same id.
	reqRedirect := httptest.NewRequest(http.MethodGet, "/api/files/"+fileID, nil)
	wRedirect := httptest.NewRecorder()
	r.ServeHTTP(wRedirect, reqRedirect)
	if wRedirect.Code != http.StatusTemporaryRedirect {
		t.Fatalf("redirect status = %d, want %d", wRedirect.Code, http.StatusTemporaryRedirect)
	}
	if location := wRedirect.Header().Get("Location"); location == "" {
		t.Fatal("redirect Location header is empty")
	}
}

func TestFileDownloadURLPrefersCompressionVariant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)

	parentObjectID := database.NewID()
	parentFileID := database.NewID()
	parentKey := parentFileID
	parentPayload := []byte("original-bytes")

	childObjectID := database.NewID()
	childFileID := database.NewID()
	appType := "system.compression.low"
	childKey := parentFileID + ".compressed"
	childPayload := []byte("compressed-bytes")

	if err := db.Create(&database.FileObject{ID: parentObjectID, Size: int64(len(parentPayload)), MimeType: "image/png", Hash: "parent-hash", StorageKey: &parentKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create parent object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: parentFileID, Name: "photo.png", AccountID: uuid.New(), ObjectID: &parentObjectID, StorageKey: &parentKey, Indexed: true}).Error; err != nil {
		t.Fatalf("create parent file: %v", err)
	}
	if err := db.Create(&database.FileObject{ID: childObjectID, Size: int64(len(childPayload)), MimeType: "image/webp", Hash: "child-hash", StorageKey: &childKey, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create child object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: childFileID, Name: "photo.png", AccountID: uuid.New(), ObjectID: &childObjectID, ParentID: &parentFileID, StorageKey: &childKey, ApplicationType: &appType, Indexed: false}).Error; err != nil {
		t.Fatalf("create child file: %v", err)
	}
	if err := stor.Put(context.Background(), parentKey, strings.NewReader(string(parentPayload)), int64(len(parentPayload)), "image/png"); err != nil {
		t.Fatalf("put parent object: %v", err)
	}
	if err := stor.Put(context.Background(), childKey, strings.NewReader(string(childPayload)), int64(len(childPayload)), "image/webp"); err != nil {
		t.Fatalf("put child object: %v", err)
	}

	r := gin.New()
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+parentFileID+"/url", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	resp := decodeDownloadURL(t, w)
	if resp.ID != childFileID {
		t.Fatalf("id = %q, want child %q", resp.ID, childFileID)
	}
	if resp.MimeType != "image/webp" {
		t.Fatalf("mime_type = %q, want image/webp", resp.MimeType)
	}
	if resp.Size != int64(len(childPayload)) {
		t.Fatalf("size = %d, want %d", resp.Size, len(childPayload))
	}
	if !strings.Contains(resp.URL, childKey) {
		t.Fatalf("url = %q, want it to contain %q", resp.URL, childKey)
	}

	// The redirect route resolves to the same child variant.
	reqRedirect := httptest.NewRequest(http.MethodGet, "/api/files/"+parentFileID, nil)
	wRedirect := httptest.NewRecorder()
	r.ServeHTTP(wRedirect, reqRedirect)
	if wRedirect.Code != http.StatusTemporaryRedirect {
		t.Fatalf("redirect status = %d, want %d", wRedirect.Code, http.StatusTemporaryRedirect)
	}
	if location := wRedirect.Header().Get("Location"); !strings.Contains(location, childKey) {
		t.Fatalf("redirect location = %q, want it to contain %q", location, childKey)
	}

	// ?original=1 reports the parent instead.
	reqOriginal := httptest.NewRequest(http.MethodGet, "/api/files/"+parentFileID+"/url?original=1", nil)
	wOriginal := httptest.NewRecorder()
	r.ServeHTTP(wOriginal, reqOriginal)
	if wOriginal.Code != http.StatusOK {
		t.Fatalf("original status = %d, want %d, body = %s", wOriginal.Code, http.StatusOK, wOriginal.Body.String())
	}
	originalResp := decodeDownloadURL(t, wOriginal)
	if originalResp.ID != parentFileID {
		t.Fatalf("original id = %q, want parent %q", originalResp.ID, parentFileID)
	}
	if originalResp.MimeType != "image/png" {
		t.Fatalf("original mime_type = %q, want image/png", originalResp.MimeType)
	}
	if originalResp.Size != int64(len(parentPayload)) {
		t.Fatalf("original size = %d, want %d", originalResp.Size, len(parentPayload))
	}
	if !strings.Contains(originalResp.URL, parentKey) {
		t.Fatalf("original url = %q, want it to contain %q", originalResp.URL, parentKey)
	}
}

func TestFileDownloadURLUnknownIDReturnsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(t.TempDir()))

	r := gin.New()
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+database.NewID()+"/url", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "record not found") {
		t.Fatalf("body = %q, want it to contain %q", body, "record not found")
	}
}
