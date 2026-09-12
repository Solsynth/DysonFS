package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"src.solsynth.dev/sosys/filesystem/internal/config"
)

type Fit string

const (
	FitCover   Fit = "cover"   // default; scale down and crop to fill the box
	FitContain Fit = "contain" // scale to fit inside the box; may enlarge
	FitFill    Fit = "fill"    // stretch independently in both axes
	FitInside  Fit = "inside"  // contain, but never enlarge
	FitOutside Fit = "outside" // scale to cover the box; may enlarge
)

type Gravity string

const (
	GravityCenter  Gravity = "center"
	GravityTop     Gravity = "top"
	GravityBottom  Gravity = "bottom"
	GravityLeft    Gravity = "left"
	GravityRight   Gravity = "right"
	GravityEntropy Gravity = "entropy"
)

type Format string

const (
	FormatJpeg Format = "jpeg"
	FormatPng  Format = "png"
	FormatWebp Format = "webp"
	FormatAvif Format = "avif"
)

type Params struct {
	Preset  string
	Width   int
	Height  int
	Fit     Fit
	Gravity Gravity
	Format  Format // zero value "" = resolve at render from source format
	Quality int
}

// paramKeys are the transform query keys. Unknown keys are ignored (matches
// gin query conventions).
var paramKeys = []string{"width", "height", "fit", "gravity", "format", "quality"}

// ParseParams is the single source of truth for transform validation: named
// presets and explicit query parameters go through the same rules. Explicit
// query values override preset fields.
func ParseParams(cfg config.MediaConfig, query url.Values) (Params, error) {
	explicit := map[string]string{}
	for _, k := range paramKeys {
		if v, ok := query[k]; ok && len(v) > 0 && v[0] != "" {
			explicit[k] = v[0]
		}
	}

	raw := map[string]string{}
	var p Params
	if preset := query.Get("preset"); preset != "" {
		presetCfg, ok := cfg.Presets[preset]
		if !ok {
			return Params{}, fmt.Errorf("%w: unknown preset %q", ErrInvalidParams, preset)
		}
		p.Preset = preset
		if presetCfg.Width > 0 {
			raw["width"] = strconv.Itoa(presetCfg.Width)
		}
		if presetCfg.Height > 0 {
			raw["height"] = strconv.Itoa(presetCfg.Height)
		}
		if presetCfg.Fit != "" {
			raw["fit"] = presetCfg.Fit
		}
		if presetCfg.Gravity != "" {
			raw["gravity"] = presetCfg.Gravity
		}
		if presetCfg.Format != "" {
			raw["format"] = presetCfg.Format
		}
		if presetCfg.Quality > 0 {
			raw["quality"] = strconv.Itoa(presetCfg.Quality)
		}
	}
	// Explicit query values win over preset fields.
	for k, v := range explicit {
		raw[k] = v
	}

	if v, ok := raw["width"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Params{}, fmt.Errorf("%w: width must be a positive integer", ErrInvalidParams)
		}
		if n > cfg.MaxWidth {
			return Params{}, fmt.Errorf("%w: width %d exceeds limit %d", ErrInvalidParams, n, cfg.MaxWidth)
		}
		p.Width = n
	}
	if v, ok := raw["height"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Params{}, fmt.Errorf("%w: height must be a positive integer", ErrInvalidParams)
		}
		if n > cfg.MaxHeight {
			return Params{}, fmt.Errorf("%w: height %d exceeds limit %d", ErrInvalidParams, n, cfg.MaxHeight)
		}
		p.Height = n
	}

	if v, ok := raw["quality"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Params{}, fmt.Errorf("%w: quality must be an integer", ErrInvalidParams)
		}
		if n < cfg.QualityMin || n > cfg.QualityMax {
			return Params{}, fmt.Errorf("%w: quality %d must be between %d and %d", ErrInvalidParams, n, cfg.QualityMin, cfg.QualityMax)
		}
		p.Quality = n
	}

	if v, ok := raw["fit"]; ok {
		switch Fit(strings.ToLower(v)) {
		case FitCover, FitContain, FitFill, FitInside, FitOutside:
			p.Fit = Fit(strings.ToLower(v))
		default:
			return Params{}, fmt.Errorf("%w: unsupported fit %q", ErrInvalidParams, v)
		}
	}

	if v, ok := raw["gravity"]; ok {
		switch Gravity(strings.ToLower(v)) {
		case GravityCenter, GravityTop, GravityBottom, GravityLeft, GravityRight, GravityEntropy:
			p.Gravity = Gravity(strings.ToLower(v))
		default:
			return Params{}, fmt.Errorf("%w: unsupported gravity %q", ErrInvalidParams, v)
		}
	}

	if v, ok := raw["format"]; ok {
		format, err := canonicalFormat(v)
		if err != nil {
			return Params{}, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		if !formatAllowed(format, cfg.AllowedFormats) {
			return Params{}, fmt.Errorf("%w: format %q not allowed", ErrInvalidParams, format)
		}
		p.Format = format
	}

	// Defaults.
	if p.Quality == 0 {
		p.Quality = cfg.DefaultQuality
	}
	if p.Fit == "" {
		p.Fit = FitCover
	}
	if p.Gravity == "" {
		p.Gravity = GravityCenter
	}
	return p, nil
}

// canonicalFormat maps a raw format token to a canonical Format, accepting
// "jpg" as an alias for "jpeg".
func canonicalFormat(v string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "jpeg", "jpg":
		return FormatJpeg, nil
	case "png":
		return FormatPng, nil
	case "webp":
		return FormatWebp, nil
	case "avif":
		return FormatAvif, nil
	default:
		return "", fmt.Errorf("unsupported format %q", v)
	}
}

// formatAllowed reports whether the canonical format is listed in
// cfg.AllowedFormats (case-insensitive, canonical names).
func formatAllowed(format Format, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(a), string(format)) {
			return true
		}
	}
	return false
}

// CacheKey derives a stable, collision-resistant cache key for the transform
// result. It is canonical across processes: it includes every parameter that
// affects the rendered bytes plus the source identity (object ID, hash, size)
// and a version tag so key semantics can be invalidated deliberately.
func (p Params) CacheKey(objectID, objectHash string, objectSize int64) string {
	canonical := fmt.Sprintf("w=%d&h=%d&fit=%s&gravity=%s&format=%s&quality=%d|%s|%s|%d|v1",
		p.Width, p.Height, p.Fit, p.Gravity, p.Format, p.Quality, objectID, objectHash, objectSize)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}
