package database

import (
	"encoding/json"
	"testing"
)

func TestCloudFileMarshalJSONUsesFolderMimeType(t *testing.T) {
	file := &CloudFile{
		ID:       NewID(),
		Name:     "docs",
		IsFolder: true,
		Object: &FileObject{
			ID:       NewID(),
			MimeType: "application/octet-stream",
		},
	}

	body, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}

	if got := payload["mime_type"]; got != FolderMimeType {
		t.Fatalf("mime_type = %v, want %q", got, FolderMimeType)
	}

	object, ok := payload["object"].(map[string]any)
	if !ok {
		t.Fatalf("object = %T, want map", payload["object"])
	}
	if got := object["mime_type"]; got != "application/octet-stream" {
		t.Fatalf("object.mime_type = %v, want %q", got, "application/octet-stream")
	}
}
