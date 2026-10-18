// Package analysis implements source-file analysis: image dimensions,
// EXIF, blurhash, and the ffprobe media blob. It also owns the background
// reanalysis queue and the metadata writers that persist what it derives.
//
// The package is the engine behind app.solian.files.analysis.*. Those modules
// decide which file kinds are analyzed; this package does not gate anything by
// itself.
package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"strings"

	blurhash "github.com/bbrks/go-blurhash"
	"github.com/gabriel-vasile/mimetype"
	"github.com/davidbyttow/govips/v2/vips"
	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/tiff"
	"gorm.io/datatypes"

	"src.solsynth.dev/sosys/filesystem/internal/database"
)

// ImageAnalysis is what the image analyzer derives.
type ImageAnalysis struct {
	Width    int
	Height   int
	Blurhash string
	Exif     map[string]any
}

// SourceAnalysis is the union of everything the analyzers derive for one file.
type SourceAnalysis struct {
	Width  int
	Height int
	Image  *ImageAnalysis
	Media  map[string]any
}

func mimetypeDetect(path string) (string, error) {
	detected, err := mimetype.DetectFile(path)
	if err != nil {
		return "", err
	}
	return detected.String(), nil
}

// DetectSourceMime resolves the effective MIME type of a staged file.
func DetectSourceMime(path, contentType string) string {
	resolved := strings.TrimSpace(contentType)
	if resolved != "" && !strings.EqualFold(resolved, "application/octet-stream") {
		return resolved
	}
	detected, err := mimetypeDetect(path)
	if err == nil && detected != "" {
		return detected
	}
	if resolved != "" {
		return resolved
	}
	return "application/octet-stream"
}

// AnalyzeImage derives dimensions, blurhash and EXIF for an image file.
func AnalyzeImage(path string) (*ImageAnalysis, error) {
	img, err := vips.NewImageFromFile(path)
	if err != nil {
		return nil, err
	}
	defer img.Close()

	if err := img.AutoRotate(); err != nil {
		return nil, err
	}
	if err := img.RemoveMetadata(); err != nil {
		return nil, err
	}

	blur, err := analyzeBlurhashImage(img)
	if err != nil {
		return nil, err
	}

	exifMeta, err := extractExif(path)
	if err != nil {
		exifMeta = nil
	}

	return &ImageAnalysis{Width: img.Width(), Height: img.Height(), Blurhash: blur, Exif: exifMeta}, nil
}

// AnalyzeSourceFile inspects a staged file according to its MIME type.
func AnalyzeSourceFile(ctx context.Context, path, mimeType string) (*SourceAnalysis, error) {
	analysis := &SourceAnalysis{}
	mimeType = DetectSourceMime(path, mimeType)
	if strings.HasPrefix(mimeType, "image/") {
		img, err := AnalyzeImage(path)
		if err != nil {
			return nil, err
		}
		analysis.Width = img.Width
		analysis.Height = img.Height
		analysis.Image = img
		return analysis, nil
	}
	if strings.HasPrefix(mimeType, "video/") || strings.HasPrefix(mimeType, "audio/") {
		media, err := probeMedia(ctx, path)
		if err != nil {
			return nil, err
		}
		analysis.Width, analysis.Height = MediaDimensions(media)
		analysis.Media = media
		return analysis, nil
	}
	return analysis, nil
}

// MediaDimensions extracts the video stream dimensions from an ffprobe blob.
func MediaDimensions(media map[string]any) (width, height int) {
	streams, ok := media["streams"].([]any)
	if !ok {
		return 0, 0
	}
	for _, raw := range streams {
		stream, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		codecType, _ := stream["codec_type"].(string)
		if codecType != "video" {
			continue
		}
		width = intFromAny(stream["width"])
		height = intFromAny(stream["height"])
		if width > 0 || height > 0 {
			return width, height
		}
	}
	return 0, 0
}

func intFromAny(v any) int {
	switch value := v.(type) {
	case float64:
		return int(value)
	case float32:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	case int32:
		return int(value)
	case json.Number:
		n, err := value.Int64()
		if err == nil {
			return int(n)
		}
	}
	return 0
}

func probeMedia(ctx context.Context, path string) (map[string]any, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var payload map[string]any
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func extractExif(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	x, err := exif.Decode(f)
	if err != nil {
		return nil, err
	}
	meta := map[string]any{}
	_ = x.Walk(exifWalker{meta: meta})
	if len(meta) == 0 {
		return nil, nil
	}
	return meta, nil
}

type exifWalker struct{ meta map[string]any }

func (w exifWalker) Walk(name exif.FieldName, tag *tiff.Tag) error {
	if tag == nil {
		return nil
	}
	w.meta[string(name)] = tag.String()
	return nil
}

func analyzeBlurhashImage(img *vips.ImageRef) (string, error) {
	if img == nil {
		return "", fmt.Errorf("image is required")
	}
	// Downscale before PNG export: blurhash only needs low-frequency color
	// data, and a 40MP export for a 4x3 DCT is thousands of times more pixels
	// than necessary.
	thumb, err := img.Copy()
	if err != nil {
		return "", err
	}
	defer thumb.Close()
	if thumb.Width() > 64 {
		if err := thumb.Resize(64.0/float64(thumb.Width()), vips.KernelLanczos3); err != nil {
			return "", err
		}
	}
	buf, _, err := thumb.ExportPng(&vips.PngExportParams{StripMetadata: true})
	if err != nil {
		return "", err
	}
	decoded, err := png.Decode(bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	return blurhash.Encode(4, 3, decoded)
}

func mediaAspectRatio(media map[string]any, width, height int) string {
	streams, ok := media["streams"].([]any)
	if ok {
		for _, raw := range streams {
			stream, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			codecType, _ := stream["codec_type"].(string)
			if codecType != "video" {
				continue
			}
			if ratio, _ := stream["display_aspect_ratio"].(string); strings.TrimSpace(ratio) != "" && ratio != "0:1" {
				return strings.TrimSpace(ratio)
			}
			if ratio, _ := stream["sample_aspect_ratio"].(string); strings.TrimSpace(ratio) != "" && ratio != "0:1" {
				return strings.TrimSpace(ratio)
			}
		}
	}
	if width > 0 && height > 0 {
		divisor := gcd(width, height)
		return fmt.Sprintf("%d:%d", width/divisor, height/divisor)
	}
	return ""
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

// MergeJSONMeta merges updates into a JSON object column.
func MergeJSONMeta(raw datatypes.JSON, updates map[string]any) (datatypes.JSON, error) {
	meta := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 && string(bytes.TrimSpace(raw)) != "null" {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, err
		}
	}
	for k, v := range updates {
		meta[k] = v
	}
	merged, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(merged), nil
}

// SourceAnalysisUpdates turns an analysis into the metadata column updates.
func SourceAnalysisUpdates(analysis *SourceAnalysis) map[string]any {
	if analysis == nil {
		return nil
	}
	updates := map[string]any{}
	if analysis.Width > 0 {
		updates["width"] = analysis.Width
	}
	if analysis.Height > 0 {
		updates["height"] = analysis.Height
	}
	if analysis.Image != nil {
		updates["width"] = analysis.Image.Width
		updates["height"] = analysis.Image.Height
		updates["blurhash"] = analysis.Image.Blurhash
		updates["exif_version"] = 2
		if len(analysis.Image.Exif) > 0 {
			updates["exif"] = analysis.Image.Exif
		}
	}
	if len(analysis.Media) > 0 {
		updates["media"] = analysis.Media
		if ratio := mediaAspectRatio(analysis.Media, analysis.Width, analysis.Height); ratio != "" {
			updates["aspect_ratio"] = ratio
		}
	}
	return updates
}

// DerivedCompatibilityFlags reports the compatibility flags a source file of
// the given type will carry once processed: images gain a compression
// derivative and videos gain a thumbnail. The worker recomputes both flags
// from the actual derivative children when processing finishes.
func DerivedCompatibilityFlags(mimeType string) (hasThumbnail, hasCompression bool) {
	switch {
	case strings.HasPrefix(mimeType, "video/"):
		return true, false
	case strings.HasPrefix(mimeType, "image/"):
		return false, true
	default:
		return false, false
	}
}

// ClientOnlySourceMetaKeys are metadata fields only client-side analysis
// supplies. The server reports the same facts inside its media probe blob, so
// a completed server reanalysis drops these scalars instead of leaving
// possibly-wrong client values next to authoritative server metadata.
var ClientOnlySourceMetaKeys = []string{"duration_ms", "video_codec", "audio_codec", "container", "sample_rate", "channels"}

// NeedsServerReanalysis reports whether the server derives source metadata for
// a media type on its own.
func NeedsServerReanalysis(mimeType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
	return strings.HasPrefix(normalized, "image/") ||
		strings.HasPrefix(normalized, "video/") ||
		strings.HasPrefix(normalized, "audio/")
}

// StoredSourceExif reads the v2 EXIF map already persisted on an object's
// metadata. Returns nil when the object carries none.
func StoredSourceExif(raw datatypes.JSON) map[string]any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil
	}
	value, ok := meta["exif"].(map[string]any)
	if !ok {
		return nil
	}
	return value
}

// MergeSourceExif keeps every stored tag and lets the server's values win per
// tag, so a reanalysis never loses a field the client already extracted.
func MergeSourceExif(stored, extracted map[string]any) map[string]any {
	if len(stored) == 0 {
		return extracted
	}
	merged := make(map[string]any, len(stored)+len(extracted))
	for key, value := range stored {
		merged[key] = value
	}
	for key, value := range extracted {
		merged[key] = value
	}
	return merged
}

// objectMeta reads an object's metadata map.
func objectMeta(object *database.FileObject) map[string]any {
	if object == nil || len(bytes.TrimSpace(object.Meta)) == 0 {
		return nil
	}
	var meta map[string]any
	if err := json.Unmarshal(object.Meta, &meta); err != nil {
		return nil
	}
	return meta
}
