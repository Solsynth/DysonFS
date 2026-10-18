package core

import (
	"context"
	"strings"
	"sync"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

// AnalysisEngine is the analysis implementation DysonFS ships today. The
// registry keeps it behind an interface so a private build can supply another.
type AnalysisEngine interface {
	AnalyzeSourceFile(ctx context.Context, path, mimeType string) (*service.SourceAnalysis, error)
	ScheduleSourceReanalysis(fileID string) error
	ReanalyzeStoredObject(ctx context.Context, objectID string) (*database.CloudFile, error)
}

// AnalysisRegistry gates source analysis per file kind. Each
// app.solian.files.analysis.<kind> module enables one kind; with none enabled
// uploads store raw files and no metadata is derived.
type AnalysisRegistry struct {
	mu     sync.RWMutex
	kinds  map[string]bool
	engine AnalysisEngine
}

// NewAnalysisRegistry builds an empty registry over an engine.
func NewAnalysisRegistry(engine AnalysisEngine) *AnalysisRegistry {
	return &AnalysisRegistry{kinds: map[string]bool{}, engine: engine}
}

// Enable turns on one kind ("image", "video", "audio") or "all".
func (r *AnalysisRegistry) Enable(kind string) {
	if r == nil {
		return
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kinds[kind] = true
}

// Enabled reports whether a kind is on.
func (r *AnalysisRegistry) Enabled(kind string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.kinds[kind]
}

// Any reports whether at least one kind is enabled.
func (r *AnalysisRegistry) Any() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.kinds) > 0
}

// KindOf maps a MIME type to its analysis kind. It returns "" for kinds the
// registry does not model.
func KindOf(mimeType string) string {
	normalized := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
	switch {
	case strings.HasPrefix(normalized, "image/"):
		return "image"
	case strings.HasPrefix(normalized, "video/"):
		return "video"
	case strings.HasPrefix(normalized, "audio/"):
		return "audio"
	default:
		return ""
	}
}

// Handles reports whether the enabled set covers this MIME type.
func (r *AnalysisRegistry) Handles(mimeType string) bool {
	kind := KindOf(mimeType)
	if kind == "" {
		return false
	}
	return r.Enabled(kind)
}

// AnalyzeSource inspects a staged file when its kind is enabled.
func (r *AnalysisRegistry) AnalyzeSource(ctx context.Context, path, mimeType string) (*service.SourceAnalysis, error) {
	if r == nil || r.engine == nil || !r.Handles(mimeType) {
		return nil, nil
	}
	return r.engine.AnalyzeSourceFile(ctx, path, mimeType)
}

// NeedsServerReanalysis reports whether the enabled set wants a background
// pass for this MIME type.
func (r *AnalysisRegistry) NeedsServerReanalysis(mimeType string) bool {
	return r != nil && r.Handles(mimeType)
}

// ScheduleReanalysis flags an object for the background queue.
func (r *AnalysisRegistry) ScheduleReanalysis(fileID string) error {
	if r == nil || r.engine == nil || !r.Any() {
		return nil
	}
	return r.engine.ScheduleSourceReanalysis(fileID)
}

// ReanalyzeObject re-runs analysis for an object.
func (r *AnalysisRegistry) ReanalyzeObject(ctx context.Context, objectID string) (*database.CloudFile, error) {
	if r == nil || r.engine == nil || !r.Any() {
		return nil, nil
	}
	return r.engine.ReanalyzeStoredObject(ctx, objectID)
}

// DerivativeFunc produces every derivative one file kind supports.
type DerivativeFunc func(ctx context.Context, file *database.CloudFile, path string) error

// DerivativeRegistry holds one handler per file kind. A kind with no handler
// produces no derivatives, which is what a build without that module does.
type DerivativeRegistry struct {
	mu       sync.RWMutex
	handlers map[string]DerivativeFunc
}

// NewDerivativeRegistry builds an empty registry.
func NewDerivativeRegistry() *DerivativeRegistry {
	return &DerivativeRegistry{handlers: map[string]DerivativeFunc{}}
}

// Register installs the handler for a kind ("image", "video", "audio").
func (r *DerivativeRegistry) Register(kind string, fn DerivativeFunc) {
	if r == nil || fn == nil {
		return
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[kind] = fn
}

// Handler returns the handler covering this MIME type.
func (r *DerivativeRegistry) Handler(mimeType string) (DerivativeFunc, bool) {
	if r == nil {
		return nil, false
	}
	kind := KindOf(mimeType)
	if kind == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.handlers[kind]
	return fn, ok
}

// Any reports whether at least one derivative kind is registered.
func (r *DerivativeRegistry) Any() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.handlers) > 0
}
