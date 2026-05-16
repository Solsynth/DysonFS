package migratelegacy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/database"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type Options struct {
	DryRun          bool
	BatchSize       int
	SkipDerived     bool
	ContinueOnError bool

	SubjectTypeMap   map[int]string
	PermissionMap    map[int]string
	ReplicaStatusMap map[int]string
}

type Migrator struct {
	src *gorm.DB
	dst *gorm.DB
	op  Options
}

type Summary struct {
	Pools          int64
	QuotaRecords   int64
	FileObjects    int64
	Files          int64
	FilePerms      int64
	FileReplicas   int64
	DerivedFiles   int64
	DerivedObjects int64
	Skipped        int64
	Failed         int64
}

func OpenTargetAndSource(targetDSN, sourceDSN string, op Options) (*Migrator, error) {
	if strings.TrimSpace(targetDSN) == "" {
		return nil, fmt.Errorf("target dsn is required")
	}
	if strings.TrimSpace(sourceDSN) == "" {
		return nil, fmt.Errorf("legacy dsn is required")
	}
	src, err := gorm.Open(postgres.Open(sourceDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)})
	if err != nil {
		return nil, err
	}
	dst, err := gorm.Open(postgres.Open(targetDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)})
	if err != nil {
		return nil, err
	}
	if op.BatchSize <= 0 {
		op.BatchSize = 500
	}
	if op.SubjectTypeMap == nil {
		op.SubjectTypeMap = defaultSubjectTypeMap()
	}
	if op.PermissionMap == nil {
		op.PermissionMap = defaultPermissionMap()
	}
	if op.ReplicaStatusMap == nil {
		op.ReplicaStatusMap = defaultReplicaStatusMap()
	}
	return &Migrator{src: src, dst: dst, op: op}, nil
}

func (m *Migrator) Run(ctx context.Context) (Summary, error) {
	_ = ctx
	var summary Summary
	if err := m.dst.AutoMigrate(
		&database.FilePool{}, &database.FileObject{}, &database.CloudFile{}, &database.FileReplica{}, &database.FilePermission{}, &database.PoolPermission{}, &database.PersistentTask{}, &database.QuotaRecord{},
	); err != nil {
		return summary, err
	}
	if err := m.migratePools(&summary); err != nil {
		return summary, err
	}
	if err := m.migrateQuotaRecords(&summary); err != nil {
		return summary, err
	}
	if err := m.migrateFileObjects(&summary); err != nil {
		return summary, err
	}
	if err := m.migrateFiles(&summary); err != nil {
		return summary, err
	}
	if err := m.migrateFilePermissions(&summary); err != nil {
		return summary, err
	}
	if err := m.migrateFileReplicas(&summary); err != nil {
		return summary, err
	}
	if !m.op.SkipDerived {
		if err := m.migrateDerived(&summary); err != nil {
			return summary, err
		}
	}
	return summary, nil
}

type legacyPool struct {
	ID            string         `gorm:"column:id;primaryKey"`
	Name          string         `gorm:"column:name"`
	StorageConfig datatypes.JSON `gorm:"column:storage_config"`
	BillingConfig datatypes.JSON `gorm:"column:billing_config"`
	PolicyConfig  datatypes.JSON `gorm:"column:policy_config"`
	AccountID     *string        `gorm:"column:account_id"`
	CreatedAt     time.Time      `gorm:"column:created_at"`
	UpdatedAt     time.Time      `gorm:"column:updated_at"`
	DeletedAt     *time.Time     `gorm:"column:deleted_at"`
	Description   string         `gorm:"column:description"`
	IsHidden      bool           `gorm:"column:is_hidden"`
}

func (legacyPool) TableName() string { return "pools" }

type legacyQuotaRecord struct {
	ID          string     `gorm:"column:id;primaryKey"`
	AccountID   string     `gorm:"column:account_id"`
	Name        string     `gorm:"column:name"`
	Description string     `gorm:"column:description"`
	Quota       int64      `gorm:"column:quota"`
	ExpiredAt   *time.Time `gorm:"column:expired_at"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at"`
	DeletedAt   *time.Time `gorm:"column:deleted_at"`
}

func (legacyQuotaRecord) TableName() string { return "quota_records" }

type legacyFileObject struct {
	ID             string         `gorm:"column:id;primaryKey"`
	Size           int64          `gorm:"column:size"`
	Meta           datatypes.JSON `gorm:"column:meta"`
	MimeType       *string        `gorm:"column:mime_type"`
	Hash           *string        `gorm:"column:hash"`
	HasCompression bool           `gorm:"column:has_compression"`
	HasThumbnail   bool           `gorm:"column:has_thumbnail"`
	CreatedAt      time.Time      `gorm:"column:created_at"`
	UpdatedAt      time.Time      `gorm:"column:updated_at"`
	DeletedAt      *time.Time     `gorm:"column:deleted_at"`
}

func (legacyFileObject) TableName() string { return "file_objects" }

type legacyFile struct {
	ID              string         `gorm:"column:id;primaryKey"`
	Name            string         `gorm:"column:name"`
	Description     *string        `gorm:"column:description"`
	UserMeta        datatypes.JSON `gorm:"column:user_meta"`
	SensitiveMarks  datatypes.JSON `gorm:"column:sensitive_marks"`
	UploadedAt      *time.Time     `gorm:"column:uploaded_at"`
	IsMarkedRecycle bool           `gorm:"column:is_marked_recycle"`
	StorageID       *string        `gorm:"column:storage_id"`
	StorageURL      *string        `gorm:"column:storage_url"`
	AccountID       string         `gorm:"column:account_id"`
	CreatedAt       time.Time      `gorm:"column:created_at"`
	UpdatedAt       time.Time      `gorm:"column:updated_at"`
	DeletedAt       *time.Time     `gorm:"column:deleted_at"`
	ExpiredAt       *time.Time     `gorm:"column:expired_at"`
	BundleID        *string        `gorm:"column:bundle_id"`
	ObjectID        *string        `gorm:"column:object_id"`
	FileMeta        datatypes.JSON `gorm:"column:file_meta"`
	HasCompression  bool           `gorm:"column:has_compression"`
	HasThumbnail    bool           `gorm:"column:has_thumbnail"`
	Hash            *string        `gorm:"column:hash"`
	MimeType        *string        `gorm:"column:mime_type"`
	Size            int64          `gorm:"column:size"`
	Indexed         bool           `gorm:"column:indexed"`
	IsFolder        bool           `gorm:"column:is_folder"`
	ParentID        *string        `gorm:"column:parent_id"`
}

func (legacyFile) TableName() string { return "files" }

type legacyFilePermission struct {
	ID          string     `gorm:"column:id;primaryKey"`
	FileID      string     `gorm:"column:file_id"`
	SubjectType int        `gorm:"column:subject_type"`
	SubjectID   string     `gorm:"column:subject_id"`
	Permission  int        `gorm:"column:permission"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at"`
	DeletedAt   *time.Time `gorm:"column:deleted_at"`
}

func (legacyFilePermission) TableName() string { return "file_permissions" }

type legacyFileReplica struct {
	ID        string     `gorm:"column:id;primaryKey"`
	ObjectID  string     `gorm:"column:object_id"`
	PoolID    *string    `gorm:"column:pool_id"`
	StorageID string     `gorm:"column:storage_id"`
	Status    int        `gorm:"column:status"`
	IsPrimary bool       `gorm:"column:is_primary"`
	CreatedAt time.Time  `gorm:"column:created_at"`
	UpdatedAt time.Time  `gorm:"column:updated_at"`
	DeletedAt *time.Time `gorm:"column:deleted_at"`
}

func (legacyFileReplica) TableName() string { return "file_replicas" }

func (m *Migrator) migratePools(summary *Summary) error {
	var rows []legacyPool
	if err := m.src.Order("created_at asc").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		pool := database.FilePool{ID: row.ID, Name: row.Name, Description: row.Description, AccountID: parseUUID(ptrValue(row.AccountID)), StorageConfig: row.StorageConfig, BillingConfig: row.BillingConfig, PolicyConfig: row.PolicyConfig, IsHidden: row.IsHidden, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		if row.DeletedAt != nil {
			pool.DeletedAt = gorm.DeletedAt{Time: *row.DeletedAt, Valid: true}
		}
		if err := m.save(&pool).Error; err != nil {
			if m.op.ContinueOnError {
				summary.Failed++
				continue
			}
			return err
		}
		summary.Pools++
	}
	return nil
}

func (m *Migrator) migrateQuotaRecords(summary *Summary) error {
	var rows []legacyQuotaRecord
	if err := m.src.Order("created_at asc").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		rec := database.QuotaRecord{ID: row.ID, AccountID: parseUUID(row.AccountID), Description: row.Description, Name: row.Name, Quota: row.Quota, ExpiredAt: row.ExpiredAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		if row.DeletedAt != nil {
			rec.DeletedAt = gorm.DeletedAt{Time: *row.DeletedAt, Valid: true}
		}
		if err := m.save(&rec).Error; err != nil {
			if m.op.ContinueOnError {
				summary.Failed++
				continue
			}
			return err
		}
		summary.QuotaRecords++
	}
	return nil
}

func (m *Migrator) migrateFileObjects(summary *Summary) error {
	var rows []legacyFileObject
	if err := m.src.Order("created_at asc").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		mime := "application/octet-stream"
		if row.MimeType != nil && strings.TrimSpace(*row.MimeType) != "" {
			mime = *row.MimeType
		}
		hash := ""
		if row.Hash != nil {
			hash = *row.Hash
		}
		obj := database.FileObject{ID: row.ID, Size: row.Size, MimeType: mime, Hash: hash, Meta: row.Meta, HasCompression: row.HasCompression, HasThumbnail: row.HasThumbnail, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		if row.DeletedAt != nil {
			obj.DeletedAt = gorm.DeletedAt{Time: *row.DeletedAt, Valid: true}
		}
		if err := m.save(&obj).Error; err != nil {
			if m.op.ContinueOnError {
				summary.Failed++
				continue
			}
			return err
		}
		summary.FileObjects++
	}
	return nil
}

func (m *Migrator) migrateFiles(summary *Summary) error {
	var rows []legacyFile
	if err := m.src.Order("created_at asc").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var desc *string
		if row.Description != nil {
			v := strings.TrimSpace(*row.Description)
			desc = &v
		}
		file := database.CloudFile{ID: row.ID, Name: row.Name, Description: desc, AccountID: parseUUID(row.AccountID), PoolID: nil, ObjectID: row.ObjectID, ParentID: row.ParentID, Indexed: row.Indexed, IsFolder: row.IsFolder, IsMarkedRecycle: row.IsMarkedRecycle, ExpiredAt: row.ExpiredAt, UploadedAt: row.UploadedAt, StorageID: row.StorageID, StorageURL: row.StorageURL, StorageKey: row.StorageID, FileMeta: row.FileMeta, UserMeta: row.UserMeta, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		if row.DeletedAt != nil {
			file.DeletedAt = gorm.DeletedAt{Time: *row.DeletedAt, Valid: true}
		}
		if file.StorageKey == nil && file.ObjectID != nil {
			key := *file.ObjectID
			file.StorageKey = &key
		}
		if err := m.save(&file).Error; err != nil {
			if m.op.ContinueOnError {
				summary.Failed++
				continue
			}
			return err
		}
		summary.Files++
	}
	return nil
}

func (m *Migrator) migrateFilePermissions(summary *Summary) error {
	var rows []legacyFilePermission
	if err := m.src.Order("created_at asc").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		perm, err := m.mapFilePermission(row)
		if err != nil {
			if m.op.ContinueOnError {
				summary.Failed++
				continue
			}
			return err
		}
		if err := m.save(perm).Error; err != nil {
			if m.op.ContinueOnError {
				summary.Failed++
				continue
			}
			return err
		}
		summary.FilePerms++
	}
	return nil
}

func (m *Migrator) migrateFileReplicas(summary *Summary) error {
	var rows []legacyFileReplica
	if err := m.src.Order("created_at asc").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		rep := database.FileReplica{ID: row.ID, ObjectID: row.ObjectID, PoolID: row.PoolID, StorageID: &row.StorageID, Status: m.mapReplicaStatus(row), IsPrimary: row.IsPrimary, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		if row.DeletedAt != nil {
			rep.DeletedAt = gorm.DeletedAt{Time: *row.DeletedAt, Valid: true}
		}
		if err := m.save(&rep).Error; err != nil {
			if m.op.ContinueOnError {
				summary.Failed++
				continue
			}
			return err
		}
		summary.FileReplicas++
	}
	return nil
}

func (m *Migrator) migrateDerived(summary *Summary) error {
	var files []database.CloudFile
	if err := m.dst.Preload("Object").Where("object_id IS NOT NULL").Find(&files).Error; err != nil {
		return err
	}
	for i := range files {
		parent := &files[i]
		if parent.Object == nil {
			continue
		}
		if parent.Object.HasThumbnail {
			if err := m.ensureDerived(parent, "thumbnail", "system.thumbnail", summary); err != nil {
				if m.op.ContinueOnError {
					summary.Failed++
					continue
				}
				return err
			}
		}
		if parent.Object.HasCompression {
			if err := m.ensureDerived(parent, "compressed", "system.compression.low", summary); err != nil {
				if m.op.ContinueOnError {
					summary.Failed++
					continue
				}
				return err
			}
		}
	}
	return nil
}

func (m *Migrator) ensureDerived(parent *database.CloudFile, suffix, appType string, summary *Summary) error {
	key := parent.ID + "." + suffix
	var existing database.CloudFile
	err := m.dst.Where("parent_id = ? AND application_type = ?", parent.ID, appType).First(&existing).Error
	if err == nil {
		summary.Skipped++
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	obj := database.FileObject{ID: database.NewID(), Size: parent.Object.Size, MimeType: parent.Object.MimeType, Hash: parent.Object.Hash, StorageKey: &key, Meta: parent.Object.Meta}
	if suffix == "thumbnail" {
		obj.HasThumbnail = true
	}
	if strings.HasPrefix(suffix, "compressed") {
		obj.HasCompression = true
	}
	if err := m.save(&obj).Error; err != nil {
		return err
	}
	pt := parent.ID
	t := appType
	child := database.CloudFile{ID: database.NewID(), Name: parent.Name, AccountID: parent.AccountID, PoolID: parent.PoolID, ObjectID: &obj.ID, ParentID: &pt, Indexed: false, IsFolder: false, IsMarkedRecycle: false, StorageKey: &key, FileMeta: datatypes.JSON([]byte(`{}`)), UserMeta: datatypes.JSON([]byte(`{}`)), ApplicationType: &t, CreatedAt: parent.CreatedAt, UpdatedAt: parent.UpdatedAt}
	if err := m.save(&child).Error; err != nil {
		return err
	}
	summary.DerivedObjects++
	summary.DerivedFiles++
	return nil
}

func (m *Migrator) mapFilePermission(row legacyFilePermission) (*database.FilePermission, error) {
	subjectType, ok := m.op.SubjectTypeMap[row.SubjectType]
	if !ok {
		return nil, fmt.Errorf("unknown legacy subject type %d for file permission %s", row.SubjectType, row.ID)
	}
	permission, ok := m.op.PermissionMap[row.Permission]
	if !ok {
		return nil, fmt.Errorf("unknown legacy permission %d for file permission %s", row.Permission, row.ID)
	}
	perm := &database.FilePermission{ID: row.ID, FileID: row.FileID, SubjectType: subjectType, SubjectID: row.SubjectID, Permission: permission, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.DeletedAt != nil {
		perm.DeletedAt = gorm.DeletedAt{Time: *row.DeletedAt, Valid: true}
	}
	return perm, nil
}

func (m *Migrator) mapReplicaStatus(row legacyFileReplica) string {
	if mapped, ok := m.op.ReplicaStatusMap[row.Status]; ok {
		return mapped
	}
	if row.IsPrimary {
		return "primary"
	}
	return strconv.Itoa(row.Status)
}

func (m *Migrator) save(value any) *gorm.DB {
	if m.op.DryRun {
		return &gorm.DB{Error: nil}
	}
	return m.dst.Clauses(clause.OnConflict{DoNothing: true}).Create(value)
}

func defaultSubjectTypeMap() map[int]string {
	return map[int]string{0: "private", 1: "account", 2: "scope", 3: "public"}
}
func defaultPermissionMap() map[int]string    { return map[int]string{0: "read", 1: "write", 2: "manage"} }
func defaultReplicaStatusMap() map[int]string { return map[int]string{} }

func parseUUID(v string) uuid.UUID {
	if strings.TrimSpace(v) == "" {
		return uuid.Nil
	}
	parsed, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil
	}
	return parsed
}

func ptrValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
