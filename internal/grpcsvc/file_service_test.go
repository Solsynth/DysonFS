package grpcsvc

import (
	"testing"

	"src.solsynth.dev/sosys/filesystem/internal/database"
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
