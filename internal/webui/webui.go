// Package webui serves the built Dyson Drive console from a static directory.
package webui

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Handler serves a built single-page application. Existing files are served
// as-is; unknown paths without a file extension fall back to index.html so
// client-side routes survive a refresh, while missing assets keep their 404.
type Handler struct {
	root  string
	files http.Handler
}

// New returns a handler rooted at dir. It fails when dir does not contain an
// index.html, which lets callers keep their API-only 404 behaviour instead of
// mounting a broken console.
func New(dir string) (*Handler, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("webui: static directory is empty")
	}

	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("webui: resolve %q: %w", dir, err)
	}

	index := filepath.Join(root, "index.html")
	info, err := os.Stat(index)
	if err != nil {
		return nil, fmt.Errorf("webui: no index.html in %q: %w", root, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("webui: %q is a directory", index)
	}

	return &Handler{root: root, files: http.FileServer(http.Dir(root))}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	upath := path.Clean("/" + r.URL.Path)
	if upath == "." {
		upath = "/"
	}
	if upath == "/" {
		h.serveIndex(w, "/", r)
		return
	}
	// The picker is a second document in the same build: a host embeds it in a
	// frame, so it needs a stable address that is not the drive's shell.
	if upath == "/picker" || upath == "/picker/" {
		if h.hasDocument("picker.html") {
			h.serveIndex(w, "/picker.html", r)
			return
		}
	}

	rel := strings.TrimPrefix(upath, "/")
	full := filepath.Join(h.root, filepath.FromSlash(rel))
	if !within(h.root, full) {
		http.NotFound(w, r)
		return
	}

	if info, err := os.Stat(full); err == nil && info.Mode().IsRegular() {
		h.serveFile(w, r, upath)
		return
	}

	// A request that names a file but misses it is a stale asset, not a
	// client-side route. Returning index.html here would feed HTML to a
	// script or stylesheet request.
	if path.Ext(upath) != "" {
		http.NotFound(w, r)
		return
	}

	h.serveIndex(w, "/", r)
}

func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, upath string) {
	w.Header().Set("Cache-Control", cacheControl(upath))
	h.files.ServeHTTP(w, withPath(r, upath))
}

func (h *Handler) serveIndex(w http.ResponseWriter, document string, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	h.files.ServeHTTP(w, withPath(r, document))
}

func (h *Handler) hasDocument(name string) bool {
	info, err := os.Stat(filepath.Join(h.root, name))
	return err == nil && info.Mode().IsRegular()
}

func cacheControl(upath string) string {
	// Vite emits content-hashed filenames under /assets, so they can be cached
	// forever. Everything else (favicons, manifest) stays short-lived.
	if strings.HasPrefix(upath, "/assets/") {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=3600"
}

func withPath(r *http.Request, upath string) *http.Request {
	clone := r.Clone(r.Context())
	clone.URL.Path = upath
	clone.URL.RawPath = ""
	return clone
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
