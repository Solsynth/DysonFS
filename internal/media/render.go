package media

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/davidbyttow/govips/v2/vips"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

// Render produces a transformed derivative for file with the given params.
// The source is the (already variant-selected) file; when the requested size
// exceeds the selected source and escalation is enabled, the parent (original)
// is loaded instead, at most once.
func (s *Service) Render(ctx context.Context, resolver SourceResolver, file *database.CloudFile, p Params) (*Result, error) {
	if s.sem != nil {
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Resolve the source, escalating to the parent original at most once.
	var img *vips.ImageRef
	var info storage.ObjectInfo
	attempt := 0
	for {
		backend, err := resolver.BackendForFile(file)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve backend: %v", ErrSourceUnavailable, err)
		}
		key := resolver.ResolveStorageKey(file)
		if key == "" {
			return nil, fmt.Errorf("%w: file storage key missing", ErrSourceUnavailable)
		}
		rc, objInfo, err := backend.Get(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSourceUnavailable, err)
		}
		buf, readErr := io.ReadAll(io.LimitReader(rc, s.cfg.MaxSourceBytes+1))
		rc.Close()
		if readErr != nil {
			return nil, fmt.Errorf("%w: read source: %v", ErrSourceUnavailable, readErr)
		}
		if objInfo.Size > s.cfg.MaxSourceBytes || int64(len(buf)) > s.cfg.MaxSourceBytes {
			return nil, ErrSourceTooLarge
		}
		info = objInfo

		loaded, err := vips.NewImageFromBuffer(buf)
		if err != nil {
			return nil, fmt.Errorf("%w: decode source: %v", ErrSourceUnavailable, err)
		}
		if img != nil {
			img.Close()
		}
		img = loaded
		if err := img.AutoRotate(); err != nil {
			return nil, fmt.Errorf("auto-rotate: %w", err)
		}
		if img.Pages() > 1 {
			return nil, ErrAnimatedUnsupported
		}
		if int64(img.Width())*int64(img.Height()) > s.cfg.MaxPixels {
			return nil, ErrSourceTooLarge
		}

		if s.cfg.EscalateToOriginal && attempt == 0 && file.ParentID != nil &&
			((p.Width > 0 && p.Width > img.Width()) || (p.Height > 0 && p.Height > img.Height())) {
			parent, err := resolver.GetFile(*file.ParentID)
			if err == nil && parent != nil && parent.Object != nil {
				file = parent
				attempt = 1
				continue
			}
			// Any error or nil parent: proceed with the current source.
		}
		break
	}
	defer img.Close()

	// Resolve the output format before any cache interaction so a cache hit
	// can be served with correct content metadata.
	format := resolveFormat(p.Format, img.OriginalFormat(), s.cfg.AllowedFormats)

	objectHash := ""
	objectSize := int64(0)
	if file.Object != nil {
		objectHash = file.Object.Hash
		objectSize = file.Object.Size
	}
	key := p.CacheKey(file.ID, objectHash, objectSize)

	if sc, ok := s.cache.(*s3Cache); ok {
		// s3 tier: never proxy the derivative bytes through DysonFS. A cache
		// hit is a Stat + presigned redirect; a miss renders in-process, puts
		// to the cache bucket, then redirects.
		if info, err := sc.backend.Stat(ctx, sc.prefix+key); err == nil && info.Size <= s.cfg.MaxOutputBytes {
			return &Result{ContentType: contentTypeFor(format), Ext: extFor(format), ETag: key, FromCache: true, Redirect: true}, nil
		}
		out, err := renderImage(img, p, format, s.cfg)
		if err != nil {
			return nil, err
		}
		if err := s.cache.Put(ctx, key, out, contentTypeFor(format)); err != nil {
			// A failed put leaves the cache cold; the redirect still serves once
			// the next request re-renders, so the error is not fatal.
			logging.Log.Error().Err(err).Str("key", key).Msg("media s3 cache put failed")
		}
		return &Result{ContentType: contentTypeFor(format), Ext: extFor(format), ETag: key, FromCache: false, Redirect: true}, nil
	}

	if cached, ok, err := s.cache.Get(ctx, key); err == nil && ok && len(cached) <= int(s.cfg.MaxOutputBytes) {
		return &Result{
			Bytes:       cached,
			ContentType: contentTypeFor(format),
			Ext:         extFor(format),
			ETag:        key,
			ModTime:     time.Time{},
			FromCache:   true,
		}, nil
	}

	out, err := renderImage(img, p, format, s.cfg)
	if err != nil {
		return nil, err
	}

	if err := s.cache.Put(ctx, key, out, contentTypeFor(format)); err != nil {
		// Cache failures never fail the response; the derivative is still valid.
		logging.Log.Error().Err(err).Str("key", key).Msg("media cache put failed")
	}

	return &Result{
		Bytes:       out,
		ContentType: contentTypeFor(format),
		Ext:         extFor(format),
		ETag:        key,
		ModTime:     info.ModTime,
		FromCache:   false,
	}, nil
}

// signedURLTTL bounds presigned cache URLs issued for the s3 tier. The client
// follows the redirect immediately, so the window only needs to cover the
// follow fetch (and any CDN replay within it).
const signedURLTTL = 15 * time.Minute

// SignedCacheURL presigns a URL for a cached derivative (s3 tier only). It
// returns "" when the cache is not s3-backed. filename/download are forwarded
// to the backend so the object is served inline or as an attachment.
func (s *Service) SignedCacheURL(ctx context.Context, key, filename string, download bool, ttl time.Duration) (string, error) {
	sc, ok := s.cache.(*s3Cache)
	if !ok {
		return "", nil
	}
	return sc.backend.SignedURL(ctx, sc.prefix+key, ttl, filename, download)
}

// renderImage applies the transform, strips metadata, exports, and enforces
// the output size limit. Callers must hold the service semaphore.
func renderImage(img *vips.ImageRef, p Params, format Format, cfg config.MediaConfig) ([]byte, error) {
	if err := applyTransform(img, p, cfg); err != nil {
		return nil, err
	}
	if err := img.RemoveMetadata(); err != nil {
		return nil, fmt.Errorf("remove metadata: %w", err)
	}
	out, err := exportImage(img, format, p.Quality)
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > cfg.MaxOutputBytes {
		return nil, ErrOutputTooLarge
	}
	return out, nil
}

// resolveFormat picks the output format: an explicit param wins; otherwise the
// source's own format is preserved, falling back to webp for unknown types or
// formats that are not allowed.
func resolveFormat(p Format, original vips.ImageType, allowed []string) Format {
	if p != "" {
		return p
	}
	switch original {
	case vips.ImageTypeJPEG:
		p = FormatJpeg
	case vips.ImageTypePNG:
		p = FormatPng
	case vips.ImageTypeWEBP:
		p = FormatWebp
	case vips.ImageTypeHEIF:
		p = FormatAvif
	default:
		p = FormatWebp
	}
	if !formatAllowed(p, allowed) {
		p = FormatWebp
	}
	return p
}

// applyTransform resizes img according to params. cover never enlarges; single
// dimension requests scale down unless fit is contain/outside; contain/outside/
// fill may enlarge.
func applyTransform(img *vips.ImageRef, p Params, cfg config.MediaConfig) error {
	srcW, srcH := img.Width(), img.Height()
	if srcW <= 0 || srcH <= 0 {
		return fmt.Errorf("transform: invalid source dimensions %dx%d", srcW, srcH)
	}
	w, h := p.Width, p.Height
	switch {
	case w > 0 && h > 0:
		switch p.Fit {
		case FitCover:
			if p.Gravity == GravityLeft || p.Gravity == GravityRight {
				// libvips has no left/right smartcrop interest; implement
				// cover with an edge crop (down-scale only, like SizeDown).
				return coverCropHorizontal(img, w, h, p.Gravity)
			}
			return img.ThumbnailWithSize(w, h, interestingFromGravity(p.Gravity), vips.SizeDown)
		case FitContain:
			scale := min(float64(w)/float64(srcW), float64(h)/float64(srcH))
			return img.Resize(scale, vips.KernelLanczos3)
		case FitInside:
			scale := min(float64(w)/float64(srcW), float64(h)/float64(srcH))
			scale = min(scale, 1)
			return img.Resize(scale, vips.KernelLanczos3)
		case FitOutside:
			scale := max(float64(w)/float64(srcW), float64(h)/float64(srcH))
			if err := img.Resize(scale, vips.KernelLanczos3); err != nil {
				return err
			}
			if int64(img.Width())*int64(img.Height()) > cfg.MaxPixels {
				return ErrSourceTooLarge
			}
			return nil
		case FitFill:
			return img.ResizeWithVScale(float64(w)/float64(srcW), float64(h)/float64(srcH), vips.KernelLanczos3)
		default:
			return fmt.Errorf("transform: unsupported fit %q", p.Fit)
		}
	case w > 0:
		scale := float64(w) / float64(srcW)
		if p.Fit != FitContain && p.Fit != FitOutside {
			scale = min(scale, 1)
		}
		return img.Resize(scale, vips.KernelLanczos3)
	case h > 0:
		scale := float64(h) / float64(srcH)
		if p.Fit != FitContain && p.Fit != FitOutside {
			scale = min(scale, 1)
		}
		return img.Resize(scale, vips.KernelLanczos3)
	default:
		// No resize: format/quality conversion only.
		return nil
	}
}

// interestingFromGravity maps the param gravity to a libvips crop interest.
func interestingFromGravity(g Gravity) vips.Interesting {
	switch g {
	case GravityTop:
		return vips.InterestingHigh
	case GravityBottom:
		return vips.InterestingLow
	case GravityEntropy:
		return vips.InterestingEntropy
	default:
		return vips.InterestingCentre
	}
}

// coverCropHorizontal implements cover with left/right gravity: scale to cover
// the box (down only, mirroring ThumbnailWithSize with SizeDown) and crop the
// excess from the requested side, keeping vertical centering.
func coverCropHorizontal(img *vips.ImageRef, w, h int, g Gravity) error {
	srcW, srcH := img.Width(), img.Height()
	scale := max(float64(w)/float64(srcW), float64(h)/float64(srcH))
	scale = min(scale, 1)
	if err := img.Resize(scale, vips.KernelLanczos3); err != nil {
		return err
	}
	newW, newH := img.Width(), img.Height()
	cropW, cropH := min(w, newW), min(h, newH)
	x := 0
	if g == GravityRight {
		x = newW - cropW
	}
	y := (newH - cropH) / 2
	return img.ExtractArea(x, y, cropW, cropH)
}

// exportImage renders the image to the requested format. JPEG output flattens
// alpha onto white (JPEG has no alpha channel). PNG ignores quality.
func exportImage(img *vips.ImageRef, format Format, quality int) ([]byte, error) {
	var out []byte
	var err error
	switch format {
	case FormatJpeg:
		if img.HasAlpha() {
			if err := img.Flatten(&vips.Color{R: 255, G: 255, B: 255}); err != nil {
				return nil, fmt.Errorf("flatten for jpeg: %w", err)
			}
		}
		out, _, err = img.ExportJpeg(&vips.JpegExportParams{Quality: quality, StripMetadata: true, OptimizeCoding: true, Interlace: true})
	case FormatPng:
		out, _, err = img.ExportPng(&vips.PngExportParams{Compression: 6, StripMetadata: true})
	case FormatWebp:
		out, _, err = img.ExportWebp(&vips.WebpExportParams{Quality: quality, StripMetadata: true})
	case FormatAvif:
		out, _, err = img.ExportAvif(&vips.AvifExportParams{Quality: quality, StripMetadata: true, Effort: 4})
	default:
		return nil, fmt.Errorf("export: unsupported format %q", format)
	}
	if err != nil {
		return nil, fmt.Errorf("export %s: %w", format, err)
	}
	return out, nil
}

func contentTypeFor(f Format) string {
	switch f {
	case FormatJpeg:
		return "image/jpeg"
	case FormatPng:
		return "image/png"
	case FormatWebp:
		return "image/webp"
	case FormatAvif:
		return "image/avif"
	default:
		return "application/octet-stream"
	}
}

func extFor(f Format) string {
	switch f {
	case FormatJpeg:
		return "jpg"
	case FormatPng:
		return "png"
	case FormatWebp:
		return "webp"
	case FormatAvif:
		return "avif"
	default:
		return "bin"
	}
}
