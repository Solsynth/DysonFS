package migratelegacy

import (
	"context"
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
	if err := m.ensureStorageKeyColumns(); err != nil {
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

func (m *Migrator) ensureStorageKeyColumns() error {
	statements := []string{
		`ALTER TABLE file_objects ALTER COLUMN storage_key TYPE varchar(64) USING storage_key::varchar(64)`,
		`ALTER TABLE cloud_files ALTER COLUMN storage_key TYPE varchar(64) USING storage_key::varchar(64)`,
		`ALTER TABLE persistent_tasks ALTER COLUMN storage_key TYPE varchar(64) USING storage_key::varchar(64)`,
	}
	for _, stmt := range statements {
		if err := m.dst.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

func (m *Migrator) lastCreatedAt(model any) time.Time {
	var t time.Time
	m.dst.Model(model).Select("COALESCE(MAX(created_at), '1970-01-01')").Scan(&t)
	return t
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
	since := m.lastCreatedAt(&database.FilePool{})
	var rows []legacyPool
	if err := m.src.Where("created_at > ?", since).Order("created_at asc").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("pools: up to date")
		return nil
	}
	fmt.Printf("pools: migrating %d new records\n", len(rows))
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
	since := m.lastCreatedAt(&database.QuotaRecord{})
	var rows []legacyQuotaRecord
	if err := m.src.Where("created_at > ?", since).Order("created_at asc").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("quota_records: up to date")
		return nil
	}
	fmt.Printf("quota_records: migrating %d new records\n", len(rows))
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
	since := m.lastCreatedAt(&database.FileObject{})
	var rows []legacyFileObject
	if err := m.src.Where("created_at > ?", since).Order("created_at asc").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("file_objects: up to date")
		return nil
	}
	fmt.Printf("file_objects: migrating %d new records\n", len(rows))
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
	since := m.lastCreatedAt(&database.CloudFile{})
	var rows []legacyFile
	if err := m.src.Where("created_at > ?", since).Order("created_at asc").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("files: up to date")
		return nil
	}
	fmt.Printf("files: migrating %d new records\n", len(rows))

	type parentUpdate struct {
		ID       string
		ParentID *string
	}
	var parentUpdates []parentUpdate

	for _, row := range rows {
		var desc *string
		if row.Description != nil {
			v := strings.TrimSpace(*row.Description)
			desc = &v
		}
		file := database.CloudFile{ID: row.ID, Name: row.Name, Description: desc, AccountID: parseUUID(row.AccountID), PoolID: nil, ObjectID: row.ObjectID, ParentID: nil, Indexed: row.Indexed, IsFolder: row.IsFolder, IsMarkedRecycle: row.IsMarkedRecycle, ExpiredAt: row.ExpiredAt, UploadedAt: row.UploadedAt, StorageID: row.StorageID, StorageURL: row.StorageURL, StorageKey: row.StorageID, FileMeta: row.FileMeta, UserMeta: row.UserMeta, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
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
		if row.ParentID != nil {
			parentUpdates = append(parentUpdates, parentUpdate{ID: row.ID, ParentID: row.ParentID})
		}
	}

	for _, u := range parentUpdates {
		if err := m.dst.Model(&database.CloudFile{}).Where("id = ?", u.ID).Update("parent_id", u.ParentID).Error; err != nil {
			if m.op.ContinueOnError {
				summary.Failed++
				continue
			}
			return err
		}
	}

	return nil
}

func (m *Migrator) migrateFilePermissions(summary *Summary) error {
	since := m.lastCreatedAt(&database.FilePermission{})
	var rows []legacyFilePermission
	if err := m.src.Where("created_at > ?", since).Order("created_at asc").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("file_permissions: up to date")
		return nil
	}
	fmt.Printf("file_permissions: migrating %d new records\n", len(rows))
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
	since := m.lastCreatedAt(&database.FileReplica{})
	var rows []legacyFileReplica
	if err := m.src.Where("created_at > ?", since).Order("created_at asc").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("file_replicas: up to date")
		return nil
	}
	fmt.Printf("file_replicas: migrating %d new records\n", len(rows))
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
	updates := []struct {
		appType string
		suffix  string
	}{
		{appType: "system.thumbnail", suffix: ".thumbnail"},
		{appType: "system.compression.low", suffix: ".compressed"},
	}
	for _, u := range updates {
		res := m.dst.Exec(`
			UPDATE cloud_files
			SET storage_key = object_id || ?
			WHERE application_type = ? AND object_id IS NOT NULL
		`, u.suffix, u.appType)
		if res.Error != nil {
			return res.Error
		}
		summary.DerivedFiles += res.RowsAffected

		res = m.dst.Exec(`
			UPDATE file_objects fo
			SET storage_key = cf.object_id || ?
			FROM cloud_files cf
			WHERE cf.object_id = fo.id
			  AND cf.application_type = ?
			  AND cf.object_id IS NOT NULL
		`, u.suffix, u.appType)
		if res.Error != nil {
			return res.Error
		}
		summary.DerivedObjects += res.RowsAffected
	}
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
