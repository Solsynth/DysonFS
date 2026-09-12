package media

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"src.solsynth.dev/sosys/filesystem/internal/config"
)

func testMediaConfig() config.MediaConfig {
	return config.MediaConfig{
		MaxWidth:       4096,
		MaxHeight:      4096,
		QualityMin:     40,
		QualityMax:     90,
		DefaultQuality: 80,
		AllowedFormats: []string{"jpeg", "png", "webp", "avif"},
	}
}

func mustParseQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	q, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("parse query %q: %v", raw, err)
	}
	return q
}

func TestMediaParamsDefaults(t *testing.T) {
	p, err := ParseParams(testMediaConfig(), mustParseQuery(t, ""))
	if err != nil {
		t.Fatalf("ParseParams() error = %v", err)
	}
	if p.Width != 0 || p.Height != 0 {
		t.Fatalf("width/height = %d/%d, want 0/0", p.Width, p.Height)
	}
	if p.Fit != FitCover {
		t.Fatalf("Fit = %q, want %q", p.Fit, FitCover)
	}
	if p.Gravity != GravityCenter {
		t.Fatalf("Gravity = %q, want %q", p.Gravity, GravityCenter)
	}
	if p.Format != "" {
		t.Fatalf("Format = %q, want empty (resolve at render)", p.Format)
	}
	if p.Quality != 80 {
		t.Fatalf("Quality = %d, want default 80", p.Quality)
	}
}

func TestMediaParamsParsesAllFields(t *testing.T) {
	p, err := ParseParams(testMediaConfig(), mustParseQuery(t, "width=320&height=240&fit=contain&gravity=top&format=webp&quality=60"))
	if err != nil {
		t.Fatalf("ParseParams() error = %v", err)
	}
	if p.Width != 320 || p.Height != 240 {
		t.Fatalf("width/height = %d/%d, want 320/240", p.Width, p.Height)
	}
	if p.Fit != FitContain || p.Gravity != GravityTop || p.Format != FormatWebp || p.Quality != 60 {
		t.Fatalf("params = %+v", p)
	}
}

func TestMediaParamsJpgAlias(t *testing.T) {
	p, err := ParseParams(testMediaConfig(), mustParseQuery(t, "format=jpg"))
	if err != nil {
		t.Fatalf("ParseParams() error = %v", err)
	}
	if p.Format != FormatJpeg {
		t.Fatalf("Format = %q, want %q", p.Format, FormatJpeg)
	}
}

func TestMediaParamsPresetMerge(t *testing.T) {
	cfg := testMediaConfig()
	cfg.Presets = map[string]config.MediaPresetConfig{
		"thumb": {Width: 256, Height: 256, Fit: "cover", Gravity: "center", Format: "webp", Quality: 75},
	}
	p, err := ParseParams(cfg, mustParseQuery(t, "preset=thumb"))
	if err != nil {
		t.Fatalf("ParseParams() error = %v", err)
	}
	if p.Preset != "thumb" || p.Width != 256 || p.Height != 256 || p.Fit != FitCover || p.Gravity != GravityCenter || p.Format != FormatWebp || p.Quality != 75 {
		t.Fatalf("preset params = %+v", p)
	}

	// Explicit query values override preset fields.
	p, err = ParseParams(cfg, mustParseQuery(t, "preset=thumb&width=640&quality=85"))
	if err != nil {
		t.Fatalf("ParseParams(override) error = %v", err)
	}
	if p.Width != 640 || p.Quality != 85 {
		t.Fatalf("overridden params = %+v, want width 640 quality 85", p)
	}
	if p.Height != 256 || p.Format != FormatWebp {
		t.Fatalf("preset fields lost: %+v", p)
	}
}

func TestMediaParamsPresetPartialFillsDefaults(t *testing.T) {
	cfg := testMediaConfig()
	cfg.Presets = map[string]config.MediaPresetConfig{
		"avatar": {Width: 128, Height: 128, Format: "webp"},
	}
	p, err := ParseParams(cfg, mustParseQuery(t, "preset=avatar"))
	if err != nil {
		t.Fatalf("ParseParams() error = %v", err)
	}
	if p.Fit != FitCover || p.Gravity != GravityCenter || p.Quality != 80 {
		t.Fatalf("defaults not applied for partial preset: %+v", p)
	}
}

func TestMediaParamsRejectsUnknownPreset(t *testing.T) {
	_, err := ParseParams(testMediaConfig(), mustParseQuery(t, "preset=nope"))
	if !errors.Is(err, ErrInvalidParams) || err == nil || !strings.Contains(err.Error(), `unknown preset "nope"`) {
		t.Fatalf("error = %v, want ErrInvalidParams wrapping unknown preset", err)
	}
}

func TestMediaParamsRejectsBadWidth(t *testing.T) {
	for _, q := range []string{"width=abc", "width=0", "width=-5"} {
		if _, err := ParseParams(testMediaConfig(), mustParseQuery(t, q)); !errors.Is(err, ErrInvalidParams) {
			t.Fatalf("%s: error = %v, want ErrInvalidParams", q, err)
		}
	}
}

func TestMediaParamsRejectsWidthOverLimit(t *testing.T) {
	_, err := ParseParams(testMediaConfig(), mustParseQuery(t, "width=99999"))
	if !errors.Is(err, ErrInvalidParams) || !strings.Contains(err.Error(), "width 99999 exceeds limit 4096") {
		t.Fatalf("error = %v, want width exceeds limit", err)
	}
}

func TestMediaParamsRejectsHeightOverLimit(t *testing.T) {
	_, err := ParseParams(testMediaConfig(), mustParseQuery(t, "height=5000"))
	if !errors.Is(err, ErrInvalidParams) || !strings.Contains(err.Error(), "height 5000 exceeds limit 4096") {
		t.Fatalf("error = %v, want height exceeds limit", err)
	}
}

func TestMediaParamsRejectsBadQuality(t *testing.T) {
	_, err := ParseParams(testMediaConfig(), mustParseQuery(t, "quality=abc"))
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("error = %v, want ErrInvalidParams", err)
	}
}

func TestMediaParamsRejectsQualityBelowMin(t *testing.T) {
	_, err := ParseParams(testMediaConfig(), mustParseQuery(t, "quality=20"))
	if !errors.Is(err, ErrInvalidParams) || !strings.Contains(err.Error(), "quality 20 must be between 40 and 90") {
		t.Fatalf("error = %v, want quality range error", err)
	}
}

func TestMediaParamsRejectsQualityAboveMax(t *testing.T) {
	_, err := ParseParams(testMediaConfig(), mustParseQuery(t, "quality=95"))
	if !errors.Is(err, ErrInvalidParams) || !strings.Contains(err.Error(), "quality 95 must be between 40 and 90") {
		t.Fatalf("error = %v, want quality range error", err)
	}
}

func TestMediaParamsRejectsPresetQualityOutOfRange(t *testing.T) {
	cfg := testMediaConfig()
	cfg.Presets = map[string]config.MediaPresetConfig{
		"low": {Width: 64, Quality: 10},
	}
	if _, err := ParseParams(cfg, mustParseQuery(t, "preset=low")); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("error = %v, want ErrInvalidParams for out-of-range preset quality", err)
	}
}

func TestMediaParamsRejectsBadFit(t *testing.T) {
	_, err := ParseParams(testMediaConfig(), mustParseQuery(t, "fit=crop"))
	if !errors.Is(err, ErrInvalidParams) || !strings.Contains(err.Error(), `unsupported fit "crop"`) {
		t.Fatalf("error = %v, want unsupported fit", err)
	}
}

func TestMediaParamsRejectsBadGravity(t *testing.T) {
	_, err := ParseParams(testMediaConfig(), mustParseQuery(t, "gravity=up"))
	if !errors.Is(err, ErrInvalidParams) || !strings.Contains(err.Error(), `unsupported gravity "up"`) {
		t.Fatalf("error = %v, want unsupported gravity", err)
	}
}

func TestMediaParamsRejectsBadFormat(t *testing.T) {
	_, err := ParseParams(testMediaConfig(), mustParseQuery(t, "format=bmp"))
	if !errors.Is(err, ErrInvalidParams) || !strings.Contains(err.Error(), `unsupported format "bmp"`) {
		t.Fatalf("error = %v, want unsupported format", err)
	}
}

func TestMediaParamsRejectsDisallowedFormat(t *testing.T) {
	cfg := testMediaConfig()
	cfg.AllowedFormats = []string{"webp"}
	_, err := ParseParams(cfg, mustParseQuery(t, "format=png"))
	if !errors.Is(err, ErrInvalidParams) || !strings.Contains(err.Error(), `format "png" not allowed`) {
		t.Fatalf("error = %v, want format not allowed", err)
	}
}

func TestMediaParamsIgnoresUnknownKeys(t *testing.T) {
	p, err := ParseParams(testMediaConfig(), mustParseQuery(t, "width=32&rotate=90&foo=bar"))
	if err != nil {
		t.Fatalf("ParseParams() error = %v", err)
	}
	if p.Width != 32 {
		t.Fatalf("Width = %d, want 32", p.Width)
	}
}

func TestMediaParamsCacheKeyStable(t *testing.T) {
	p := Params{Width: 320, Height: 240, Fit: FitCover, Gravity: GravityCenter, Format: FormatWebp, Quality: 75}
	k1 := p.CacheKey("file-1", "hash-1", 1234)
	k2 := p.CacheKey("file-1", "hash-1", 1234)
	if k1 != k2 {
		t.Fatalf("CacheKey not stable: %q vs %q", k1, k2)
	}
	if len(k1) != 64 {
		t.Fatalf("CacheKey length = %d, want 64 (sha256 hex)", len(k1))
	}
}

func TestMediaParamsCacheKeySensitive(t *testing.T) {
	base := Params{Width: 320, Height: 240, Fit: FitCover, Gravity: GravityCenter, Format: FormatWebp, Quality: 75}
	baseKey := base.CacheKey("file-1", "hash-1", 1234)
	variants := []Params{
		{Width: 640, Height: 240, Fit: FitCover, Gravity: GravityCenter, Format: FormatWebp, Quality: 75},
		{Width: 320, Height: 480, Fit: FitCover, Gravity: GravityCenter, Format: FormatWebp, Quality: 75},
		{Width: 320, Height: 240, Fit: FitContain, Gravity: GravityCenter, Format: FormatWebp, Quality: 75},
		{Width: 320, Height: 240, Fit: FitCover, Gravity: GravityTop, Format: FormatWebp, Quality: 75},
		{Width: 320, Height: 240, Fit: FitCover, Gravity: GravityCenter, Format: FormatPng, Quality: 75},
		{Width: 320, Height: 240, Fit: FitCover, Gravity: GravityCenter, Format: FormatWebp, Quality: 60},
	}
	for i, v := range variants {
		if v.CacheKey("file-1", "hash-1", 1234) == baseKey {
			t.Fatalf("variant %d produced identical cache key", i)
		}
	}
	if base.CacheKey("file-2", "hash-1", 1234) == baseKey {
		t.Fatal("cache key not sensitive to object id")
	}
	if base.CacheKey("file-1", "hash-2", 1234) == baseKey {
		t.Fatal("cache key not sensitive to object hash")
	}
	if base.CacheKey("file-1", "hash-1", 9999) == baseKey {
		t.Fatal("cache key not sensitive to object size")
	}
}
