package database

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const FolderMimeType = "folder/folder"

type FilePool struct {
	ID            string         `gorm:"primaryKey;size:36" json:"id"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	AccountID     uuid.UUID      `json:"account_id"`
	StorageConfig datatypes.JSON `gorm:"type:jsonb" json:"storage_config"`
	BillingConfig datatypes.JSON `gorm:"type:jsonb" json:"billing_config"`
	PolicyConfig  datatypes.JSON `gorm:"type:jsonb" json:"policy_config"`
	IsHidden      bool           `json:"is_hidden"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"deleted_at"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type FileObject struct {
	ID             string         `gorm:"primaryKey;size:36" json:"id"`
	Size           int64          `json:"size"`
	MimeType       string         `json:"mime_type"`
	Hash           string         `json:"hash"`
	StorageKey     *string        `gorm:"size:64" json:"storage_key"`
	Meta           datatypes.JSON `gorm:"type:jsonb" json:"meta"`
	HasCompression bool           `json:"has_compression"`
	HasThumbnail   bool           `json:"has_thumbnail"`
	NeedsRehash    bool           `gorm:"index" json:"needs_rehash"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"deleted_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type CloudFile struct {
	ID               string           `gorm:"primaryKey;size:36" json:"id"`
	Name             string           `json:"name"`
	Description      *string          `json:"description"`
	AccountID        uuid.UUID        `json:"account_id"`
	PoolID           *string          `gorm:"size:36" json:"pool_id"`
	ObjectID         *string          `gorm:"size:36" json:"object_id"`
	ParentID         *string          `gorm:"size:36;index:idx_cloud_files_parent_deleted,priority:1;index:idx_cloud_files_parent_active,where:deleted_at IS NULL" json:"parent_id"`
	Indexed          bool             `json:"indexed"`
	IsFolder         bool             `json:"is_folder"`
	IsMarkedRecycle  bool             `json:"is_marked_recycle"`
	ExpiredAt        *time.Time       `json:"expired_at"`
	StorageID        *string          `gorm:"size:36" json:"storage_id"`
	StorageURL       *string          `gorm:"size:255" json:"storage_url"`
	StorageKey       *string          `gorm:"size:64" json:"storage_key"`
	FileMeta         datatypes.JSON   `gorm:"type:jsonb" json:"file_meta"`
	UserMeta         datatypes.JSON   `gorm:"type:jsonb" json:"user_meta"`
	Usage            *string          `json:"usage"`
	ApplicationType  *string          `json:"application_type"`
	DeletedAt        gorm.DeletedAt   `gorm:"index;index:idx_cloud_files_parent_deleted,priority:2" json:"deleted_at"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
	Object           *FileObject      `gorm:"foreignKey:ObjectID;references:ID" json:"object,omitempty"`
	Children         []CloudFile      `gorm:"foreignKey:ParentID;references:ID" json:"children,omitempty"`
	ChildrenCount    int              `gorm:"-" json:"children_count"`
	PermissionStatus PermissionStatus `gorm:"-" json:"permission_status"`
}

type PermissionStatus struct {
	Readable      bool    `json:"readable"`
	Writable      bool    `json:"writable"`
	Manageable    bool    `json:"manageable"`
	Visibility    string  `json:"visibility"`
	InheritedFrom *string `json:"inherited_from,omitempty"`
}

func (f *FileObject) LegacyMeta() datatypes.JSON {
	if f == nil {
		return nil
	}
	return f.Meta
}

func (f *FileObject) MarshalJSON() ([]byte, error) {
	if f == nil {
		return []byte("null"), nil
	}
	return json.Marshal(map[string]any{
		"id":              f.ID,
		"size":            f.Size,
		"meta":            f.LegacyMeta(),
		"mime_type":       f.MimeType,
		"hash":            f.Hash,
		"has_compression": f.HasCompression,
		"has_thumbnail":   f.HasThumbnail,
		"file_replicas":   []any{},
		"created_at":      f.CreatedAt,
		"updated_at":      f.UpdatedAt,
		"deleted_at":      nullableDeletedAt(f.DeletedAt),
	})
}

func (f *CloudFile) LegacyFileMeta() datatypes.JSON {
	if f == nil {
		return nil
	}
	if f.Object != nil {
		return f.Object.LegacyMeta()
	}
	if len(bytes.TrimSpace(f.FileMeta)) > 0 && string(bytes.TrimSpace(f.FileMeta)) != "null" {
		return f.FileMeta
	}
	return nil
}

func (f *CloudFile) LegacySensitiveMarks() []any {
	return []any{}
}

func (f *CloudFile) ResourceIdentifier() string {
	if f == nil || f.ID == "" {
		return ""
	}
	return "file:" + f.ID
}

func (f *CloudFile) MarshalJSON() ([]byte, error) {
	if f == nil {
		return []byte("null"), nil
	}
	objectID := f.ObjectID
	var object any
	if f.Object != nil {
		object = f.Object
	}
	return json.Marshal(map[string]any{
		"id":                  f.ID,
		"name":                f.Name,
		"description":         f.Description,
		"user_meta":           f.UserMeta,
		"sensitive_marks":     f.LegacySensitiveMarks(),
		"file_meta":           f.LegacyFileMeta(),
		"mime_type":           f.ResponseMimeType(),
		"hash":                f.legacyHash(),
		"expired_at":          f.ExpiredAt,
		"size":                f.legacySize(),
		"has_compression":     f.legacyHasCompression(),
		"has_thumbnail":       f.legacyHasThumbnail(),
		"object_id":           objectID,
		"object":              object,
		"parent_id":           f.ParentID,
		"indexed":             f.Indexed,
		"is_folder":           f.IsFolder,
		"usage":               f.Usage,
		"application_type":    f.ApplicationType,
		"is_marked_recycle":   f.IsMarkedRecycle,
		"storage_id":          f.StorageID,
		"storage_url":         f.StorageURL,
		"account_id":          f.AccountID,
		"resource_identifier": f.ResourceIdentifier(),
		"children_count":      f.ChildrenCount,
		"permission_status":   f.PermissionStatus,
		"created_at":          f.CreatedAt,
		"updated_at":          f.UpdatedAt,
		"deleted_at":          nullableDeletedAt(f.DeletedAt),
	})
}

func (f *CloudFile) ResponseMimeType() string {
	if f == nil {
		return ""
	}
	if f.IsFolder {
		return FolderMimeType
	}
	if f.Object != nil && f.Object.MimeType != "" {
		return f.Object.MimeType
	}
	return ""
}

func (f *CloudFile) legacyMimeType() string {
	return f.ResponseMimeType()
}

func (f *CloudFile) legacyHash() string {
	if f == nil {
		return ""
	}
	if f.Object != nil && f.Object.Hash != "" {
		return f.Object.Hash
	}
	return ""
}

func (f *CloudFile) legacySize() int64 {
	if f == nil {
		return 0
	}
	if f.Object != nil && f.Object.Size != 0 {
		return f.Object.Size
	}
	return 0
}

func (f *CloudFile) legacyHasCompression() bool {
	if f == nil {
		return false
	}
	if f.Object != nil {
		return f.Object.HasCompression
	}
	return false
}

func (f *CloudFile) legacyHasThumbnail() bool {
	if f == nil {
		return false
	}
	if f.Object != nil {
		return f.Object.HasThumbnail
	}
	return false
}

func nullableDeletedAt(v gorm.DeletedAt) any {
	if !v.Valid {
		return nil
	}
	return v.Time
}

type FilePermission struct {
	ID          string         `gorm:"primaryKey;size:36" json:"id"`
	FileID      string         `gorm:"size:36;index;index:idx_file_permissions_file_permission_deleted,priority:1;index:idx_file_permissions_active_lookup,priority:1,where:deleted_at IS NULL" json:"file_id"`
	SubjectType string         `json:"subject_type"`
	SubjectID   string         `gorm:"size:36" json:"subject_id"`
	Permission  string         `gorm:"index:idx_file_permissions_file_permission_deleted,priority:2;index:idx_file_permissions_active_lookup,priority:2,where:deleted_at IS NULL" json:"permission"`
	DeletedAt   gorm.DeletedAt `gorm:"index;index:idx_file_permissions_file_permission_deleted,priority:3" json:"deleted_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type FileLock struct {
	ID        string         `gorm:"primaryKey;size:36" json:"id"`
	FileID    string         `gorm:"size:36;index" json:"file_id"`
	LockToken string         `gorm:"size:255" json:"lock_token"`
	Protocol  string         `gorm:"size:16;index" json:"protocol"`
	AccountID *uuid.UUID     `json:"account_id"`
	Timeout   int            `json:"timeout"`
	ExpiresAt time.Time      `gorm:"index" json:"expires_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type WebDAVToken struct {
	ID          string         `gorm:"primaryKey;size:36" json:"id"`
	AccountID   uuid.UUID      `gorm:"index" json:"account_id"`
	TokenHash   string         `gorm:"size:60;uniqueIndex" json:"-"`
	Label       string         `gorm:"size:128" json:"label"`
	LastUsedAt  *time.Time     `json:"last_used_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

func (l *FileLock) BeforeCreate(tx *gorm.DB) error {
	if l.ID == "" {
		l.ID = NewID()
	}
	return nil
}

func (t *WebDAVToken) BeforeCreate(tx *gorm.DB) error {
	if t.ID == "" {
		t.ID = NewID()
	}
	return nil
}

func (p *FilePermission) BeforeCreate(tx *gorm.DB) error {
	if p.ID == "" {
		p.ID = NewID()
	}
	return nil
}

type PoolPermission struct {
	ID          string         `gorm:"primaryKey;size:36" json:"id"`
	PoolID      string         `gorm:"size:36;index" json:"pool_id"`
	SubjectType string         `json:"subject_type"`
	SubjectID   string         `gorm:"size:36" json:"subject_id"`
	Permission  string         `json:"permission"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type PersistentTask struct {
	ID              string         `gorm:"primaryKey;size:36" json:"id"`
	TaskID          string         `gorm:"size:36;index" json:"task_id"`
	Name            string         `json:"name"`
	Type            string         `json:"type"`
	Status          string         `json:"status"`
	AccountID       uuid.UUID      `json:"account_id"`
	Progress        float64        `json:"progress"`
	ChunkSize       int64          `json:"chunk_size"`
	ChunksCount     int            `json:"chunks_count"`
	ChunksUploaded  int            `json:"chunks_uploaded"`
	UploadedChunks  datatypes.JSON `gorm:"type:jsonb" json:"uploaded_chunks"`
	Parameters      datatypes.JSON `gorm:"type:jsonb" json:"parameters"`
	Results         datatypes.JSON `gorm:"type:jsonb" json:"results"`
	Indexed         bool           `json:"indexed"`
	ErrorMessage    *string        `json:"error_message"`
	Priority        int            `json:"priority"`
	LastActivity    time.Time      `json:"last_activity"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	FileName        *string        `json:"file_name"`
	FileSize        *int64         `json:"file_size"`
	PoolID          *string        `gorm:"size:36" json:"pool_id"`
	ParentID        *string        `gorm:"size:36" json:"parent_id"`
	OverwriteID     *string        `gorm:"size:36" json:"overwrite_id"`
	FastMode        bool           `json:"fast_mode"`
	Description     *string        `json:"description"`
	Hash            *string        `json:"hash"`
	ExpiredAt       *time.Time     `json:"expired_at"`
	Usage           *string        `json:"usage"`
	ApplicationType *string        `json:"application_type"`
	StorageKey      *string        `gorm:"size:36" json:"storage_key"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}

// UploadChunk makes chunk completion idempotent without repeatedly rewriting a
// JSON array on the parent upload task.
type UploadChunk struct {
	TaskID     string    `gorm:"primaryKey;size:36" json:"task_id"`
	ChunkIndex int       `gorm:"primaryKey" json:"chunk_index"`
	CreatedAt  time.Time `json:"created_at"`
}

type QuotaRecord struct {
	ID          string         `gorm:"primaryKey;size:36" json:"id"`
	AccountID   uuid.UUID      `json:"account_id"`
	Description string         `json:"description"`
	Name        string         `json:"name"`
	Quota       int64          `json:"quota"`
	ExpiredAt   *time.Time     `json:"expired_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type StorageNode struct {
	ID         string         `gorm:"primaryKey;size:36" json:"id"`
	Name       string         `json:"name"`
	MachineID  string         `gorm:"size:64;uniqueIndex" json:"machine_id"`
	Endpoint   string         `json:"endpoint"`
	AuthToken  string         `gorm:"size:255" json:"-"`
	Status     string         `gorm:"size:16;index" json:"status"`
	LastSeenAt *time.Time     `json:"last_seen_at"`
	PoolID     *string        `gorm:"size:36" json:"pool_id"`
	AccountID  uuid.UUID      `json:"account_id"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"deleted_at"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

func (n *StorageNode) BeforeCreate(tx *gorm.DB) error {
	if n.ID == "" {
		n.ID = NewID()
	}
	return nil
}

type S3Token struct {
	ID          string         `gorm:"primaryKey;size:36" json:"id"`
	AccountID   uuid.UUID      `gorm:"index" json:"account_id"`
	AccessKey   string         `gorm:"size:128;uniqueIndex" json:"-"`
	SecretKey   string         `gorm:"size:128" json:"-"`
	Label       string         `gorm:"size:128" json:"label"`
	PoolID      *string        `gorm:"size:36" json:"pool_id"`
	LastUsedAt  *time.Time     `json:"last_used_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

func (t *S3Token) BeforeCreate(tx *gorm.DB) error {
	if t.ID == "" {
		t.ID = NewID()
	}
	return nil
}
