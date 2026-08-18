package database

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPersistentTaskStoresLongUploadID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:persistent-task-long-upload-id?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(&PersistentTask{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	var uploadColumn gorm.ColumnType
	for _, column := range mustColumnTypes(t, db, &PersistentTask{}) {
		if column.Name() == "upload_id" {
			uploadColumn = column
			break
		}
	}
	if uploadColumn == nil {
		t.Fatal("upload_id column was not created")
	}
	if got := strings.ToUpper(uploadColumn.DatabaseTypeName()); got != "TEXT" {
		t.Fatalf("upload_id database type = %q, want TEXT", got)
	}

	uploadID := strings.Repeat("opaque-upload-id-", 40)
	task := &PersistentTask{
		ID:        NewID(),
		TaskID:    NewID(),
		Name:      "large.zip",
		Type:      "file.upload",
		Status:    "pending",
		AccountID: uuid.New(),
		UploadID:  &uploadID,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	var loaded PersistentTask
	if err := db.First(&loaded, "task_id = ?", task.TaskID).Error; err != nil {
		t.Fatalf("load task: %v", err)
	}
	if loaded.UploadID == nil || *loaded.UploadID != uploadID {
		t.Fatalf("loaded upload_id = %v, want %q", loaded.UploadID, uploadID)
	}
}

func mustColumnTypes(t *testing.T, db *gorm.DB, value any) []gorm.ColumnType {
	t.Helper()
	columns, err := db.Migrator().ColumnTypes(value)
	if err != nil {
		t.Fatalf("column types: %v", err)
	}
	return columns
}

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
