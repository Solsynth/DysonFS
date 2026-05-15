package database

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type FilePool struct {
	ID            string         `gorm:"primaryKey;size:26" json:"id"`
	Name          string         `json:"name"`
	AccountID     uuid.UUID      `json:"account_id"`
	StorageConfig datatypes.JSON `gorm:"type:jsonb" json:"storage_config"`
	BillingConfig datatypes.JSON `gorm:"type:jsonb" json:"billing_config"`
	PolicyConfig  datatypes.JSON `gorm:"type:jsonb" json:"policy_config"`
	IsHidden      bool           `json:"is_hidden"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type FileObject struct {
	ID             string         `gorm:"primaryKey;size:26" json:"id"`
	Size           int64          `json:"size"`
	MimeType       string         `json:"mime_type"`
	Hash           string         `json:"hash"`
	StorageKey     *string        `json:"storage_key"`
	Meta           datatypes.JSON `gorm:"type:jsonb" json:"meta"`
	HasCompression bool           `json:"has_compression"`
	HasThumbnail   bool           `json:"has_thumbnail"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type CloudFile struct {
	ID              string         `gorm:"primaryKey;size:26" json:"id"`
	Name            string         `json:"name"`
	AccountID       uuid.UUID      `json:"account_id"`
	ObjectID        *string        `gorm:"size:26" json:"object_id"`
	ParentID        *string        `gorm:"size:26" json:"parent_id"`
	Indexed         bool           `json:"indexed"`
	IsFolder        bool           `json:"is_folder"`
	IsMarkedRecycle bool           `json:"is_marked_recycle"`
	ExpiredAt       *time.Time     `json:"expired_at"`
	UploadedAt      *time.Time     `json:"uploaded_at"`
	StorageID       *string        `json:"storage_id"`
	StorageURL      *string        `json:"storage_url"`
	StorageKey      *string        `json:"storage_key"`
	FileMeta        datatypes.JSON `gorm:"type:jsonb" json:"file_meta"`
	UserMeta        datatypes.JSON `gorm:"type:jsonb" json:"user_meta"`
	Usage           *string        `json:"usage"`
	ApplicationType *string        `json:"application_type"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"deleted_at"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	Object          *FileObject    `gorm:"foreignKey:ObjectID;references:ID" json:"object,omitempty"`
	Children        []CloudFile    `gorm:"foreignKey:ParentID;references:ID" json:"children,omitempty"`
}

type FileReplica struct {
	ID        string    `gorm:"primaryKey;size:26" json:"id"`
	ObjectID  string    `gorm:"size:26;index" json:"object_id"`
	PoolID    *string   `gorm:"size:26" json:"pool_id"`
	StorageID *string   `json:"storage_id"`
	Status    string    `json:"status"`
	IsPrimary bool      `json:"is_primary"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type FilePermission struct {
	ID          string    `gorm:"primaryKey;size:26" json:"id"`
	FileID      string    `gorm:"size:26;index" json:"file_id"`
	SubjectType string    `json:"subject_type"`
	SubjectID   string    `json:"subject_id"`
	Permission  string    `json:"permission"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type PersistentTask struct {
	ID              string         `gorm:"primaryKey;size:26" json:"id"`
	TaskID          string         `gorm:"size:26;index" json:"task_id"`
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
	ErrorMessage    *string        `json:"error_message"`
	Priority        int            `json:"priority"`
	LastActivity    time.Time      `json:"last_activity"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	FileName        *string        `json:"file_name"`
	FileSize        *int64         `json:"file_size"`
	PoolID          *string        `gorm:"size:26" json:"pool_id"`
	ParentID        *string        `gorm:"size:26" json:"parent_id"`
	ApplicationType *string        `json:"application_type"`
	StorageKey      *string        `json:"storage_key"`
}

type QuotaRecord struct {
	ID        string     `gorm:"primaryKey;size:26" json:"id"`
	AccountID uuid.UUID  `json:"account_id"`
	Name      string     `json:"name"`
	Quota     int64      `json:"quota"`
	ExpiredAt *time.Time `json:"expired_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}
