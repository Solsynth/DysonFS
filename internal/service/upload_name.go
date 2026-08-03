package service

import "strings"

// contentTypeExtensions maps common media types to the file extension used
// when a client uploads without a file name.
var contentTypeExtensions = map[string]string{
	"image/jpeg":                   "jpg",
	"image/png":                    "png",
	"image/gif":                    "gif",
	"image/webp":                   "webp",
	"image/heic":                   "heic",
	"image/heif":                   "heif",
	"image/avif":                   "avif",
	"image/svg+xml":                "svg",
	"video/mp4":                    "mp4",
	"video/quicktime":              "mov",
	"video/webm":                   "webm",
	"audio/mpeg":                   "mp3",
	"audio/mp4":                    "m4a",
	"audio/x-m4a":                  "m4a",
	"audio/wav":                    "wav",
	"audio/x-wav":                  "wav",
	"audio/ogg":                    "ogg",
	"application/ogg":              "ogg",
	"audio/flac":                   "flac",
	"audio/x-flac":                 "flac",
	"application/pdf":              "pdf",
	"application/json":             "json",
	"application/zip":              "zip",
	"application/gzip":             "gz",
	"application/x-tar":            "tar",
	"application/x-7z-compressed":  "7z",
	"application/x-rar-compressed": "rar",
	"application/epub+zip":         "epub",
	"text/plain":                   "txt",
	"text/html":                    "html",
	"text/markdown":                "md",
}

// DefaultUploadFileName returns a fallback name ("upload.<ext>") for uploads
// that arrive without a file name, so they are never rejected or stored
// unnamed. The extension is derived from the content type (an image/jpeg
// upload becomes "upload.jpg"); unknown types fall back to "upload.bin".
func DefaultUploadFileName(contentType string) string {
	normalized := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if ext, ok := contentTypeExtensions[normalized]; ok {
		return "upload." + ext
	}
	if family := strings.SplitN(normalized, "/", 2); len(family) == 2 {
		switch family[0] {
		case "image":
			return "upload.img"
		case "video":
			return "upload.video"
		case "audio":
			return "upload.audio"
		case "text":
			return "upload.txt"
		}
	}
	return "upload.bin"
}
