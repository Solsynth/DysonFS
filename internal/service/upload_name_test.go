package service

import "testing"

func TestDefaultUploadFileName(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        string
	}{
		{"jpeg", "image/jpeg", "upload.jpg"},
		{"png with parameters", "image/png; charset=binary", "upload.png"},
		{"heic", "image/heic", "upload.heic"},
		{"mp4", "video/mp4", "upload.mp4"},
		{"quicktime", "video/quicktime", "upload.mov"},
		{"mp3", "audio/mpeg", "upload.mp3"},
		{"pdf", "application/pdf", "upload.pdf"},
		{"json", "application/json", "upload.json"},
		{"zip", "application/zip", "upload.zip"},
		{"text", "text/plain", "upload.txt"},
		{"case insensitive", "IMAGE/JPEG", "upload.jpg"},
		{"unknown image family", "image/tiff", "upload.img"},
		{"unknown video family", "video/x-matroska", "upload.video"},
		{"unknown audio family", "audio/x-aiff", "upload.audio"},
		{"unknown type", "application/x-foo", "upload.bin"},
		{"empty", "", "upload.bin"},
		{"whitespace", "  ", "upload.bin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DefaultUploadFileName(tt.contentType); got != tt.want {
				t.Fatalf("DefaultUploadFileName(%q) = %q, want %q", tt.contentType, got, tt.want)
			}
		})
	}
}
