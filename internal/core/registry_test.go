package core

import (
	"context"
	"testing"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

type stubAnalysis struct {
	analyzed   int
	scheduled  int
	reanalyzed int
}

func (s *stubAnalysis) AnalyzeSourceFile(context.Context, string, string) (*service.SourceAnalysis, error) {
	s.analyzed++
	return &service.SourceAnalysis{Width: 10, Height: 20}, nil
}

func (s *stubAnalysis) ScheduleSourceReanalysis(string) error {
	s.scheduled++
	return nil
}

func (s *stubAnalysis) ReanalyzeStoredObject(context.Context, string) (*database.CloudFile, error) {
	s.reanalyzed++
	return &database.CloudFile{}, nil
}

// TestAnalysisRegistryDisabledAnalyzesNothing is the core guarantee of the
// optional analysis modules: with no kind enabled, uploads are stored without
// derived metadata and nothing is queued.
func TestAnalysisRegistryDisabledAnalyzesNothing(t *testing.T) {
	engine := &stubAnalysis{}
	registry := NewAnalysisRegistry(engine)

	if registry.Any() {
		t.Fatal("Any() = true for an empty registry")
	}
	analysis, err := registry.AnalyzeSource(context.Background(), "/tmp/x.jpg", "image/jpeg")
	if err != nil || analysis != nil {
		t.Fatalf("AnalyzeSource() = %#v, %v; want nil, nil", analysis, err)
	}
	if registry.NeedsServerReanalysis("image/jpeg") {
		t.Fatal("NeedsServerReanalysis() = true for an empty registry")
	}
	if err := registry.ScheduleReanalysis("file"); err != nil {
		t.Fatalf("ScheduleReanalysis() error = %v", err)
	}
	if _, err := registry.ReanalyzeObject(context.Background(), "object"); err != nil {
		t.Fatalf("ReanalyzeObject() error = %v", err)
	}
	if engine.analyzed+engine.scheduled+engine.reanalyzed != 0 {
		t.Fatalf("engine was called %d/%d/%d times, want 0", engine.analyzed, engine.scheduled, engine.reanalyzed)
	}
}

// TestAnalysisRegistryOnlyHandlesEnabledKinds covers the per-kind gate: enabling
// image analysis must not analyze video or audio.
func TestAnalysisRegistryOnlyHandlesEnabledKinds(t *testing.T) {
	engine := &stubAnalysis{}
	registry := NewAnalysisRegistry(engine)
	registry.Enable("image")

	if !registry.NeedsServerReanalysis("image/png") {
		t.Fatal("image analysis was enabled but does not handle image/png")
	}
	if registry.NeedsServerReanalysis("video/mp4") || registry.NeedsServerReanalysis("audio/mpeg") {
		t.Fatal("a video/audio MIME is handled while only image analysis is enabled")
	}
	if analysis, err := registry.AnalyzeSource(context.Background(), "/tmp/x.mp4", "video/mp4"); err != nil || analysis != nil {
		t.Fatalf("AnalyzeSource(video) = %#v, %v; want nil, nil", analysis, err)
	}
	if engine.analyzed != 0 {
		t.Fatalf("engine analyzed %d files, want 0", engine.analyzed)
	}
	if analysis, err := registry.AnalyzeSource(context.Background(), "/tmp/x.png", "image/png"); err != nil || analysis == nil {
		t.Fatalf("AnalyzeSource(image) = %#v, %v; want an analysis", analysis, err)
	}
	if engine.analyzed != 1 {
		t.Fatalf("engine analyzed %d files, want 1", engine.analyzed)
	}
}

func TestKindOf(t *testing.T) {
	tests := map[string]string{
		"image/jpeg":            "image",
		"IMAGE/PNG":             "image",
		"video/mp4":             "video",
		"audio/mpeg":            "audio",
		"application/pdf":       "",
		"image/svg+xml;charset": "image",
	}
	for mime, want := range tests {
		if got := KindOf(mime); got != want {
			t.Fatalf("KindOf(%q) = %q, want %q", mime, got, want)
		}
	}
}

// TestDerivativeRegistryDisabledProducesNothing proves a build without the
// derivative modules writes no previews.
func TestDerivativeRegistryDisabledProducesNothing(t *testing.T) {
	registry := NewDerivativeRegistry()
	if registry.Any() {
		t.Fatal("Any() = true for an empty registry")
	}
	if _, ok := registry.Handler("image/jpeg"); ok {
		t.Fatal("Handler() found a handler in an empty registry")
	}
}

func TestDerivativeRegistryDispatchesByKind(t *testing.T) {
	registry := NewDerivativeRegistry()
	seen := ""
	registry.Register("image", func(context.Context, *database.CloudFile, string) error {
		seen = "image"
		return nil
	})
	handler, ok := registry.Handler("image/webp")
	if !ok {
		t.Fatal("Handler(image/webp) = !ok")
	}
	if err := handler(context.Background(), nil, "/tmp/x"); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if seen != "image" {
		t.Fatalf("handler kind = %q, want image", seen)
	}
	if _, ok := registry.Handler("video/mp4"); ok {
		t.Fatal("Handler(video/mp4) = ok while only image is registered")
	}
}
