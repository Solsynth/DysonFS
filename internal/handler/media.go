package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/media"
	"src.solsynth.dev/sosys/filesystem/internal/service"

	"github.com/gin-gonic/gin"
)

// mediaTransformParams are the query keys that switch the content routes from a
// storage redirect to the on-the-fly transform proxy.
var mediaTransformParams = []string{"width", "height", "fit", "gravity", "format", "quality", "preset"}

func hasTransformParams(c *gin.Context) bool {
	for _, k := range mediaTransformParams {
		if _, ok := c.GetQuery(k); ok {
			return true
		}
	}
	return false
}

func serveMediaTransform(c *gin.Context, cfg *config.Config, files *service.FileService, file *database.CloudFile, download bool) {
	med := files.Media()
	if med == nil || !med.Enabled() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "media transform is disabled"})
		return
	}
	if file.Object == nil || !strings.HasPrefix(file.Object.MimeType, "image/") {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "unsupported media type"})
		return
	}
	p, err := media.ParseParams(cfg.Media, c.Request.URL.Query())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := med.Render(c.Request.Context(), files, file, p)
	if err != nil {
		switch {
		case errors.Is(err, media.ErrSourceUnavailable):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case errors.Is(err, media.ErrAnimatedUnsupported):
			c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": err.Error()})
		case errors.Is(err, media.ErrSourceTooLarge), errors.Is(err, media.ErrOutputTooLarge):
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": err.Error()})
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			c.Status(http.StatusServiceUnavailable)
		default:
			logging.Log.Error().Err(err).Str("fileId", file.ID).Msg("media transform failed")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "transform failed"})
		}
		return
	}
	if res.Redirect {
		// s3 cache tier: the derivative lives in the cache bucket; redirect the
		// client to a presigned URL instead of proxying the bytes.
		u, err := med.SignedCacheURL(c.Request.Context(), res.ETag, transformedName(file, res.Ext), download, 15*time.Minute)
		if err != nil || u == "" {
			logging.Log.Error().Err(err).Str("fileId", file.ID).Msg("media cache signed url failed")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "transform failed"})
			return
		}
		c.Header("Cache-Control", "no-store") // the redirect embeds an expiring signature
		c.Header("ETag", `"`+res.ETag+`"`)
		c.Redirect(http.StatusTemporaryRedirect, u)
		return
	}
	serveTransformedBytes(c, file, res, download, cfg.Media.CacheControlMaxAge)
}

// transformedName appends the derivative extension to the file name when the
// stored name does not already carry it (e.g. a webp source requested as png).
func transformedName(file *database.CloudFile, ext string) string {
	name := file.Name
	if !strings.Contains(strings.ToLower(filepath.Ext(name)), "."+ext) {
		name += "." + ext
	}
	return name
}

// serveTransformedBytes streams the derivative with full HTTP caching headers.
// http.ServeContent handles Range/If-Range and, with the pre-set ETag,
// If-None-Match -> 304.
func serveTransformedBytes(c *gin.Context, file *database.CloudFile, res *media.Result, download bool, maxAge time.Duration) {
	name := transformedName(file, res.Ext)
	disposition := "inline"
	if download {
		disposition = "attachment"
	}
	c.Header("Content-Type", res.ContentType)
	c.Header("Content-Disposition", disposition+`; filename="`+name+`"`)
	c.Header("Cache-Control", fmt.Sprintf("public, max-age=%d", int(maxAge.Seconds())))
	c.Header("ETag", `"`+res.ETag+`"`)
	http.ServeContent(c.Writer, c.Request, name, res.ModTime, bytes.NewReader(res.Bytes))
}
