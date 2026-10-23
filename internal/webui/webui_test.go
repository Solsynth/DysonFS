package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()

	root := t.TempDir()
	writeTestFile(t, root, "index.html", "<!doctype html><title>console</title>")
	writeTestFile(t, root, filepath.Join("assets", "index-abc123.js"), "console.log('hi')")
	writeTestFile(t, root, "favicon.svg", "<svg></svg>")

	h, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return h
}

func writeTestFile(t *testing.T, root, name, body string) {
	t.Helper()

	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func serve(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// The picker is a second document in the same build, embedded by a host page.
// It must answer at `/picker` with its own HTML, and a build without it must
// keep the drive's fallback rather than a 404.
func TestHandlerServesPickerDocument(t *testing.T) {
	h := newTestHandler(t)
	writeTestFile(t, h.root, "picker.html", "<!doctype html><title>picker</title>")

	for _, target := range []string{"/picker", "/picker/"} {
		rec := serve(h, http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want %d", target, rec.Code, http.StatusOK)
		}
		if body := rec.Body.String(); !strings.Contains(body, "picker") {
			t.Fatalf("%s: body = %q, want the picker document", target, body)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s: Cache-Control = %q, want no-cache", target, got)
		}
	}
}


func TestHandlerServesIndexAndAssets(t *testing.T) {
	h := newTestHandler(t)

	tests := []struct {
		name        string
		target      string
		wantBody    string
		wantCache   string
		wantContent string
	}{
		{
			name:        "root",
			target:      "/",
			wantBody:    "console",
			wantCache:   "no-cache",
			wantContent: "text/html",
		},
		{
			name:        "client route falls back to index",
			target:      "/drive/folder/01ABC",
			wantBody:    "console",
			wantCache:   "no-cache",
			wantContent: "text/html",
		},
		{
			name:        "hashed asset",
			target:      "/assets/index-abc123.js",
			wantBody:    "console.log",
			wantCache:   "public, max-age=31536000, immutable",
			wantContent: "javascript",
		},
		{
			name:        "public file",
			target:      "/favicon.svg",
			wantBody:    "<svg>",
			wantCache:   "public, max-age=3600",
			wantContent: "image/svg+xml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(h, http.MethodGet, tt.target)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if body := rec.Body.String(); !strings.Contains(body, tt.wantBody) {
				t.Fatalf("body = %q, want it to contain %q", body, tt.wantBody)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.wantCache {
				t.Fatalf("Cache-Control = %q, want %q", got, tt.wantCache)
			}
			if got := rec.Header().Get("Content-Type"); !strings.Contains(got, tt.wantContent) {
				t.Fatalf("Content-Type = %q, want it to contain %q", got, tt.wantContent)
			}
		})
	}
}

func TestHandlerDoesNotFallBackForMissingAssets(t *testing.T) {
	h := newTestHandler(t)

	for _, target := range []string{"/assets/missing.js", "/logo.svg"} {
		rec := serve(h, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want %d", target, rec.Code, http.StatusNotFound)
		}
		if strings.Contains(rec.Body.String(), "console") {
			t.Fatalf("%s: served index.html for a missing asset", target)
		}
	}
}

func TestHandlerRejectsTraversalAndUnsupportedMethods(t *testing.T) {
	h := newTestHandler(t)

	rec := serve(h, http.MethodGet, "/../../etc/passwd")
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusOK {
		t.Fatalf("traversal status = %d, want 404 (or a harmless index fallback)", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "root:") {
		t.Fatal("traversal request leaked a file outside the console root")
	}

	rec = serve(h, http.MethodPost, "/drive")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("Allow = %q, want %q", got, "GET, HEAD")
	}
}

func TestHandlerSupportsHead(t *testing.T) {
	h := newTestHandler(t)

	rec := serve(h, http.MethodHead, "/assets/index-abc123.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body, _ := io.ReadAll(rec.Body); len(body) != 0 {
		t.Fatalf("HEAD body = %q, want empty", body)
	}
}

func TestNewRequiresIndexHTML(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("New(\"\") error = nil, want error")
	}
	if _, err := New(t.TempDir()); err == nil {
		t.Fatal("New(empty dir) error = nil, want error")
	}
}
