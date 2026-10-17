package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

func TestRecordServerFailuresCountsUploadFailuresAndCapturesDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	files := service.NewFileService(&database.DB{}, storage.NewLocalBackend(t.TempDir()))
	r := gin.New()
	r.Use(recordServerFailures(files))
	r.GET("/api/files/upload/direct", func(c *gin.Context) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage unavailable"})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/files/upload/direct?token=do-not-log", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	snapshot := files.FailureLog().Snapshot(1)
	if snapshot.ServerFailureCount != 1 || snapshot.UploadFailureCount != 1 {
		t.Fatalf("failure counts = %+v, want one of each", snapshot)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].Path != "/api/files/upload/direct" || snapshot.Events[0].Detail != `{"error":"storage unavailable"}` {
		t.Fatalf("event = %+v, want upload path and response detail", snapshot.Events)
	}
}

func TestMountWebConsoleServesSPAAndKeepsAPI404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>console</title>"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	assets := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(assets, "index-abc.js"), []byte("console.log('hi')"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	r := gin.New()
	r.GET("/api/files/meta", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	r.NoRoute(func(c *gin.Context) { c.JSON(http.StatusNotFound, gin.H{"error": "not found"}) })
	mountWebConsole(r, &config.Config{Web: config.WebConfig{StaticDir: dir}})

	tests := []struct {
		target      string
		wantStatus  int
		wantBody    string
		wantContent string
	}{
		{target: "/", wantStatus: http.StatusOK, wantBody: "console", wantContent: "text/html"},
		{target: "/drive/folder/01ABC", wantStatus: http.StatusOK, wantBody: "console", wantContent: "text/html"},
		{target: "/assets/index-abc.js", wantStatus: http.StatusOK, wantBody: "console.log", wantContent: "javascript"},
		{target: "/api/unknown", wantStatus: http.StatusNotFound, wantBody: `"error":"not found"`, wantContent: "application/json"},
		{target: "/wopi/unknown", wantStatus: http.StatusNotFound, wantBody: `"error":"not found"`, wantContent: "application/json"},
		{target: "/assets/missing.js", wantStatus: http.StatusNotFound, wantContent: "text/plain"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.target, nil))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q, want it to contain %q", rec.Body.String(), tt.wantBody)
			}
			if got := rec.Header().Get("Content-Type"); !strings.Contains(got, tt.wantContent) {
				t.Fatalf("Content-Type = %q, want it to contain %q", got, tt.wantContent)
			}
		})
	}
}

func TestIsAPIPath(t *testing.T) {
	apiPaths := []string{
		"/api",
		"/api/files/meta",
		"/wopi/files/01ABC",
		"/webdav/",
		"/_dfs/version",
		"/swagger/index.html",
	}
	for _, path := range apiPaths {
		if !isAPIPath(path) {
			t.Fatalf("isAPIPath(%q) = false, want true", path)
		}
	}

	consolePaths := []string{"", "/", "/drive", "/drive/folder/01ABC", "/apiary", "/login"}
	for _, path := range consolePaths {
		if isAPIPath(path) {
			t.Fatalf("isAPIPath(%q) = true, want false", path)
		}
	}
}

func TestIsConsoleRequest(t *testing.T) {
	tests := []struct {
		method string
		target string
		want   bool
	}{
		{http.MethodGet, "/", true},
		{http.MethodGet, "/drive/folder/01ABC", true},
		{http.MethodHead, "/assets/index-abc.js", true},
		{http.MethodGet, "/api/files/meta", false},
		{http.MethodGet, "/wopi/files/01ABC", false},
		{http.MethodPost, "/api/files", false},
		{http.MethodPost, "/drive", false},
	}
	for _, tt := range tests {
		if got := isConsoleRequest(httptest.NewRequest(tt.method, tt.target, nil)); got != tt.want {
			t.Fatalf("isConsoleRequest(%s %s) = %v, want %v", tt.method, tt.target, got, tt.want)
		}
	}
}

func TestAuthenticateWebDAVAcceptsTokenIDAndSecret(t *testing.T) {
	files := newWebDAVAuthTestService(t)
	accountID := uuid.New()
	tokenID := database.NewID()
	secret := database.NewID()
	createWebDAVAuthTestToken(t, files, tokenID, accountID, secret)

	req := httptest.NewRequest("PROPFIND", "/webdav/", nil)
	req.SetBasicAuth(tokenID, secret)

	gotAccountID, ok := authenticateWebDAV(req, files)
	if !ok {
		t.Fatal("authenticateWebDAV() ok = false, want true")
	}
	if gotAccountID != accountID.String() {
		t.Fatalf("authenticateWebDAV() accountID = %q, want %q", gotAccountID, accountID.String())
	}
}

func TestAuthenticateWebDAVAcceptsEmbeddedPasswordToken(t *testing.T) {
	files := newWebDAVAuthTestService(t)
	accountID := uuid.New()
	tokenID := database.NewID()
	secret := database.NewID()
	createWebDAVAuthTestToken(t, files, tokenID, accountID, secret)

	req := httptest.NewRequest("PROPFIND", "/webdav/", nil)
	req.SetBasicAuth("anything", tokenID+":"+secret)

	gotAccountID, ok := authenticateWebDAV(req, files)
	if !ok {
		t.Fatal("authenticateWebDAV() ok = false, want true")
	}
	if gotAccountID != accountID.String() {
		t.Fatalf("authenticateWebDAV() accountID = %q, want %q", gotAccountID, accountID.String())
	}
}

func TestAuthenticateWebDAVAcceptsSecretOnlyPassword(t *testing.T) {
	files := newWebDAVAuthTestService(t)
	accountID := uuid.New()
	tokenID := database.NewID()
	secret := database.NewID()
	createWebDAVAuthTestToken(t, files, tokenID, accountID, secret)

	req := httptest.NewRequest("PROPFIND", "/webdav/", nil)
	req.SetBasicAuth("anything", secret)

	gotAccountID, ok := authenticateWebDAV(req, files)
	if !ok {
		t.Fatal("authenticateWebDAV() ok = false, want true")
	}
	if gotAccountID != accountID.String() {
		t.Fatalf("authenticateWebDAV() accountID = %q, want %q", gotAccountID, accountID.String())
	}
}

func newWebDAVAuthTestService(t *testing.T) *service.FileService {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+database.NewID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&database.WebDAVToken{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	return service.NewFileService(&database.DB{DB: db}, nil)
}

func createWebDAVAuthTestToken(t *testing.T, files *service.FileService, tokenID string, accountID uuid.UUID, secret string) {
	t.Helper()

	hashBytes, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	token := database.WebDAVToken{
		ID:        tokenID,
		AccountID: accountID,
		TokenHash: string(hashBytes),
		Label:     "test",
	}
	if err := files.DB().Create(&token).Error; err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}
