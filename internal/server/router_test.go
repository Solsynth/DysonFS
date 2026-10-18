package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

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

// TestHostServesSPAAndKeepsAPI404 covers the host-level contract: the console
// owns non-API GETs, and every API path keeps a JSON 404 even when no module
// mounted a matching route.
func TestHostServesSPAAndKeepsAPI404(t *testing.T) {
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

	files := service.NewFileService(&database.DB{}, storage.NewLocalBackend(t.TempDir()))
	cfg := &config.Config{Web: config.WebConfig{StaticDir: dir}}
	r := New(cfg, "master", files).Engine

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

// TestHostWithoutConsoleKeepsJSON404 verifies an unbuilt console leaves the API
// only and never falls back to a stray file.
func TestHostWithoutConsoleKeepsJSON404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	files := service.NewFileService(&database.DB{}, storage.NewLocalBackend(t.TempDir()))
	srv := New(&config.Config{Web: config.WebConfig{StaticDir: t.TempDir()}}, "master", files)
	if srv.WebError == nil {
		t.Fatal("WebError = nil, want an error for a dist dir without index.html")
	}

	rec := httptest.NewRecorder()
	srv.Engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/drive", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHostHealthAndModulesEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	files := service.NewFileService(&database.DB{}, storage.NewLocalBackend(t.TempDir()))
	srv := New(&config.Config{}, "master", files)
	srv.MountModules(ModulesResponse{
		Modules:      []ModuleInfo{{ID: "app.solian.files.core", Kind: "feature"}},
		Capabilities: []string{"files.read"},
	})

	health := httptest.NewRecorder()
	srv.Engine.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"mode":"master"`) {
		t.Fatalf("health = %d %s", health.Code, health.Body.String())
	}

	modules := httptest.NewRecorder()
	srv.Engine.ServeHTTP(modules, httptest.NewRequest(http.MethodGet, "/api/modules", nil))
	if modules.Code != http.StatusOK {
		t.Fatalf("modules status = %d, want 200", modules.Code)
	}
	body := modules.Body.String()
	if !strings.Contains(body, "app.solian.files.core") || !strings.Contains(body, "files.read") {
		t.Fatalf("modules body = %s, want the resolved module and capability", body)
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
