package grpcsvc

import (
	"encoding/json"
	"testing"

	"src.solsynth.dev/sosys/filesystem/internal/database"

	"gorm.io/datatypes"
)

func TestToProtoCloudFileUsesFolderMimeType(t *testing.T) {
	file := &database.CloudFile{
		ID:       database.NewID(),
		Name:     "docs",
		IsFolder: true,
		Object: &database.FileObject{
			ID:       database.NewID(),
			MimeType: "application/octet-stream",
		},
	}

	resp := toProtoCloudFile(file)

	if resp.GetMimeType() != database.FolderMimeType {
		t.Fatalf("mime_type = %q, want %q", resp.GetMimeType(), database.FolderMimeType)
	}
	if resp.GetContentType() != database.FolderMimeType {
		t.Fatalf("content_type = %q, want %q", resp.GetContentType(), database.FolderMimeType)
	}
	if resp.GetObject().GetMimeType() != "application/octet-stream" {
		t.Fatalf("object.mime_type = %q, want %q", resp.GetObject().GetMimeType(), "application/octet-stream")
	}
}

func TestToProtoCloudFileExposesVideoDimensionsFromObjectMeta(t *testing.T) {
	meta := datatypes.JSON([]byte(`{"width":1920,"height":1080,"aspect_ratio":"16:9"}`))
	file := &database.CloudFile{
		ID:   database.NewID(),
		Name: "clip.mp4",
		Object: &database.FileObject{
			ID:       database.NewID(),
			MimeType: "video/mp4",
			Meta:     meta,
		},
	}

	resp := toProtoCloudFile(file)

	if resp.GetWidth() != 1920 {
		t.Fatalf("width = %d, want 1920", resp.GetWidth())
	}
	if resp.GetHeight() != 1080 {
		t.Fatalf("height = %d, want 1080", resp.GetHeight())
	}

	var decoded map[string]any
	if err := json.Unmarshal(resp.GetObject().GetMeta(), &decoded); err != nil {
		t.Fatalf("unmarshal object meta: %v", err)
	}
	if got, _ := decoded["aspect_ratio"].(string); got != "16:9" {
		t.Fatalf("aspect_ratio = %q, want %q", got, "16:9")
	}
}
