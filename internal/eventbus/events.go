package eventbus

import (
	"time"

	shared "src.solsynth.dev/sosys/go/pkg/eventbus"
)

type FileUploadedEvent struct {
	shared.Event
	FileID             string `json:"file_id"`
	TaskID             string `json:"task_id"`
	RemoteID           string `json:"remote_id"`
	StorageID          string `json:"storage_id,omitempty"`
	StorageKey         string `json:"storage_key,omitempty"`
	ContentType        string `json:"content_type,omitempty"`
	ProcessingFilePath string `json:"processing_file_path"`
	IsTempFile         bool   `json:"is_temp_file"`
}

type FileActionEvent struct {
	Action    string `json:"action"`
	FileID    string `json:"file_id"`
	AccountID string `json:"account_id"`
	Name      string `json:"name,omitempty"`
}

type FileMetadataSnapshot struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	MimeType        string         `json:"mime_type,omitempty"`
	Hash            string         `json:"hash,omitempty"`
	Size            int64          `json:"size"`
	FileMeta        map[string]any `json:"file_meta,omitempty"`
	HasCompression  bool           `json:"has_compression"`
	HasThumbnail    bool           `json:"has_thumbnail"`
	Usage           *string        `json:"usage,omitempty"`
	ApplicationType *string        `json:"application_type,omitempty"`
	Status          int            `json:"status"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type FileMetadataUpdatedEvent struct {
	shared.Event
	FileID    string               `json:"file_id"`
	TaskID    string               `json:"task_id,omitempty"`
	AccountID string               `json:"account_id"`
	Status    int                  `json:"status"`
	File      FileMetadataSnapshot `json:"file"`
}
