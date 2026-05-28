package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	blurhash "github.com/bbrks/go-blurhash"
	"github.com/davidbyttow/govips/v2/vips"
	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/tiff"
	ffmpeg "github.com/u2takey/ffmpeg-go"
	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	gen "src.solsynth.dev/sosys/go/proto"

	"github.com/gabriel-vasile/mimetype"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	dyauth "src.solsynth.dev/sosys/go/pkg/auth"
	sharedcache "src.solsynth.dev/sosys/go/pkg/cache"
)

type PoolConfig struct {
	RequirePrivilege int      `json:"require_privilege"`
	PublicUsable     bool     `json:"public_usable"`
	AllowEncryption  bool     `json:"allow_encryption"`
	AcceptTypes      []string `json:"accept_types"`
	MaxFileSize      *int64   `json:"max_file_size"`
	NoOptimization   bool     `json:"no_optimization"`
}

type PoolBillingConfig struct {
	CostMultiplier *float64 `json:"cost_multiplier"`
}

type PoolStorageConfig struct {
	EnableSigned   bool    `json:"enable_signed"`
	EnableSsl      bool    `json:"enable_ssl"`
	Endpoint       string  `json:"endpoint"`
	AccessEndpoint *string `json:"access_endpoint"`
	Bucket         string  `json:"bucket"`
	ImageProxy     *string `json:"image_proxy"`
	AccessProxy    *string `json:"access_proxy"`
	SecretId       string  `json:"secret_id"`
	SecretKey      string  `json:"secret_key"`
}

type Pool struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	AccountID     uuid.UUID         `json:"account_id"`
	StorageConfig PoolStorageConfig `json:"storage_config"`
	BillingConfig PoolBillingConfig `json:"billing_config"`
	PolicyConfig  PoolConfig        `json:"policy_config"`
	IsHidden      bool              `json:"is_hidden"`
}

type ReplicaRepairPreview struct {
	ObjectID   string `json:"object_id"`
	FileID     string `json:"file_id"`
	PoolID     string `json:"pool_id"`
	StorageKey string `json:"storage_key"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
}

type ReplicaRepairSummary struct {
	Scanned        int `json:"scanned"`
	Candidates     int `json:"candidates"`
	Verified       int `json:"verified"`
	Created        int `json:"created"`
	AlreadyPresent int `json:"already_present"`
	MissingPool    int `json:"missing_pool"`
	MissingKey     int `json:"missing_key"`
	MissingRemote  int `json:"missing_remote"`
	Failed         int `json:"failed"`
}

type SubjectPermission struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	Permission  string `json:"permission"`
}

type AccessContext struct {
	Account  *gen.DyAccount
	Session  *gen.DyAuthSession
	IsPublic bool
}

type FileService struct {
	db            *database.DB
	stor          storage.Backend
	cache         sharedcache.CacheService
	defaultPoolID string
	accessSecret  string
}

const systemPoolName = "system"
const compressedImageTargetBytes = 100 * 1024

func NewFileService(db *database.DB, stor storage.Backend) *FileService {
	return &FileService{db: db, stor: stor}
}

func (s *FileService) SetCache(cache sharedcache.CacheService) {
	s.cache = cache
}

func (s *FileService) DB() *database.DB { return s.db }

func (s *FileService) Storage() storage.Backend { return s.stor }

func (s *FileService) SetStorage(stor storage.Backend) { s.stor = stor }

func (s *FileService) SetAccessSecret(secret string) { s.accessSecret = secret }

func (s *FileService) AccessSecret() string { return s.accessSecret }

func (s *FileService) SeedPools(cfg *config.Config) (string, error) {
	if len(cfg.Pools) == 0 {
		return "", fmt.Errorf("at least one pool must be configured")
	}
	defaultCount := 0
	defaultPoolID := ""
	if err := s.db.DB.Transaction(func(tx *gorm.DB) error {
		for _, poolCfg := range cfg.Pools {
			poolID := strings.TrimSpace(poolCfg.ID)
			if poolID == "" {
				poolID = database.NewID()
			}
			name := strings.TrimSpace(poolCfg.Name)
			if name == "" {
				name = poolID
			}
			pool := database.FilePool{
				ID:            poolID,
				Name:          name,
				AccountID:     uuid.Nil,
				StorageConfig: mustJSON(mapPoolStorageConfig(poolCfg.Storage)),
				BillingConfig: mustJSON(PoolBillingConfig{CostMultiplier: poolCfg.Billing.CostMultiplier}),
				PolicyConfig:  mustJSON(PoolConfig{RequirePrivilege: poolCfg.Policy.RequirePrivilege, PublicUsable: poolCfg.Policy.PublicUsable, AllowEncryption: poolCfg.Policy.AllowEncryption, AcceptTypes: poolCfg.Policy.AcceptTypes, MaxFileSize: poolCfg.Policy.MaxFileSize, NoOptimization: poolCfg.Policy.NoOptimization}),
				IsHidden:      poolCfg.Hidden,
			}
			var existing database.FilePool
			if err := tx.Where("id = ?", poolID).First(&existing).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if err := tx.Create(&pool).Error; err != nil {
					return err
				}
			} else {
				if err := tx.Model(&database.FilePool{}).Where("id = ?", existing.ID).Updates(map[string]any{
					"name":           pool.Name,
					"storage_config": pool.StorageConfig,
					"billing_config": pool.BillingConfig,
					"policy_config":  pool.PolicyConfig,
					"is_hidden":      pool.IsHidden,
				}).Error; err != nil {
					return err
				}
			}
			if poolCfg.Default {
				defaultCount++
				defaultPoolID = poolID
			}
		}
		return nil
	}); err != nil {
		return "", err
	}
	if defaultCount != 1 {
		return "", fmt.Errorf("exactly one pool must be marked default")
	}
	s.defaultPoolID = defaultPoolID
	return defaultPoolID, nil
}

func (s *FileService) resolvedPoolID(poolID *string) *string {
	if poolID != nil && strings.TrimSpace(*poolID) != "" {
		resolved := strings.TrimSpace(*poolID)
		return &resolved
	}
	if strings.TrimSpace(s.defaultPoolID) == "" {
		return nil
	}
	resolved := s.defaultPoolID
	return &resolved
}

func (s *FileService) ResolvedPoolID(poolID *string) *string {
	return s.resolvedPoolID(poolID)
}

func (s *FileService) createPrimaryReplica(tx *gorm.DB, objectID string, poolID *string) error {
	if strings.TrimSpace(objectID) == "" {
		return fmt.Errorf("object id is required")
	}
	replica := &database.FileReplica{
		ID:        database.NewID(),
		ObjectID:  objectID,
		PoolID:    poolID,
		StorageID: poolID,
		Status:    "primary",
		IsPrimary: true,
	}
	return tx.Create(replica).Error
}

func (s *FileService) BackendForPoolID(poolID *string) (storage.Backend, error) {
	if poolID == nil || strings.TrimSpace(*poolID) == "" {
		return s.stor, nil
	}
	pool, err := s.GetPool(*poolID)
	if err != nil {
		return nil, err
	}
	return backendFromPoolStorage(pool.StorageConfig, s.stor)
}

func (s *FileService) BackendForFile(file *database.CloudFile) (storage.Backend, error) {
	if file == nil || file.StorageID == nil || strings.TrimSpace(*file.StorageID) == "" {
		return s.stor, nil
	}
	return s.BackendForPoolID(file.StorageID)
}

func backendFromPoolStorage(cfg PoolStorageConfig, fallback storage.Backend) (storage.Backend, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("storage backend not configured")
	}
	if strings.TrimSpace(cfg.SecretId) == "" && strings.TrimSpace(cfg.SecretKey) == "" && filepath.IsAbs(cfg.Endpoint) {
		return storage.NewLocalBackend(cfg.Endpoint), nil
	}
	return storage.NewS3Backend(cfg.Endpoint, cfg.SecretId, cfg.SecretKey, cfg.Bucket, cfg.EnableSsl)
}

func mapPoolStorageConfig(cfg config.StoragePoolConfig) PoolStorageConfig {
	return PoolStorageConfig{
		EnableSigned:   cfg.EnableSigned,
		EnableSsl:      cfg.EnableSsl,
		Endpoint:       cfg.Endpoint,
		AccessEndpoint: cfg.AccessEndpoint,
		Bucket:         cfg.Bucket,
		ImageProxy:     cfg.ImageProxy,
		AccessProxy:    cfg.AccessProxy,
		SecretId:       cfg.SecretId,
		SecretKey:      cfg.SecretKey,
	}
}

func mustJSON(v any) datatypes.JSON {
	raw, _ := json.Marshal(v)
	return datatypes.JSON(raw)
}

func systemPoolID() string { return "01SYSTEMPOOLID00000000000000" }

func SystemPoolID() string { return systemPoolID() }

func (s *FileService) GetFile(id string) (*database.CloudFile, error) {
	var file database.CloudFile
	if err := s.db.Preload("Object").First(&file, "id = ?", id).Error; err != nil {
		return nil, err
	}
	file.ChildrenCount = s.countChildren(file.ID)
	file.PermissionStatus = s.permissionStatus(&file)
	return &file, nil
}

func (s *FileService) GetBreadcrumb(fileID string) ([]database.CloudFile, error) {
	ids, err := s.loadAncestorIDs(fileID)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, gorm.ErrRecordNotFound
	}

	var files []database.CloudFile
	if err := s.db.Where("id IN ? AND deleted_at IS NULL", ids).Find(&files).Error; err != nil {
		return nil, err
	}

	filesByID := make(map[string]database.CloudFile, len(files))
	for _, file := range files {
		filesByID[file.ID] = file
	}

	breadcrumb := make([]database.CloudFile, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		file, ok := filesByID[ids[i]]
		if !ok {
			return nil, gorm.ErrRecordNotFound
		}
		breadcrumb = append(breadcrumb, file)
	}

	return breadcrumb, nil
}

func (s *FileService) GetChildren(parentID string) ([]database.CloudFile, error) {
	var files []database.CloudFile
	if err := s.db.Preload("Object").Where("parent_id = ?", parentID).Where("deleted_at IS NULL").Find(&files).Error; err != nil {
		return nil, err
	}
	if err := s.populateFilesMetadata(files); err != nil {
		return nil, err
	}
	return files, nil
}

func (s *FileService) GetPool(id string) (*Pool, error) {
	var pool database.FilePool
	if err := s.db.First(&pool, "id = ?", id).Error; err != nil {
		return nil, err
	}
	var policy PoolConfig
	var billing PoolBillingConfig
	var storage PoolStorageConfig
	_ = json.Unmarshal(pool.PolicyConfig, &policy)
	_ = json.Unmarshal(pool.BillingConfig, &billing)
	_ = json.Unmarshal(pool.StorageConfig, &storage)
	return &Pool{ID: pool.ID, Name: pool.Name, AccountID: pool.AccountID, PolicyConfig: policy, BillingConfig: billing, StorageConfig: storage, IsHidden: pool.IsHidden}, nil
}

func (s *FileService) ListPools(ctx AccessContext) ([]Pool, error) {
	var pools []database.FilePool
	if err := s.db.Find(&pools).Error; err != nil {
		return nil, err
	}
	out := make([]Pool, 0, len(pools))
	for _, p := range pools {
		var policy PoolConfig
		var billing PoolBillingConfig
		var storage PoolStorageConfig
		_ = json.Unmarshal(p.PolicyConfig, &policy)
		_ = json.Unmarshal(p.BillingConfig, &billing)
		_ = json.Unmarshal(p.StorageConfig, &storage)
		pool := &Pool{ID: p.ID, Name: p.Name, AccountID: p.AccountID, PolicyConfig: policy, BillingConfig: billing, StorageConfig: storage, IsHidden: p.IsHidden}
		if s.CanUsePool(ctx, pool, "read") {
			out = append(out, *pool)
		}
	}
	return out, nil
}

func (s *FileService) ListPoolPermissions(poolID string) ([]database.PoolPermission, error) {
	var perms []database.PoolPermission
	if err := s.db.Where("pool_id = ?", poolID).Find(&perms).Error; err != nil {
		return nil, err
	}
	return perms, nil
}

func (s *FileService) ListFilePermissions(fileID string) ([]database.FilePermission, error) {
	var perms []database.FilePermission
	if err := s.db.Where("file_id = ?", fileID).Find(&perms).Error; err != nil {
		return nil, err
	}
	return perms, nil
}

func (s *FileService) UpdatePoolPermissions(poolID string, perms []database.PoolPermission) error {
	return s.db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("pool_id = ?", poolID).Delete(&database.PoolPermission{}).Error; err != nil {
			return err
		}
		if len(perms) == 0 {
			return nil
		}
		return tx.Create(&perms).Error
	})
}

func (s *FileService) UpdateFilePermissions(fileID string, perms []database.FilePermission) error {
	if err := s.db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("file_id = ?", fileID).Delete(&database.FilePermission{}).Error; err != nil {
			return err
		}
		if len(perms) == 0 {
			return nil
		}
		return tx.Create(&perms).Error
	}); err != nil {
		return err
	}
	s.InvalidateFilePermissionCache(context.Background(), fileID)
	return nil
}

func (s *FileService) IsPoolPublic(poolID string) (bool, error) {
	pool, err := s.GetPool(poolID)
	if err != nil {
		return false, err
	}
	return pool.PolicyConfig.PublicUsable, nil
}

func (s *FileService) CanAccessPool(account *gen.DyAccount, pool *Pool, permission string) bool {
	if pool == nil {
		return false
	}
	if account != nil && account.GetIsSuperuser() {
		return true
	}
	if account != nil && pool.AccountID.String() == account.GetId() {
		return true
	}
	if pool.PolicyConfig.PublicUsable {
		return true
	}
	if permission == "read" {
		var perms []database.PoolPermission
		if err := s.db.Where("pool_id = ? AND permission = ?", pool.ID, permission).Find(&perms).Error; err != nil {
			return false
		}
		return len(perms) == 0
	}
	return false
}

func (s *FileService) CanUsePool(ctx AccessContext, pool *Pool, permission string) bool {
	if pool == nil {
		return false
	}
	if ctx.Account != nil && ctx.Account.GetIsSuperuser() {
		return true
	}
	if ctx.Account != nil && pool.AccountID.String() == ctx.Account.GetId() {
		return true
	}
	if pool.PolicyConfig.PublicUsable {
		return true
	}
	var perms []database.PoolPermission
	if err := s.db.Where("pool_id = ? AND permission = ?", pool.ID, permission).Find(&perms).Error; err != nil {
		return false
	}
	for _, perm := range perms {
		switch perm.SubjectType {
		case "account":
			if ctx.Account != nil && perm.SubjectID == ctx.Account.GetId() {
				return true
			}
		case "scope":
			if ctx.Session != nil {
				for _, scope := range ctx.Session.GetScopes() {
					if scope == perm.SubjectID {
						return true
					}
				}
			}
		}
	}
	return false
}

func (s *FileService) IsResourcePublic(perms []database.FilePermission) bool {
	return len(perms) == 0
}

type cachedFilePermissionLookup struct {
	HasSource bool                      `json:"has_source"`
	SourceID  string                    `json:"source_id"`
	Perms     []database.FilePermission `json:"perms"`
}

func (s *FileService) filePermissionCacheKey(fileID, permission string) string {
	return fmt.Sprintf("fs:file-perm:%s:%s", fileID, permission)
}

func (s *FileService) filePermissionGroupKey(fileID string) string {
	return "fs:file-perm-group:" + fileID
}

func (s *FileService) InvalidateFilePermissionCache(ctx context.Context, fileID string) {
	if s == nil || s.cache == nil || strings.TrimSpace(fileID) == "" {
		return
	}
	_ = s.cache.RemoveGroup(ctx, s.filePermissionGroupKey(fileID))
}

func (s *FileService) loadAncestorIDs(fileID string) ([]string, error) {
	const query = `WITH RECURSIVE ancestors AS (
		SELECT id, parent_id, 0 AS depth
		FROM cloud_files
		WHERE id = ? AND deleted_at IS NULL

		UNION ALL

		SELECT cf.id, cf.parent_id, ancestors.depth + 1
		FROM cloud_files cf
		JOIN ancestors ON cf.id = ancestors.parent_id
		WHERE cf.deleted_at IS NULL
	)
	SELECT id FROM ancestors ORDER BY depth`

	rows, err := s.db.DB.Raw(query, fileID).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]string, 0, 8)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *FileService) loadInheritedFilePermissions(fileID, permission string) (cachedFilePermissionLookup, error) {
	const sourceQuery = `WITH RECURSIVE ancestors AS (
		SELECT id, parent_id, 0 AS depth
		FROM cloud_files
		WHERE id = ? AND deleted_at IS NULL

		UNION ALL

		SELECT cf.id, cf.parent_id, ancestors.depth + 1
		FROM cloud_files cf
		JOIN ancestors ON cf.id = ancestors.parent_id
		WHERE cf.deleted_at IS NULL
	)
	SELECT id
	FROM ancestors a
	WHERE EXISTS (
		SELECT 1
		FROM file_permissions fp
		WHERE fp.file_id = a.id
		  AND fp.permission = ?
		  AND fp.deleted_at IS NULL
	)
	ORDER BY depth
	LIMIT 1`

	var sourceID string
	if err := s.db.DB.Raw(sourceQuery, fileID, permission).Scan(&sourceID).Error; err != nil {
		return cachedFilePermissionLookup{}, err
	}
	if strings.TrimSpace(sourceID) == "" {
		return cachedFilePermissionLookup{HasSource: false}, nil
	}

	var perms []database.FilePermission
	if err := s.db.Where("file_id = ? AND permission = ?", sourceID, permission).Find(&perms).Error; err != nil {
		return cachedFilePermissionLookup{}, err
	}
	return cachedFilePermissionLookup{HasSource: true, SourceID: sourceID, Perms: perms}, nil
}

func (s *FileService) loadInheritedFilePermissionsBatch(fileIDs []string, permission string) (map[string]cachedFilePermissionLookup, error) {
	if len(fileIDs) == 0 {
		return map[string]cachedFilePermissionLookup{}, nil
	}

	const sourceQuery = `WITH RECURSIVE ancestors AS (
		SELECT id AS file_id, id AS ancestor_id, parent_id, 0 AS depth
		FROM cloud_files
		WHERE id IN ? AND deleted_at IS NULL

		UNION ALL

		SELECT ancestors.file_id, cf.id AS ancestor_id, cf.parent_id, ancestors.depth + 1
		FROM cloud_files cf
		JOIN ancestors ON cf.id = ancestors.parent_id
		WHERE cf.deleted_at IS NULL
	), ranked_sources AS (
		SELECT a.file_id, a.ancestor_id AS source_id,
			ROW_NUMBER() OVER (PARTITION BY a.file_id ORDER BY a.depth) AS rn
		FROM ancestors a
		JOIN file_permissions fp ON fp.file_id = a.ancestor_id
		WHERE fp.permission = ?
		  AND fp.deleted_at IS NULL
	)
	SELECT file_id, source_id
	FROM ranked_sources
	WHERE rn = 1`

	type permissionSourceRow struct {
		FileID   string
		SourceID string
	}

	var sourceRows []permissionSourceRow
	if err := s.db.DB.Raw(sourceQuery, fileIDs, permission).Scan(&sourceRows).Error; err != nil {
		return nil, err
	}

	lookups := make(map[string]cachedFilePermissionLookup, len(fileIDs))
	if len(sourceRows) == 0 {
		return lookups, nil
	}

	sourceIDs := make([]string, 0, len(sourceRows))
	sourceToFileIDs := make(map[string][]string, len(sourceRows))
	for _, row := range sourceRows {
		if strings.TrimSpace(row.FileID) == "" || strings.TrimSpace(row.SourceID) == "" {
			continue
		}
		if _, ok := sourceToFileIDs[row.SourceID]; !ok {
			sourceIDs = append(sourceIDs, row.SourceID)
		}
		sourceToFileIDs[row.SourceID] = append(sourceToFileIDs[row.SourceID], row.FileID)
		lookups[row.FileID] = cachedFilePermissionLookup{HasSource: true, SourceID: row.SourceID}
	}

	var perms []database.FilePermission
	if err := s.db.Where("file_id IN ? AND permission = ?", sourceIDs, permission).Find(&perms).Error; err != nil {
		return nil, err
	}

	permsBySource := make(map[string][]database.FilePermission, len(sourceIDs))
	for _, perm := range perms {
		permsBySource[perm.FileID] = append(permsBySource[perm.FileID], perm)
	}

	for sourceID, fileIDs := range sourceToFileIDs {
		for _, fileID := range fileIDs {
			lookup := lookups[fileID]
			lookup.Perms = permsBySource[sourceID]
			lookups[fileID] = lookup
		}
	}

	return lookups, nil
}

func (s *FileService) resolveInheritedFilePermissions(ctx context.Context, fileID, permission string) (cachedFilePermissionLookup, error) {
	key := s.filePermissionCacheKey(fileID, permission)
	if s.cache != nil {
		var cached cachedFilePermissionLookup
		if ok, err := s.cache.GetData(ctx, key, &cached, "FilePermissionLookup"); err == nil && ok {
			return cached, nil
		}
	}

	lookup, err := s.loadInheritedFilePermissions(fileID, permission)
	if err != nil {
		return cachedFilePermissionLookup{}, err
	}

	if s.cache != nil {
		groups := []string{}
		if ancestors, err := s.loadAncestorIDs(fileID); err == nil {
			groups = make([]string, 0, len(ancestors))
			for _, ancestorID := range ancestors {
				groups = append(groups, s.filePermissionGroupKey(ancestorID))
			}
		}
		_ = s.cache.SetWithGroups(ctx, key, lookup, groups, 5*time.Minute)
	}

	return lookup, nil
}

func (s *FileService) CanAccessFile(account *gen.DyAccount, session *gen.DyAuthSession, file *database.CloudFile, permission string) bool {
	if file == nil {
		return false
	}
	if account != nil && account.GetIsSuperuser() {
		return true
	}
	if account != nil && file.AccountID.String() == account.GetId() {
		return true
	}
	lookup, err := s.resolveInheritedFilePermissions(context.Background(), file.ID, permission)
	if err != nil {
		return false
	}
	if !lookup.HasSource {
		return true
	}
	for _, perm := range lookup.Perms {
		switch perm.SubjectType {
		case "account":
			if account != nil && perm.SubjectID == account.GetId() {
				return true
			}
		case "scope":
			if session != nil {
				for _, scope := range session.GetScopes() {
					if scope == perm.SubjectID {
						return true
					}
				}
			}
		case "public":
			return true
		}
	}
	return false
}

func (s *FileService) ValidatePoolUsage(ctx AccessContext, poolID *string, fileSize int64, contentType string) error {
	if poolID == nil || strings.TrimSpace(*poolID) == "" {
		return nil
	}
	pool, err := s.GetPool(*poolID)
	if err != nil {
		return err
	}
	if !s.CanUsePool(ctx, pool, "write") {
		return fmt.Errorf("pool access denied")
	}
	if pool.PolicyConfig.MaxFileSize != nil && fileSize > *pool.PolicyConfig.MaxFileSize {
		return fmt.Errorf("file size exceeds pool limit")
	}
	if len(pool.PolicyConfig.AcceptTypes) > 0 && !acceptTypeAllowed(pool.PolicyConfig.AcceptTypes, contentType) {
		return fmt.Errorf("content type not accepted by pool")
	}
	return nil
}

func acceptTypeAllowed(acceptTypes []string, contentType string) bool {
	contentType = strings.TrimSpace(strings.ToLower(contentType))
	if contentType == "" {
		return false
	}
	for _, accepted := range acceptTypes {
		accepted = strings.TrimSpace(strings.ToLower(accepted))
		if accepted == "" {
			continue
		}
		if accepted == contentType {
			return true
		}
		if strings.HasSuffix(accepted, "/*") && strings.HasPrefix(contentType, strings.TrimSuffix(accepted, "*")) {
			return true
		}
	}
	return false
}

func (s *FileService) ListRoot(accountID uuid.UUID) ([]database.CloudFile, error) {
	var files []database.CloudFile
	if err := s.db.Preload("Object").Where("account_id = ? AND parent_id IS NULL AND indexed = true", accountID).Find(&files).Error; err != nil {
		return nil, err
	}
	if err := s.populateFilesMetadata(files); err != nil {
		return nil, err
	}
	return files, nil
}

func (s *FileService) ListRootOwned(accountID uuid.UUID, take int) ([]database.CloudFile, error) {
	var files []database.CloudFile
	query := s.db.Preload("Object").Where("account_id = ? AND parent_id IS NULL", accountID).Order("created_at desc")
	if take > 0 {
		query = query.Limit(take)
	}
	if err := query.Find(&files).Error; err != nil {
		return nil, err
	}
	if err := s.populateFilesMetadata(files); err != nil {
		return nil, err
	}
	return files, nil
}

func (s *FileService) ListOwned(accountID uuid.UUID) ([]database.CloudFile, error) {
	var files []database.CloudFile
	if err := s.db.Preload("Object").Where("account_id = ?", accountID).Find(&files).Error; err != nil {
		return nil, err
	}
	if err := s.populateFilesMetadata(files); err != nil {
		return nil, err
	}
	return files, nil
}

func (s *FileService) ListUnindexed(accountID uuid.UUID) ([]database.CloudFile, error) {
	var files []database.CloudFile
	if err := s.db.Preload("Object").Where("account_id = ? AND indexed = false AND parent_id IS NULL", accountID).Find(&files).Error; err != nil {
		return nil, err
	}
	if err := s.populateFilesMetadata(files); err != nil {
		return nil, err
	}
	return files, nil
}

func (s *FileService) CreateFolder(accountID uuid.UUID, name string, parentID *string) (*database.CloudFile, error) {
	file := &database.CloudFile{ID: database.NewID(), Name: name, AccountID: accountID, Indexed: true, IsFolder: true, ParentID: parentID}
	if err := s.db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(file).Error; err != nil {
			return err
		}
		if parentID != nil && strings.TrimSpace(*parentID) != "" {
			return nil
		}
		perm := database.FilePermission{ID: database.NewID(), FileID: file.ID, SubjectType: "private", SubjectID: "", Permission: "read"}
		return tx.Create(&perm).Error
	}); err != nil {
		return nil, err
	}
	return file, nil
}

func (s *FileService) countChildren(parentID string) int {
	var count int64
	_ = s.db.Model(&database.CloudFile{}).Where("parent_id = ? AND deleted_at IS NULL", parentID).Count(&count).Error
	return int(count)
}

func (s *FileService) countChildrenBatch(parentIDs []string) (map[string]int, error) {
	counts := make(map[string]int, len(parentIDs))
	if len(parentIDs) == 0 {
		return counts, nil
	}

	type childCountRow struct {
		ParentID string
		Count    int
	}

	var rows []childCountRow
	if err := s.db.Model(&database.CloudFile{}).
		Select("parent_id, COUNT(*) AS count").
		Where("parent_id IN ? AND deleted_at IS NULL", parentIDs).
		Group("parent_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	for _, row := range rows {
		counts[row.ParentID] = row.Count
	}
	return counts, nil
}

func permissionStatusFromLookup(lookup cachedFilePermissionLookup) database.PermissionStatus {
	if !lookup.HasSource {
		return database.PermissionStatus{Readable: true, Visibility: "public"}
	}
	visibility := "restricted"
	readable := false
	for _, perm := range lookup.Perms {
		switch perm.SubjectType {
		case "public":
			visibility = "public"
			readable = true
		case "private":
			visibility = "private"
		case "account", "scope":
			readable = true
		}
	}
	status := database.PermissionStatus{
		Readable:   readable || visibility == "public",
		Writable:   false,
		Manageable: false,
		Visibility: visibility,
	}
	if strings.TrimSpace(lookup.SourceID) != "" {
		status.InheritedFrom = &lookup.SourceID
	}
	return status
}

func (s *FileService) populateFilesMetadata(files []database.CloudFile) error {
	if len(files) == 0 {
		return nil
	}

	ids := make([]string, 0, len(files))
	for i := range files {
		if strings.TrimSpace(files[i].ID) != "" {
			ids = append(ids, files[i].ID)
		}
	}

	childCounts, err := s.countChildrenBatch(ids)
	if err != nil {
		return err
	}
	permissionLookups, err := s.loadInheritedFilePermissionsBatch(ids, "read")
	if err != nil {
		return err
	}

	for i := range files {
		files[i].ChildrenCount = childCounts[files[i].ID]
		lookup, ok := permissionLookups[files[i].ID]
		if !ok {
			lookup = cachedFilePermissionLookup{HasSource: false}
		}
		files[i].PermissionStatus = permissionStatusFromLookup(lookup)
	}

	return nil
}

func (s *FileService) permissionStatus(file *database.CloudFile) database.PermissionStatus {
	if file == nil {
		return database.PermissionStatus{Readable: true, Visibility: "public"}
	}
	lookup, err := s.resolveInheritedFilePermissions(context.Background(), file.ID, "read")
	if err != nil || !lookup.HasSource {
		return database.PermissionStatus{Readable: true, Visibility: "public"}
	}
	return permissionStatusFromLookup(lookup)
}

func (s *FileService) CreateFile(accountID uuid.UUID, name string, objectID string, parentID *string, appType *string) (*database.CloudFile, error) {
	file := &database.CloudFile{ID: database.NewID(), Name: name, AccountID: accountID, ObjectID: &objectID, ParentID: parentID, Indexed: true, ApplicationType: appType, FileMeta: datatypes.JSON([]byte(`{}`)), UserMeta: datatypes.JSON([]byte(`{}`))}
	if err := s.db.Create(file).Error; err != nil {
		return nil, err
	}
	return file, nil
}

func (s *FileService) MarkDerived(parentID string, kind string) error {
	var parent database.CloudFile
	if err := s.db.First(&parent, "id = ?", parentID).Error; err != nil {
		return err
	}
	var count int64
	if err := s.db.Model(&database.CloudFile{}).Where("parent_id = ? and application_type = ?", parentID, kind).Count(&count).Error; err != nil {
		return err
	}
	if kind == "system.thumbnail" {
		if parent.ObjectID == nil {
			return nil
		}
		return s.db.Model(&database.FileObject{}).Where("id = ?", *parent.ObjectID).Update("has_thumbnail", count > 0).Error
	}
	if strings.HasPrefix(kind, "system.compression") {
		if parent.ObjectID == nil {
			return nil
		}
		return s.db.Model(&database.FileObject{}).Where("id = ?", *parent.ObjectID).Update("has_compression", count > 0).Error
	}
	return nil
}

func (s *FileService) DeleteFile(id string) error {
	var file database.CloudFile
	if err := s.db.Select("id", "parent_id", "application_type").First(&file, "id = ?", id).Error; err != nil {
		return err
	}
	affectedIDs, err := s.loadDescendantIDs([]string{id})
	if err != nil {
		return err
	}
	if err := s.db.Delete(&database.CloudFile{}, "id IN ?", affectedIDs).Error; err != nil {
		return err
	}
	if err := s.touchDerivedParentFlags(&file); err != nil {
		return err
	}
	s.invalidatePermissionCaches(context.Background(), affectedIDs)
	return nil
}

func (s *FileService) RecycleFile(id string) error {
	return s.db.Model(&database.CloudFile{}).Where("id = ?", id).Update("is_marked_recycle", true).Error
}

func (s *FileService) RecycleBatch(ids []string) (int64, error) {
	ids = normalizeFileIDs(ids)
	if len(ids) == 0 {
		return 0, nil
	}
	tx := s.db.Model(&database.CloudFile{}).Where("id IN ?", ids).Update("is_marked_recycle", true)
	return tx.RowsAffected, tx.Error
}

func (s *FileService) RestoreFile(id string) error {
	return s.db.Model(&database.CloudFile{}).Where("id = ?", id).Update("is_marked_recycle", false).Error
}

func (s *FileService) RestoreBatch(ids []string) (int64, error) {
	ids = normalizeFileIDs(ids)
	if len(ids) == 0 {
		return 0, nil
	}
	tx := s.db.Model(&database.CloudFile{}).Where("id IN ?", ids).Update("is_marked_recycle", false)
	return tx.RowsAffected, tx.Error
}

func (s *FileService) PurgeFile(id string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("database not configured")
	}
	affectedIDs, err := s.loadDescendantIDsIncludingDeleted([]string{id})
	if err != nil {
		return err
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var file database.CloudFile
		if err := tx.Unscoped().Preload("Object").First(&file, "id = ?", id).Error; err != nil {
			return err
		}
		var files []database.CloudFile
		if err := tx.Unscoped().Where("id IN ?", affectedIDs).Find(&files).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Delete(&database.CloudFile{}, "id IN ?", affectedIDs).Error; err != nil {
			return err
		}
		if err := s.touchDerivedParentFlagsTx(tx, &file); err != nil {
			return err
		}
		if err := tx.Unscoped().Delete(&database.FilePermission{}, "file_id IN ?", affectedIDs).Error; err != nil {
			return err
		}
		seenObjectIDs := make(map[string]struct{}, len(files))
		for _, descendant := range files {
			if descendant.ObjectID == nil {
				continue
			}
			objectID := strings.TrimSpace(*descendant.ObjectID)
			if objectID == "" {
				continue
			}
			if _, ok := seenObjectIDs[objectID]; ok {
				continue
			}
			seenObjectIDs[objectID] = struct{}{}
			if err := s.purgeObjectIfDereferenced(tx, &descendant, objectID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	s.invalidatePermissionCaches(context.Background(), affectedIDs)
	return nil
}

func (s *FileService) PurgeBatch(ids []string) (int64, error) {
	ids = normalizeFileIDs(ids)
	if len(ids) == 0 {
		return 0, nil
	}
	var count int64
	for _, id := range ids {
		if err := s.PurgeFile(id); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (s *FileService) touchDerivedParentFlags(file *database.CloudFile) error {
	if s == nil || s.db == nil || s.db.DB == nil {
		return nil
	}
	return s.touchDerivedParentFlagsTx(s.db.DB, file)
}

func (s *FileService) touchDerivedParentFlagsTx(tx *gorm.DB, file *database.CloudFile) error {
	if tx == nil || file == nil || file.ParentID == nil || file.ApplicationType == nil {
		return nil
	}
	kind := strings.TrimSpace(*file.ApplicationType)
	if kind != "system.thumbnail" && !strings.HasPrefix(kind, "system.compression") {
		return nil
	}
	parentID := strings.TrimSpace(*file.ParentID)
	if parentID == "" {
		return nil
	}
	var thumb, comp int64
	if err := tx.Model(&database.CloudFile{}).Where("parent_id = ? and application_type = ?", parentID, "system.thumbnail").Count(&thumb).Error; err != nil {
		return err
	}
	if err := tx.Model(&database.CloudFile{}).Where("parent_id = ? and application_type LIKE ?", parentID, "system.compression%").Count(&comp).Error; err != nil {
		return err
	}
	return tx.Model(&database.FileObject{}).
		Where("id = (SELECT object_id FROM cloud_files WHERE id = ?)", parentID).
		Updates(map[string]any{"has_thumbnail": thumb > 0, "has_compression": comp > 0}).Error
}

func (s *FileService) MoveBatch(ids []string, parentID *string) (int64, error) {
	ids = normalizeFileIDs(ids)
	if len(ids) == 0 {
		return 0, nil
	}
	var resolvedParentID *string
	if parentID != nil {
		trimmed := strings.TrimSpace(*parentID)
		if trimmed != "" {
			resolvedParentID = &trimmed
		}
	}
	if resolvedParentID != nil {
		descendantIDs, err := s.loadDescendantIDs(ids)
		if err != nil {
			return 0, err
		}
		for _, descendantID := range descendantIDs {
			if descendantID == *resolvedParentID {
				return 0, fmt.Errorf("parent_id cannot reference a moved file or its descendant")
			}
		}
	}
	var parentValue any
	if resolvedParentID != nil {
		parentValue = *resolvedParentID
	}
	tx := s.db.Model(&database.CloudFile{}).Where("id IN ?", ids).Updates(map[string]any{"parent_id": parentValue})
	if tx.Error != nil {
		return tx.RowsAffected, tx.Error
	}
	descendantIDs, err := s.loadDescendantIDs(ids)
	if err != nil {
		return tx.RowsAffected, err
	}
	s.invalidatePermissionCaches(context.Background(), descendantIDs)
	return tx.RowsAffected, nil
}

func (s *FileService) loadDescendantIDs(fileIDs []string) ([]string, error) {
	return s.loadDescendantIDsWithDeleted(fileIDs, false)
}

func (s *FileService) loadDescendantIDsIncludingDeleted(fileIDs []string) ([]string, error) {
	return s.loadDescendantIDsWithDeleted(fileIDs, true)
}

func (s *FileService) loadDescendantIDsWithDeleted(fileIDs []string, includeDeleted bool) ([]string, error) {
	fileIDs = normalizeFileIDs(fileIDs)
	if len(fileIDs) == 0 {
		return []string{}, nil
	}

	recursiveFilter := "WHERE cf.deleted_at IS NULL"
	if includeDeleted {
		recursiveFilter = ""
	}
	query := `WITH RECURSIVE descendants AS (
		SELECT id, parent_id
		FROM cloud_files
		WHERE id IN ?

		UNION

		SELECT cf.id, cf.parent_id
		FROM cloud_files cf
		JOIN descendants d ON cf.parent_id = d.id
		` + recursiveFilter + `
	)
	SELECT DISTINCT id
	FROM descendants`

	type descendantRow struct {
		ID string
	}

	var rows []descendantRow
	if err := s.db.DB.Raw(query, fileIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.ID) == "" {
			continue
		}
		ids = append(ids, row.ID)
	}
	return ids, nil
}

func (s *FileService) invalidatePermissionCaches(ctx context.Context, fileIDs []string) {
	if s == nil || s.cache == nil {
		return
	}
	seen := make(map[string]struct{}, len(fileIDs))
	for _, fileID := range fileIDs {
		fileID = strings.TrimSpace(fileID)
		if fileID == "" {
			continue
		}
		if _, ok := seen[fileID]; ok {
			continue
		}
		seen[fileID] = struct{}{}
		s.InvalidateFilePermissionCache(ctx, fileID)
	}
}

func (s *FileService) purgeObjectIfDereferenced(tx *gorm.DB, file *database.CloudFile, objectID string) error {
	var refCount int64
	if err := tx.Model(&database.CloudFile{}).Where("object_id = ? AND deleted_at IS NULL", objectID).Count(&refCount).Error; err != nil {
		return err
	}
	if refCount > 0 {
		return nil
	}

	var object database.FileObject
	if err := tx.Unscoped().First(&object, "id = ?", objectID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	var replicas []database.FileReplica
	if err := tx.Unscoped().Where("object_id = ?", objectID).Find(&replicas).Error; err != nil {
		return err
	}

	storageKey := firstNonEmptyPtr(object.StorageKey, file.StorageKey, file.ObjectID)
	if storageKey != nil && strings.TrimSpace(*storageKey) != "" {
		if err := s.deleteObjectFromBackends(context.Background(), file, replicas, *storageKey); err != nil {
			return err
		}
	}

	if err := tx.Unscoped().Delete(&database.FileReplica{}, "object_id = ?", objectID).Error; err != nil {
		return err
	}
	return tx.Unscoped().Delete(&database.FileObject{}, "id = ?", objectID).Error
}

func (s *FileService) deleteObjectFromBackends(ctx context.Context, file *database.CloudFile, replicas []database.FileReplica, storageKey string) error {
	if s == nil {
		return fmt.Errorf("file service not configured")
	}
	if strings.TrimSpace(storageKey) == "" {
		return nil
	}

	type backendTarget struct {
		storageID *string
		poolID    *string
	}
	targets := make([]backendTarget, 0, len(replicas)+1)
	seen := make(map[string]struct{})
	addTarget := func(storageID, poolID *string) {
		storageKey := ""
		if storageID != nil {
			storageKey = "storage:" + strings.TrimSpace(*storageID)
		}
		poolKey := ""
		if poolID != nil {
			poolKey = "pool:" + strings.TrimSpace(*poolID)
		}
		key := storageKey + "|" + poolKey
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		targets = append(targets, backendTarget{storageID: storageID, poolID: poolID})
	}

	for _, replica := range replicas {
		addTarget(replica.StorageID, replica.PoolID)
	}
	if len(targets) == 0 && file != nil {
		addTarget(file.StorageID, file.PoolID)
	}
	if len(targets) == 0 {
		targets = append(targets, backendTarget{})
	}

	for _, target := range targets {
		backend, err := s.backendForStorageTarget(target.storageID, target.poolID)
		if err != nil {
			return err
		}
		if err := backend.Delete(ctx, storageKey); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func normalizeFileIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (s *FileService) backendForStorageTarget(storageID, poolID *string) (storage.Backend, error) {
	if storageID != nil && strings.TrimSpace(*storageID) != "" {
		return s.BackendForPoolID(storageID)
	}
	if poolID != nil && strings.TrimSpace(*poolID) != "" {
		return s.BackendForPoolID(poolID)
	}
	return s.stor, nil
}

func (s *FileService) PurgeRecycleBin(accountID uuid.UUID) (int64, error) {
	tx := s.db.Where("account_id = ? AND is_marked_recycle = true", accountID).Delete(&database.CloudFile{})
	return tx.RowsAffected, tx.Error
}

func (s *FileService) EnsureDerivedChildren(accountID uuid.UUID, parentID, baseName string) error {
	_ = accountID
	_ = parentID
	_ = baseName
	return nil
}

func (s *FileService) TouchCompatibilityFlags(fileID string) error {
	var file database.CloudFile
	if err := s.db.Preload("Object").First(&file, "id = ?", fileID).Error; err != nil {
		return err
	}
	if file.ObjectID == nil {
		return nil
	}
	var thumb, comp int64
	if err := s.db.Model(&database.CloudFile{}).Where("parent_id = ? and application_type = ?", fileID, "system.thumbnail").Count(&thumb).Error; err != nil {
		return err
	}
	if err := s.db.Model(&database.CloudFile{}).Where("parent_id = ? and application_type LIKE ?", fileID, "system.compression%").Count(&comp).Error; err != nil {
		return err
	}
	if err := s.db.Model(&database.FileObject{}).Where("id = ?", *file.ObjectID).Updates(map[string]any{"has_thumbnail": thumb > 0, "has_compression": comp > 0}).Error; err != nil {
		return err
	}
	return nil
}

func (s *FileService) SaveChunk(tempDir, taskID string, idx int, reader io.Reader) (string, error) {
	chunkDir := filepath.Join(tempDir, taskID)
	if err := os.MkdirAll(chunkDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(chunkDir, fmt.Sprintf("%d.chunk", idx))
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, reader); err != nil {
		return "", err
	}
	return path, nil
}

func (s *FileService) MergeChunks(taskID, chunkDir, mergedPath string, chunksCount int, progress func(int, int) error) error {
	out, err := os.Create(mergedPath)
	if err != nil {
		return err
	}
	defer out.Close()
	for i := 0; i < chunksCount; i++ {
		path := filepath.Join(chunkDir, fmt.Sprintf("%d.chunk", i))
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = in.Close()
			return err
		}
		_ = in.Close()
		if progress != nil {
			if err := progress(i+1, chunksCount); err != nil {
				return err
			}
		}
	}
	_ = taskID
	return nil
}

func (s *FileService) DetectAndCreateObject(path string) (*database.FileObject, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	mimeType := "application/octet-stream"
	if len(data) > 0 {
		mimeType = mimetype.Detect(data).String()
	}
	object := &database.FileObject{ID: database.NewID(), Size: int64(len(data)), MimeType: mimeType, Hash: ComputeHash(data), Meta: datatypes.JSON([]byte(`{}`)), HasCompression: false, HasThumbnail: false}
	if err := s.db.Create(object).Error; err != nil {
		return nil, err
	}
	return object, nil
}

func ComputeHash(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type ImageAnalysis struct {
	Width    int
	Height   int
	Blurhash string
	Exif     map[string]any
}

type SourceAnalysis struct {
	Width  int
	Height int
	Image  *ImageAnalysis
	Media  map[string]any
}

func (s *FileService) AnalyzeImage(path string) (*ImageAnalysis, error) {
	img, err := vips.NewImageFromFile(path)
	if err != nil {
		return nil, err
	}
	defer img.Close()

	if err := img.AutoRotate(); err != nil {
		return nil, err
	}
	if err := img.RemoveMetadata(); err != nil {
		return nil, err
	}

	blur, err := analyzeBlurhash(path)
	if err != nil {
		return nil, err
	}

	exifMeta, err := extractExif(path)
	if err != nil {
		exifMeta = nil
	}

	return &ImageAnalysis{Width: img.Width(), Height: img.Height(), Blurhash: blur, Exif: exifMeta}, nil
}

func (s *FileService) AnalyzeSourceFile(ctx context.Context, path, mimeType string) (*SourceAnalysis, error) {
	analysis := &SourceAnalysis{}
	if strings.HasPrefix(mimeType, "image/") {
		img, err := s.AnalyzeImage(path)
		if err != nil {
			return nil, err
		}
		analysis.Width = img.Width
		analysis.Height = img.Height
		analysis.Image = img
		return analysis, nil
	}
	if strings.HasPrefix(mimeType, "video/") || strings.HasPrefix(mimeType, "audio/") {
		media, err := probeMedia(ctx, path)
		if err != nil {
			return nil, err
		}
		analysis.Width, analysis.Height = mediaDimensions(media)
		analysis.Media = media
		return analysis, nil
	}
	return analysis, nil
}

func mediaDimensions(media map[string]any) (width, height int) {
	streams, ok := media["streams"].([]any)
	if !ok {
		return 0, 0
	}
	for _, raw := range streams {
		stream, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		codecType, _ := stream["codec_type"].(string)
		if codecType != "video" {
			continue
		}
		width = intFromAny(stream["width"])
		height = intFromAny(stream["height"])
		if width > 0 || height > 0 {
			return width, height
		}
	}
	return 0, 0
}

func intFromAny(v any) int {
	switch value := v.(type) {
	case float64:
		return int(value)
	case float32:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	case int32:
		return int(value)
	case json.Number:
		n, err := value.Int64()
		if err == nil {
			return int(n)
		}
	}
	return 0
}

func probeMedia(ctx context.Context, path string) (map[string]any, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var payload map[string]any
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func extractExif(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	x, err := exif.Decode(f)
	if err != nil {
		return nil, err
	}
	meta := map[string]any{}
	_ = x.Walk(exifWalker{meta: meta})
	if len(meta) == 0 {
		return nil, nil
	}
	return meta, nil
}

type exifWalker struct{ meta map[string]any }

func (w exifWalker) Walk(name exif.FieldName, tag *tiff.Tag) error {
	if tag == nil {
		return nil
	}
	w.meta[string(name)] = tag.String()
	return nil
}

func analyzeBlurhash(path string) (string, error) {
	img, err := vips.NewImageFromFile(path)
	if err != nil {
		return "", err
	}
	defer img.Close()

	buf, _, err := img.ExportPng(&vips.PngExportParams{StripMetadata: true})
	if err != nil {
		return "", err
	}
	decoded, err := png.Decode(bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	hash, err := blurhash.Encode(4, 3, decoded)
	if err != nil {
		return "", err
	}
	return hash, nil
}

func mergeJSONMeta(raw datatypes.JSON, updates map[string]any) (datatypes.JSON, error) {
	meta := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 && string(bytes.TrimSpace(raw)) != "null" {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, err
		}
	}
	for k, v := range updates {
		meta[k] = v
	}
	merged, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(merged), nil
}

func (s *FileService) StoreImageAnalysis(fileID string, analysis *ImageAnalysis) (*database.CloudFile, error) {
	return s.StoreSourceAnalysis(fileID, &SourceAnalysis{Image: analysis})
}

func (s *FileService) StoreSourceAnalysis(fileID string, analysis *SourceAnalysis) (*database.CloudFile, error) {
	if analysis == nil {
		return s.GetFile(fileID)
	}
	if err := s.db.DB.Transaction(func(tx *gorm.DB) error {
		var file database.CloudFile
		if err := tx.Preload("Object").First(&file, "id = ?", fileID).Error; err != nil {
			return err
		}
		updates := map[string]any{}
		if analysis.Width > 0 {
			updates["width"] = analysis.Width
		}
		if analysis.Height > 0 {
			updates["height"] = analysis.Height
		}
		if analysis.Image != nil {
			updates["width"] = analysis.Image.Width
			updates["height"] = analysis.Image.Height
			updates["blurhash"] = analysis.Image.Blurhash
			updates["exif_version"] = 2
			if len(analysis.Image.Exif) > 0 {
				updates["exif"] = analysis.Image.Exif
			}
		}
		if len(analysis.Media) > 0 {
			updates["media"] = analysis.Media
		}
		if file.ObjectID != nil {
			var object database.FileObject
			if err := tx.First(&object, "id = ?", *file.ObjectID).Error; err != nil {
				return err
			}
			mergedObjectMeta, err := mergeJSONMeta(object.LegacyMeta(), updates)
			if err != nil {
				return err
			}
			if err := tx.Model(&database.FileObject{}).Where("id = ?", object.ID).Update("meta", mergedObjectMeta).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return s.GetFile(fileID)
}

type ReanalysisResult struct {
	Scanned int `json:"scanned"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

type ReanalysisCandidate struct {
	FileID     string    `json:"file_id"`
	Name       string    `json:"name"`
	MimeType   string    `json:"mime_type"`
	Size       int64     `json:"size"`
	Kind       string    `json:"kind,omitempty"`
	StorageKey string    `json:"storage_key,omitempty"`
	ObjectID   string    `json:"object_id,omitempty"`
	Reason     string    `json:"reason"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s *FileService) ListReanalysisCandidates(ctx context.Context, limit int) ([]ReanalysisCandidate, error) {
	if s.stor == nil {
		return nil, fmt.Errorf("storage backend not configured")
	}
	if limit <= 0 {
		limit = 100
	}
	var files []database.CloudFile
	if err := s.db.Preload("Object").Where("deleted_at IS NULL").Where("is_folder = false").Order("updated_at desc").Limit(limit).Find(&files).Error; err != nil {
		return nil, err
	}
	candidates := make([]ReanalysisCandidate, 0, len(files))
	for i := range files {
		file := &files[i]
		if file.Object == nil {
			continue
		}
		reason, kind := s.reanalysisReason(file)
		if reason == "" && kind == "image" {
			reason = s.imageVariantRepairReason(file)
		}
		if reason == "" {
			continue
		}
		candidate := ReanalysisCandidate{FileID: file.ID, Name: file.Name, MimeType: file.Object.MimeType, Size: file.Object.Size, UpdatedAt: file.UpdatedAt, Reason: reason, Kind: kind}
		if file.ObjectID != nil {
			candidate.ObjectID = *file.ObjectID
		}
		if file.StorageKey != nil {
			candidate.StorageKey = *file.StorageKey
		} else if file.Object != nil && file.Object.StorageKey != nil {
			candidate.StorageKey = *file.Object.StorageKey
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func (s *FileService) ListImageReanalysisCandidates(ctx context.Context, limit int) ([]ReanalysisCandidate, error) {
	items, err := s.ListReanalysisCandidates(ctx, limit)
	if err != nil {
		return nil, err
	}
	filtered := make([]ReanalysisCandidate, 0, len(items))
	for _, item := range items {
		if item.Kind == "image" {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *FileService) RepairReanalysisCandidate(ctx context.Context, fileID string) error {
	if s.stor == nil {
		return fmt.Errorf("storage backend not configured")
	}
	file, path, cleanup, err := s.prepareSourceReanalysis(ctx, fileID)
	if err != nil {
		return err
	}
	if file == nil || file.Object == nil {
		return nil
	}
	defer cleanup()
	if err := s.rebuildSourceVariants(ctx, file, path); err != nil {
		return err
	}
	analysis, err := s.AnalyzeSourceFile(ctx, path, file.Object.MimeType)
	if err != nil {
		return err
	}
	if _, err := s.StoreSourceAnalysis(file.ID, analysis); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if err := s.db.Model(&database.FileObject{}).Where("id = ?", file.Object.ID).Update("size", info.Size()).Error; err != nil {
		return err
	}
	resolvedKey := firstNonEmptyPtr(file.StorageKey, file.Object.StorageKey, file.ObjectID)
	if file.StorageKey == nil && resolvedKey != nil {
		_ = s.db.Model(&database.CloudFile{}).Where("id = ?", file.ID).Update("storage_key", *resolvedKey).Error
	}
	if file.Object.StorageKey == nil && resolvedKey != nil {
		_ = s.db.Model(&database.FileObject{}).Where("id = ?", file.Object.ID).Update("storage_key", *resolvedKey).Error
	}
	return s.TouchCompatibilityFlags(file.ID)
}

func (s *FileService) RepairImageMetadataCandidate(ctx context.Context, fileID string) error {
	return s.RepairReanalysisCandidate(ctx, fileID)
}

func (s *FileService) imageVariantRepairReason(file *database.CloudFile) string {
	if file == nil || file.Object == nil || !strings.HasPrefix(file.Object.MimeType, "image/") {
		return ""
	}
	var reasons []string
	if child, err := s.getDerivedChild(file.ID, "system.compression.low"); err != nil || child == nil {
		reasons = append(reasons, "missing compressed variant")
	}
	if len(reasons) == 0 {
		return ""
	}
	return strings.Join(reasons, ", ")
}

func (s *FileService) getDerivedChild(parentID, kind string) (*database.CloudFile, error) {
	var child database.CloudFile
	if err := s.db.Preload("Object").First(&child, "parent_id = ? and application_type = ? and deleted_at IS NULL", parentID, kind).Error; err != nil {
		return nil, err
	}
	return &child, nil
}

func (s *FileService) rebuildImageVariants(ctx context.Context, file *database.CloudFile, storageKey string) error {
	if s == nil || s.stor == nil || file == nil || file.Object == nil {
		return nil
	}
	rc, _, err := s.stor.Get(ctx, storageKey)
	if err != nil {
		return err
	}
	defer rc.Close()
	path, cleanup, err := writeTempObject(rc)
	if err != nil {
		return err
	}
	defer cleanup()
	img, err := vips.NewImageFromFile(path)
	if err != nil {
		return err
	}
	defer img.Close()
	if err := img.AutoRotate(); err != nil {
		return err
	}
	if err := img.RemoveMetadata(); err != nil {
		return err
	}
	if img.Pages() > 1 {
		return nil
	}
	origBuf, _, err := img.ExportWebp(&vips.WebpExportParams{Lossless: true, StripMetadata: true})
	if err != nil {
		return err
	}
	compBuf, err := exportCompressedWebp(img, origBuf, compressedImageTargetBytes)
	if err != nil {
		return err
	}
	if len(compBuf) > 0 {
		if err := s.persistReanalysisVariant(ctx, file, "system.compression.low", compBuf, "image/webp", ".compressed"); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileService) prepareSourceReanalysis(ctx context.Context, fileID string) (*database.CloudFile, string, func(), error) {
	var file database.CloudFile
	if err := s.db.Preload("Object").First(&file, "id = ?", fileID).Error; err != nil {
		return nil, "", func() {}, err
	}
	if file.Object == nil {
		return nil, "", func() {}, nil
	}
	backend, err := s.BackendForFile(&file)
	if err != nil {
		return nil, "", func() {}, err
	}
	storageKey := firstNonEmptyPtr(file.StorageKey, file.Object.StorageKey, file.ObjectID)
	if storageKey == nil || strings.TrimSpace(*storageKey) == "" {
		return nil, "", func() {}, fmt.Errorf("storage key missing")
	}
	rc, _, err := backend.Get(ctx, *storageKey)
	if err != nil {
		return nil, "", func() {}, err
	}
	path, cleanup, err := writeTempObject(rc)
	_ = rc.Close()
	if err != nil {
		return nil, "", func() {}, err
	}
	return &file, path, cleanup, nil
}

func (s *FileService) rebuildSourceVariants(ctx context.Context, file *database.CloudFile, path string) error {
	if s == nil || file == nil || file.Object == nil {
		return nil
	}
	if strings.HasPrefix(file.Object.MimeType, "image/") {
		return s.rebuildImageVariantsFromPath(ctx, file, path)
	}
	if strings.HasPrefix(file.Object.MimeType, "video/") {
		return s.rebuildVideoThumbnailFromPath(ctx, file, path)
	}
	return nil
}

func (s *FileService) rebuildImageVariantsFromPath(ctx context.Context, file *database.CloudFile, path string) error {
	if s == nil || s.stor == nil || file == nil || file.Object == nil {
		return nil
	}
	img, err := vips.NewImageFromFile(path)
	if err != nil {
		return err
	}
	defer img.Close()
	if err := img.AutoRotate(); err != nil {
		return err
	}
	if err := img.RemoveMetadata(); err != nil {
		return err
	}
	if img.Pages() > 1 {
		return nil
	}
	origBuf, _, err := img.ExportWebp(&vips.WebpExportParams{Lossless: true, StripMetadata: true})
	if err != nil {
		return err
	}
	compBuf, err := exportCompressedWebp(img, origBuf, compressedImageTargetBytes)
	if err != nil {
		return err
	}
	if len(compBuf) > 0 {
		if err := s.persistReanalysisVariant(ctx, file, "system.compression.low", compBuf, "image/webp", ".compressed"); err != nil {
			return err
		}
	}
	return nil
}

func exportCompressedWebp(img *vips.ImageRef, original []byte, targetBytes int) ([]byte, error) {
	if img == nil {
		return nil, nil
	}
	maxEdge := img.Width()
	if img.Height() > maxEdge {
		maxEdge = img.Height()
	}
	if maxEdge <= 0 {
		return nil, nil
	}

	steps := []struct {
		maxEdge int
		quality int
	}{
		{maxEdge, 82},
		{1920, 80},
		{1600, 76},
		{1280, 72},
		{960, 68},
		{720, 64},
		{512, 60},
		{384, 55},
	}
	var smallest []byte
	for _, step := range steps {
		candidate, err := img.Copy()
		if err != nil {
			return nil, err
		}
		if step.maxEdge > 0 && maxEdge > step.maxEdge {
			scale := float64(step.maxEdge) / float64(maxEdge)
			if err := candidate.Resize(scale, vips.KernelLanczos3); err != nil {
				candidate.Close()
				return nil, err
			}
		}
		buf, _, err := candidate.ExportWebp(&vips.WebpExportParams{Quality: step.quality, StripMetadata: true})
		candidate.Close()
		if err != nil {
			return nil, err
		}
		if len(smallest) == 0 || len(buf) < len(smallest) {
			smallest = buf
		}
		if len(buf) <= targetBytes {
			return buf, nil
		}
	}
	if len(original) <= targetBytes {
		return original, nil
	}
	return smallest, nil
}

func (s *FileService) rebuildVideoThumbnailFromPath(ctx context.Context, file *database.CloudFile, path string) error {
	if s == nil || s.stor == nil || file == nil || file.Object == nil {
		return nil
	}
	thumbPath := filepath.Join(os.TempDir(), file.ID+".thumb.jpg")
	defer os.Remove(thumbPath)
	stream := ffmpeg.Input(path).
		Output(thumbPath, ffmpeg.KwArgs{"vframes": 1, "q:v": 2}).
		OverWriteOutput()
	if err := stream.Run(); err != nil {
		return err
	}
	thumbBytes, err := os.ReadFile(thumbPath)
	if err != nil {
		return err
	}
	return s.persistReanalysisVariant(ctx, file, "system.thumbnail", thumbBytes, "image/jpeg", ".thumbnail")
}

func (s *FileService) persistReanalysisVariant(ctx context.Context, parent *database.CloudFile, appType string, body []byte, mimeType string, suffix string) error {
	if s == nil || parent == nil || parent.Object == nil {
		return nil
	}
	backend, err := s.BackendForFile(parent)
	if err != nil {
		return err
	}
	derived, err := s.getDerivedChild(parent.ID, appType)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	key := parent.ID + suffix
	if err := backend.Put(ctx, key, bytes.NewReader(body), mimeType); err != nil {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		obj := &database.FileObject{ID: database.NewID(), Size: int64(len(body)), MimeType: mimeType, Hash: ComputeHash(body), StorageKey: &key, Meta: datatypes.JSON([]byte(`{}`)), HasCompression: false, HasThumbnail: false}
		if err := s.db.Create(obj).Error; err != nil {
			return err
		}
		_, err = s.CreateDerivedFile(parent.AccountID, parent.ID, parent.Name, obj.ID, appType, &key)
		return err
	}
	if derived != nil && derived.ObjectID != nil {
		return s.db.Model(&database.FileObject{}).Where("id = ?", *derived.ObjectID).Updates(map[string]any{"hash": ComputeHash(body), "size": int64(len(body)), "mime_type": mimeType, "updated_at": time.Now()}).Error
	}
	return nil
}

func (s *FileService) ReanalyzeMissingMetadata(ctx context.Context, limit int) (ReanalysisResult, error) {
	if s.stor == nil {
		return ReanalysisResult{}, fmt.Errorf("storage backend not configured")
	}
	if limit <= 0 {
		limit = 100
	}
	candidates, err := s.ListReanalysisCandidates(ctx, limit)
	if err != nil {
		return ReanalysisResult{}, err
	}
	result := ReanalysisResult{Scanned: len(candidates)}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := s.RepairReanalysisCandidate(ctx, candidate.FileID); err != nil {
			result.Failed++
			continue
		}
		result.Updated++
	}
	return result, nil
}

func (s *FileService) ReanalyzeMissingImageMetadata(ctx context.Context, limit int) (ReanalysisResult, error) {
	return s.ReanalyzeMissingMetadata(ctx, limit)
}

func (s *FileService) ReanalyzeFiles(ctx context.Context, fileIDs []string) (ReanalysisResult, error) {
	seen := map[string]struct{}{}
	result := ReanalysisResult{}
	for _, fileID := range fileIDs {
		fileID = strings.TrimSpace(fileID)
		if fileID == "" {
			continue
		}
		if _, ok := seen[fileID]; ok {
			continue
		}
		seen[fileID] = struct{}{}
		result.Scanned++
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := s.RepairReanalysisCandidate(ctx, fileID); err != nil {
			result.Failed++
			continue
		}
		result.Updated++
	}
	return result, nil
}

func (s *FileService) reanalysisReason(file *database.CloudFile) (string, string) {
	if file == nil || file.Object == nil {
		return "", ""
	}
	if strings.HasPrefix(file.Object.MimeType, "image/") {
		return repairReason(file), "image"
	}
	if strings.HasPrefix(file.Object.MimeType, "video/") {
		return s.videoRepairReason(file), "video"
	}
	return "", ""
}

func (s *FileService) videoRepairReason(file *database.CloudFile) string {
	if file == nil || file.Object == nil || !strings.HasPrefix(file.Object.MimeType, "video/") {
		return ""
	}
	reasons := make([]string, 0, 4)
	fileMeta := file.Object.LegacyMeta()
	if len(bytes.TrimSpace(fileMeta)) == 0 || string(bytes.TrimSpace(fileMeta)) == "null" || string(bytes.TrimSpace(fileMeta)) == "{}" {
		reasons = append(reasons, "missing file_meta")
	}
	var meta map[string]any
	if err := json.Unmarshal(fileMeta, &meta); err != nil {
		reasons = append(reasons, "invalid file_meta")
	} else {
		if _, ok := meta["width"]; !ok {
			reasons = append(reasons, "missing width")
		}
		if _, ok := meta["height"]; !ok {
			reasons = append(reasons, "missing height")
		}
		if _, ok := meta["media"]; !ok {
			reasons = append(reasons, "missing media")
		}
	}
	if child, err := s.getDerivedChild(file.ID, "system.thumbnail"); err != nil || child == nil {
		reasons = append(reasons, "missing thumbnail variant")
	}
	if file.Object.Size == 0 {
		reasons = append(reasons, "zero object size")
	}
	if file.StorageKey == nil && file.Object.StorageKey == nil && file.ObjectID != nil {
		reasons = append(reasons, "missing storage key")
	}
	return strings.Join(reasons, ", ")
}

func repairReason(file *database.CloudFile) string {
	if file == nil {
		return ""
	}
	reasons := make([]string, 0, 4)
	fileMeta := datatypes.JSON(nil)
	if file.Object != nil {
		fileMeta = file.Object.LegacyMeta()
	}
	if len(bytes.TrimSpace(fileMeta)) == 0 || string(bytes.TrimSpace(fileMeta)) == "null" || string(bytes.TrimSpace(fileMeta)) == "{}" {
		reasons = append(reasons, "missing file_meta")
	}
	var meta map[string]any
	if err := json.Unmarshal(fileMeta, &meta); err != nil {
		reasons = append(reasons, "invalid file_meta")
	} else {
		if _, ok := meta["width"]; !ok {
			reasons = append(reasons, "missing width")
		}
		if _, ok := meta["height"]; !ok {
			reasons = append(reasons, "missing height")
		}
		if _, ok := meta["blurhash"]; !ok {
			reasons = append(reasons, "missing blurhash")
		}
		if _, ok := meta["exif_version"]; !ok {
			reasons = append(reasons, "missing exif_version")
		}
		if _, ok := meta["exif"]; !ok {
			reasons = append(reasons, "missing exif")
		}
	}
	if file.Object != nil {
		if len(bytes.TrimSpace(file.Object.Meta)) == 0 || string(bytes.TrimSpace(file.Object.Meta)) == "null" || string(bytes.TrimSpace(file.Object.Meta)) == "{}" {
			reasons = append(reasons, "missing object meta")
		}
		if file.Object.Size == 0 {
			reasons = append(reasons, "zero object size")
		}
		if file.StorageKey == nil && file.Object.StorageKey == nil && file.ObjectID != nil {
			reasons = append(reasons, "missing storage key")
		}
		if len(bytes.TrimSpace(file.Object.Meta)) > 0 && string(bytes.TrimSpace(file.Object.Meta)) != "{}" {
			var om map[string]any
			if err := json.Unmarshal(file.Object.Meta, &om); err == nil {
				if _, ok := om["exif_version"]; !ok {
					reasons = append(reasons, "missing object exif_version")
				}
				if _, ok := om["exif"]; !ok {
					reasons = append(reasons, "missing object exif")
				}
			}
		}
	}
	return strings.Join(reasons, ", ")
}

func writeTempObject(r io.Reader) (string, func(), error) {
	file, err := os.CreateTemp("", "dysonfs-reanalyze-*")
	if err != nil {
		return "", func() {}, err
	}
	if _, err := io.Copy(file, r); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", func() {}, err
	}
	return file.Name(), func() { _ = os.Remove(file.Name()) }, nil
}

func (s *FileService) CreateUploadedFile(accountID uuid.UUID, name string, description *string, hash *string, expiredAt *time.Time, usage *string, parentID *string, objectID string, poolID *string, appType *string, storageKey *string, indexed bool) (*database.CloudFile, error) {
	resolvedPoolID := s.resolvedPoolID(poolID)
	finalIndexed := indexed
	if !finalIndexed && parentID != nil && strings.TrimSpace(*parentID) != "" {
		var parent database.CloudFile
		if err := s.db.Select("indexed").First(&parent, "id = ? AND deleted_at IS NULL", strings.TrimSpace(*parentID)).Error; err == nil && parent.Indexed {
			finalIndexed = true
		}
	}
	file := &database.CloudFile{ID: database.NewID(), Name: name, Description: firstNonEmptyPtr(description), AccountID: accountID, PoolID: resolvedPoolID, ObjectID: &objectID, ParentID: firstNonEmptyPtr(parentID), Indexed: finalIndexed, ApplicationType: appType, StorageID: resolvedPoolID, StorageKey: storageKey, UserMeta: datatypes.JSON([]byte(`{}`)), ExpiredAt: expiredAt, Usage: usage}
	if hash != nil && strings.TrimSpace(*hash) != "" {
		file.FileMeta = datatypes.JSON([]byte(fmt.Sprintf(`{"hash":%q}`, strings.TrimSpace(*hash))))
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(file).Error; err != nil {
			return err
		}
		return s.createPrimaryReplica(tx, objectID, resolvedPoolID)
	}); err != nil {
		return nil, err
	}
	return file, nil
}

func (s *FileService) OverwriteFile(fileID string, objectID string, storageKey *string) (*database.CloudFile, error) {
	if s == nil || s.db == nil || s.db.DB == nil {
		return nil, fmt.Errorf("database not configured")
	}
	fileID = strings.TrimSpace(fileID)
	objectID = strings.TrimSpace(objectID)
	if fileID == "" {
		return nil, fmt.Errorf("file id is required")
	}
	if objectID == "" {
		return nil, fmt.Errorf("object id is required")
	}
	if err := s.db.DB.Transaction(func(tx *gorm.DB) error {
		var file database.CloudFile
		if err := tx.Preload("Object").First(&file, "id = ? AND deleted_at IS NULL", fileID).Error; err != nil {
			return err
		}
		if file.IsFolder {
			return fmt.Errorf("cannot overwrite folder")
		}

		var newObject database.FileObject
		if err := tx.First(&newObject, "id = ?", objectID).Error; err != nil {
			return err
		}

		resolvedStorageKey := firstNonEmptyPtr(storageKey, newObject.StorageKey)
		updates := map[string]any{
			"object_id":   objectID,
			"storage_key": nil,
		}
		if resolvedStorageKey != nil {
			updates["storage_key"] = *resolvedStorageKey
		}
		if err := tx.Model(&database.CloudFile{}).Where("id = ?", file.ID).Updates(updates).Error; err != nil {
			return err
		}
		if err := s.createPrimaryReplica(tx, objectID, s.resolvedPoolID(firstNonEmptyPtr(file.PoolID, file.StorageID))); err != nil {
			return err
		}

		var derived []database.CloudFile
		if err := tx.Preload("Object").Where("parent_id = ? AND deleted_at IS NULL", file.ID).Find(&derived).Error; err != nil {
			return err
		}
		for i := range derived {
			child := derived[i]
			if err := tx.Delete(&database.CloudFile{}, "id = ?", child.ID).Error; err != nil {
				return err
			}
			if err := s.touchDerivedParentFlagsTx(tx, &child); err != nil {
				return err
			}
			if child.ObjectID != nil && strings.TrimSpace(*child.ObjectID) != "" {
				if err := s.purgeObjectIfDereferenced(tx, &child, strings.TrimSpace(*child.ObjectID)); err != nil {
					return err
				}
			}
		}

		if file.ObjectID != nil && strings.TrimSpace(*file.ObjectID) != "" && strings.TrimSpace(*file.ObjectID) != objectID {
			if err := s.purgeObjectIfDereferenced(tx, &file, strings.TrimSpace(*file.ObjectID)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return s.GetFile(fileID)
}

func (s *FileService) CanFastOverwrite(fileID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("database not configured")
	}
	file, err := s.GetFile(strings.TrimSpace(fileID))
	if err != nil {
		return false, err
	}
	if file.IsFolder || file.ObjectID == nil || strings.TrimSpace(*file.ObjectID) == "" {
		return false, nil
	}
	var refCount int64
	if err := s.db.Model(&database.CloudFile{}).Where("object_id = ? AND deleted_at IS NULL", strings.TrimSpace(*file.ObjectID)).Count(&refCount).Error; err != nil {
		return false, err
	}
	return refCount == 1, nil
}

func (s *FileService) FastOverwriteFile(fileID, sourcePath string, analysis *SourceAnalysis) (*database.CloudFile, bool, error) {
	if s == nil || s.db == nil || s.db.DB == nil {
		return nil, false, fmt.Errorf("database not configured")
	}
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return nil, false, fmt.Errorf("file id is required")
	}
	if strings.TrimSpace(sourcePath) == "" {
		return nil, false, fmt.Errorf("source path is required")
	}
	fastAllowed, err := s.CanFastOverwrite(fileID)
	if err != nil || !fastAllowed {
		return nil, false, err
	}

	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, false, err
	}
	mimeType := "application/octet-stream"
	if len(data) > 0 {
		mimeType = mimetype.Detect(data).String()
	}
	hash := ComputeHash(data)
	size := int64(len(data))

	var updated *database.CloudFile
	if err := s.db.DB.Transaction(func(tx *gorm.DB) error {
		var file database.CloudFile
		if err := tx.Preload("Object").First(&file, "id = ? AND deleted_at IS NULL", fileID).Error; err != nil {
			return err
		}
		if file.IsFolder {
			return fmt.Errorf("cannot overwrite folder")
		}
		if file.ObjectID == nil || strings.TrimSpace(*file.ObjectID) == "" || file.Object == nil {
			return fmt.Errorf("file object missing")
		}

		objectID := strings.TrimSpace(*file.ObjectID)
		var refCount int64
		if err := tx.Model(&database.CloudFile{}).Where("object_id = ? AND deleted_at IS NULL", objectID).Count(&refCount).Error; err != nil {
			return err
		}
		if refCount != 1 {
			return nil
		}

		storageKey := firstNonEmptyPtr(file.Object.StorageKey, file.StorageKey, file.ObjectID)
		if storageKey == nil || strings.TrimSpace(*storageKey) == "" {
			return fmt.Errorf("storage key missing")
		}
		backend, err := s.BackendForFile(&file)
		if err != nil {
			return err
		}
		if err := backend.Put(context.Background(), *storageKey, bytes.NewReader(data), mimeType); err != nil {
			return err
		}

		var meta datatypes.JSON
		if file.Object != nil {
			meta = file.Object.Meta
		}
		if analysis != nil {
			updates := map[string]any{}
			if analysis.Width > 0 {
				updates["width"] = analysis.Width
			}
			if analysis.Height > 0 {
				updates["height"] = analysis.Height
			}
			if analysis.Image != nil {
				updates["width"] = analysis.Image.Width
				updates["height"] = analysis.Image.Height
				updates["blurhash"] = analysis.Image.Blurhash
				updates["exif_version"] = 2
				if len(analysis.Image.Exif) > 0 {
					updates["exif"] = analysis.Image.Exif
				}
			}
			if len(analysis.Media) > 0 {
				updates["media"] = analysis.Media
			}
			merged, err := mergeJSONMeta(meta, updates)
			if err != nil {
				return err
			}
			meta = merged
		}

		if err := tx.Model(&database.FileObject{}).Where("id = ?", objectID).Updates(map[string]any{
			"size":            size,
			"mime_type":       mimeType,
			"hash":            hash,
			"meta":            meta,
			"has_thumbnail":   false,
			"has_compression": false,
			"updated_at":      time.Now(),
		}).Error; err != nil {
			return err
		}

		var derived []database.CloudFile
		if err := tx.Preload("Object").Where("parent_id = ? AND deleted_at IS NULL", file.ID).Find(&derived).Error; err != nil {
			return err
		}
		for i := range derived {
			child := derived[i]
			if err := tx.Delete(&database.CloudFile{}, "id = ?", child.ID).Error; err != nil {
				return err
			}
			if err := s.touchDerivedParentFlagsTx(tx, &child); err != nil {
				return err
			}
			if child.ObjectID != nil && strings.TrimSpace(*child.ObjectID) != "" {
				if err := s.purgeObjectIfDereferenced(tx, &child, strings.TrimSpace(*child.ObjectID)); err != nil {
					return err
				}
			}
		}
		updated = &file
		return nil
	}); err != nil {
		return nil, false, err
	}
	if updated == nil {
		return nil, false, nil
	}
	result, err := s.GetFile(fileID)
	if err != nil {
		return nil, false, err
	}
	return result, true, nil
}

func (s *FileService) CreateDerivedFile(accountID uuid.UUID, parentID string, name string, objectID string, appType string, storageKey *string) (*database.CloudFile, error) {
	pt := parentID
	typeName := appType
	var file *database.CloudFile
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var parent database.CloudFile
		if err := tx.Select("pool_id", "storage_id").First(&parent, "id = ?", parentID).Error; err != nil {
			return err
		}
		resolvedPoolID := s.resolvedPoolID(parent.PoolID)
		file = &database.CloudFile{ID: database.NewID(), Name: name, AccountID: accountID, PoolID: resolvedPoolID, ObjectID: &objectID, ParentID: &pt, Indexed: false, ApplicationType: &typeName, StorageID: resolvedPoolID, StorageKey: storageKey, UserMeta: datatypes.JSON([]byte(`{}`))}
		if err := tx.Create(file).Error; err != nil {
			return err
		}
		return s.createPrimaryReplica(tx, objectID, resolvedPoolID)
	}); err != nil {
		return nil, err
	}
	return file, nil
}

type missingReplicaCandidate struct {
	ObjectID         string    `gorm:"column:object_id"`
	ObjectStorageKey *string   `gorm:"column:object_storage_key"`
	CreatedAt        time.Time `gorm:"column:created_at"`
}

func (s *FileService) PreviewMissingReplicas(ctx context.Context, limit int) ([]ReplicaRepairPreview, ReplicaRepairSummary, error) {
	return s.repairMissingReplicas(ctx, limit, false)
}

func (s *FileService) RepairMissingReplicas(ctx context.Context, limit int) ([]ReplicaRepairPreview, ReplicaRepairSummary, error) {
	return s.repairMissingReplicas(ctx, limit, true)
}

func (s *FileService) repairMissingReplicas(ctx context.Context, limit int, apply bool) ([]ReplicaRepairPreview, ReplicaRepairSummary, error) {
	summary := ReplicaRepairSummary{}
	if s == nil || s.db == nil {
		return nil, summary, fmt.Errorf("database not configured")
	}
	if s.stor == nil {
		return nil, summary, fmt.Errorf("storage backend not configured")
	}
	candidates, err := s.loadMissingReplicaCandidates(limit)
	if err != nil {
		return nil, summary, err
	}
	summary.Scanned = len(candidates)
	previews := make([]ReplicaRepairPreview, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return previews, summary, err
		}
		preview, created, evalErr := s.evaluateMissingReplicaCandidate(ctx, candidate, apply)
		if preview.Status != "" {
			summary.Candidates++
			previews = append(previews, preview)
		}
		switch preview.Status {
		case "verified":
			summary.Verified++
			if created {
				summary.Created++
			}
		case "already-present":
			summary.AlreadyPresent++
		case "missing-pool":
			summary.MissingPool++
		case "missing-key":
			summary.MissingKey++
		case "missing-remote":
			summary.MissingRemote++
		}
		if evalErr != nil {
			summary.Failed++
			return previews, summary, evalErr
		}
	}
	return previews, summary, nil
}

func (s *FileService) loadMissingReplicaCandidates(limit int) ([]missingReplicaCandidate, error) {
	query := s.db.Model(&database.FileObject{}).
		Select("file_objects.id AS object_id, file_objects.storage_key AS object_storage_key, file_objects.created_at").
		Joins("LEFT JOIN file_replicas ON file_replicas.object_id = file_objects.id AND file_replicas.deleted_at IS NULL").
		Where("file_objects.deleted_at IS NULL").
		Where("file_replicas.id IS NULL").
		Where("EXISTS (SELECT 1 FROM cloud_files WHERE cloud_files.object_id = file_objects.id AND cloud_files.deleted_at IS NULL)").
		Order("file_objects.created_at asc")
	if limit > 0 {
		query = query.Limit(limit)
	}
	var rows []missingReplicaCandidate
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *FileService) evaluateMissingReplicaCandidate(ctx context.Context, candidate missingReplicaCandidate, apply bool) (ReplicaRepairPreview, bool, error) {
	preview := ReplicaRepairPreview{ObjectID: candidate.ObjectID}
	file, err := s.firstFileForObject(candidate.ObjectID)
	if err != nil {
		return preview, false, err
	}
	preview.FileID = file.ID
	poolID := s.resolvedPoolID(firstNonEmptyPtr(file.PoolID, file.StorageID))
	if poolID == nil {
		preview.Status = "missing-pool"
		preview.Detail = "no pool mapping available for object"
		return preview, false, nil
	}
	preview.PoolID = *poolID
	storageKey := firstNonEmptyString(candidate.ObjectStorageKey, file.StorageKey)
	if storageKey == "" {
		storageKey = candidate.ObjectID
	}
	if strings.TrimSpace(storageKey) == "" {
		preview.Status = "missing-key"
		preview.Detail = "no storage key available for object"
		return preview, false, nil
	}
	preview.StorageKey = storageKey
	backend, err := s.BackendForPoolID(poolID)
	if err != nil {
		return preview, false, err
	}
	if _, err := backend.Stat(ctx, storageKey); err != nil {
		preview.Status = "missing-remote"
		preview.Detail = err.Error()
		return preview, false, nil
	}
	preview.Status = "verified"
	preview.Detail = "remote object exists"
	if !apply {
		return preview, false, nil
	}
	created, err := s.insertReplicaIfMissing(candidate.ObjectID, poolID)
	if err != nil {
		return preview, false, err
	}
	if !created {
		preview.Status = "already-present"
		preview.Detail = "replica was created by another process"
		return preview, false, nil
	}
	return preview, true, nil
}

func (s *FileService) firstFileForObject(objectID string) (*database.CloudFile, error) {
	var file database.CloudFile
	if err := s.db.Where("object_id = ? AND deleted_at IS NULL", objectID).Order("created_at asc").First(&file).Error; err != nil {
		return nil, err
	}
	return &file, nil
}

func (s *FileService) insertReplicaIfMissing(objectID string, poolID *string) (bool, error) {
	created := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&database.FileReplica{}).Where("object_id = ? AND deleted_at IS NULL", objectID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		if err := s.createPrimaryReplica(tx, objectID, poolID); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

func firstNonEmptyPtr(values ...*string) *string {
	for _, value := range values {
		if value != nil && strings.TrimSpace(*value) != "" {
			resolved := strings.TrimSpace(*value)
			return &resolved
		}
	}
	return nil
}

func firstNonEmptyString(values ...*string) string {
	for _, value := range values {
		if value != nil && strings.TrimSpace(*value) != "" {
			return strings.TrimSpace(*value)
		}
	}
	return ""
}

type TaskService struct{ db *database.DB }

func NewTaskService(db *database.DB) *TaskService { return &TaskService{db: db} }

func (s *TaskService) DB() *database.DB { return s.db }

func (s *TaskService) CreateUploadTask(accountID uuid.UUID, name string, payload *database.PersistentTask, size int64, poolID *string, fileName string, contentType string, chunkSize int64, chunksCount int) (*database.PersistentTask, error) {
	task := &database.PersistentTask{ID: database.NewID(), TaskID: database.NewID(), Name: name, Type: "file.upload", Status: "pending", AccountID: accountID, Progress: 0, LastActivity: time.Now(), FileName: &fileName, FileSize: &size, PoolID: poolID, ChunkSize: chunkSize, ChunksCount: chunksCount, UploadedChunks: datatypes.JSON([]byte(`[]`))}
	if payload != nil {
		task.Description = payload.Description
		task.Hash = payload.Hash
		task.ExpiredAt = payload.ExpiredAt
		task.Usage = payload.Usage
		task.ApplicationType = payload.ApplicationType
		task.ParentID = payload.ParentID
		task.OverwriteID = payload.OverwriteID
		task.FastMode = payload.FastMode
		task.Indexed = payload.Indexed
	}
	if err := s.db.Create(task).Error; err != nil {
		return nil, err
	}
	return task, nil
}

func (s *TaskService) GetUploadTask(taskID string) (*database.PersistentTask, error) {
	var task database.PersistentTask
	if err := s.db.First(&task, "task_id = ?", taskID).Error; err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *TaskService) GetUploadTaskByTaskID(taskID string) (*database.PersistentTask, error) {
	return s.GetUploadTask(taskID)
}

func (s *TaskService) ListTasks(accountID uuid.UUID) ([]database.PersistentTask, error) {
	var tasks []database.PersistentTask
	if err := s.db.Where("account_id = ?", accountID).Order("last_activity desc").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *TaskService) ListRecentTasks(accountID uuid.UUID, limit int) ([]database.PersistentTask, error) {
	var tasks []database.PersistentTask
	if err := s.db.Where("account_id = ?", accountID).Order("last_activity desc").Limit(limit).Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *TaskService) FailTask(taskID string, msg string) error {
	return s.db.Model(&database.PersistentTask{}).Where("task_id = ?", taskID).Updates(map[string]any{"status": "failed", "error_message": msg, "updated_at": time.Now(), "last_activity": time.Now()}).Error
}

func (s *TaskService) CompleteTask(taskID string) error {
	return s.db.Model(&database.PersistentTask{}).Where("task_id = ?", taskID).Updates(map[string]any{"status": "completed", "progress": 1.0, "updated_at": time.Now(), "last_activity": time.Now()}).Error
}

func (s *TaskService) Progress(taskID string, progress float64) error {
	return s.db.Model(&database.PersistentTask{}).Where("task_id = ?", taskID).Updates(map[string]any{"progress": progress, "updated_at": time.Now(), "last_activity": time.Now()}).Error
}

func (s *TaskService) UpdateUploadedChunk(taskID string, idx int) error {
	var task database.PersistentTask
	if err := s.db.First(&task, "task_id = ?", taskID).Error; err != nil {
		return err
	}
	var chunks []int
	if len(task.UploadedChunks) > 0 {
		_ = json.Unmarshal(task.UploadedChunks, &chunks)
	}
	for _, existing := range chunks {
		if existing == idx {
			return nil
		}
	}
	chunks = append(chunks, idx)
	raw, _ := json.Marshal(chunks)
	return s.db.Model(&database.PersistentTask{}).Where("task_id = ?", taskID).Updates(map[string]any{"uploaded_chunks": datatypes.JSON(raw), "chunks_uploaded": len(chunks), "updated_at": time.Now(), "last_activity": time.Now()}).Error
}

func (s *TaskService) IsChunkUploaded(taskID string, idx int) (bool, error) {
	var task database.PersistentTask
	if err := s.db.First(&task, "task_id = ?", taskID).Error; err != nil {
		return false, err
	}
	var chunks []int
	if len(task.UploadedChunks) > 0 {
		_ = json.Unmarshal(task.UploadedChunks, &chunks)
	}
	for _, existing := range chunks {
		if existing == idx {
			return true, nil
		}
	}
	return false, nil
}

func (s *TaskService) ResetPending(taskID string) error {
	return s.db.Model(&database.PersistentTask{}).Where("task_id = ?", taskID).Updates(map[string]any{"status": "pending", "progress": 0.0, "updated_at": time.Now(), "last_activity": time.Now()}).Error
}

func (s *TaskService) MarkCompleted(taskID string) error {
	return s.db.Model(&database.PersistentTask{}).Where("task_id = ?", taskID).Updates(map[string]any{"status": "completed", "progress": 1.0, "updated_at": time.Now(), "last_activity": time.Now()}).Error
}

func (s *TaskService) CleanupOld(accountID uuid.UUID) (int64, error) {
	tx := s.db.Where("account_id = ? and status in ?", accountID, []string{"completed", "failed", "cancelled", "expired"}).Delete(&database.PersistentTask{})
	return tx.RowsAffected, tx.Error
}

type QuotaService struct {
	db            *database.DB
	cache         sharedcache.CacheService
	profileClient gen.DyProfileServiceClient
	levelingCfg   config.LevelingQuotaConfig
}

func NewQuotaService(db *database.DB) *QuotaService { return &QuotaService{db: db} }

func (s *QuotaService) SetCache(cache sharedcache.CacheService) {
	s.cache = cache
}

func (s *QuotaService) SetProfileClient(client gen.DyProfileServiceClient) {
	s.profileClient = client
}

func (s *QuotaService) SetLevelingConfig(cfg config.LevelingQuotaConfig) {
	s.levelingCfg = cfg
}

func (s *QuotaService) EnrichedAccount(ctx context.Context, account *gen.DyAccount) (*gen.DyAccount, error) {
	return s.enrichedAccount(ctx, account)
}

func NewProfileClient(cfg config.PassportConfig) (gen.DyProfileServiceClient, *grpc.ClientConn, error) {
	target, useTLS := dyauth.NormalizeAuthGRPCTarget(cfg.Target, cfg.UseTLS)
	if strings.TrimSpace(target) == "" {
		return nil, nil, errors.New("profile gRPC target is empty")
	}
	var transportCredentials credentials.TransportCredentials
	if useTLS {
		transportCredentials = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.TLSSkipVerify})
	} else {
		transportCredentials = insecure.NewCredentials()
	}
	conn, err := grpc.Dial(target, grpc.WithTransportCredentials(transportCredentials))
	if err != nil {
		return nil, nil, fmt.Errorf("dial profile service: %w", err)
	}
	return gen.NewDyProfileServiceClient(conn), conn, nil
}

type QuotaSummary struct {
	BasedQuota    int64 `json:"based_quota"`
	LevelingQuota int64 `json:"leveling_quota"`
	PerkQuota     int64 `json:"perk_quota"`
	ExtraQuota    int64 `json:"extra_quota"`
	TotalQuota    int64 `json:"total_quota"`
}

type UsageSummary struct {
	UsedQuota       int64 `json:"used_quota"`
	TotalQuota      int64 `json:"total_quota"`
	TotalFileCount  int64 `json:"total_file_count"`
	TotalUsageBytes int64 `json:"total_usage_bytes"`
}

var ErrQuotaExceeded = errors.New("quota exceeded")

const quotaUnitBytes int64 = 1024 * 1024

func (s *QuotaService) CheckUploadQuota(account *gen.DyAccount, size int64, costMultiplier float64) error {
	if account == nil {
		return fmt.Errorf("account is required")
	}
	account, err := s.enrichedAccount(context.Background(), account)
	if err != nil {
		return err
	}
	summary, err := s.GetSummary(account)
	if err != nil {
		return err
	}
	usedMB, err := s.billableUsage(account.GetId())
	if err != nil {
		return err
	}
	if costMultiplier <= 0 {
		costMultiplier = 1
	}
	billableUnit := int64(math.Ceil(float64(size) * costMultiplier / float64(quotaUnitBytes)))
	if usedMB+billableUnit <= summary.TotalQuota {
		return nil
	}
	remainingMB := summary.TotalQuota - usedMB
	if remainingMB < 0 {
		remainingMB = 0
	}
	return fmt.Errorf("%w: used=%dMB total=%dMB remaining=%dMB", ErrQuotaExceeded, usedMB, summary.TotalQuota, remainingMB)
}

func (s *QuotaService) ListRecords(accountID uuid.UUID) ([]database.QuotaRecord, error) {
	var records []database.QuotaRecord
	if err := s.db.Where("account_id = ?", accountID).Order("created_at desc").Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

func (s *QuotaService) GetSummary(account *gen.DyAccount) (QuotaSummary, error) {
	if account == nil {
		return QuotaSummary{}, fmt.Errorf("account is required")
	}
	account, err := s.enrichedAccount(context.Background(), account)
	if err != nil {
		return QuotaSummary{}, err
	}
	var records []database.QuotaRecord
	if err := s.db.Where("account_id = ?", account.GetId()).Order("created_at asc").Find(&records).Error; err != nil {
		return QuotaSummary{}, err
	}
	var extraQuota int64
	now := time.Now()
	for _, record := range records {
		if record.ExpiredAt != nil && record.ExpiredAt.Before(now) {
			continue
		}
		extraQuota += record.Quota
	}
	levelingQuota := levelingQuotaFromAccount(account, s.levelingCfg)
	perkQuota := perkQuotaFromAccount(account)
	basedQuota := levelingQuota + perkQuota
	return QuotaSummary{BasedQuota: basedQuota, LevelingQuota: levelingQuota, PerkQuota: perkQuota, ExtraQuota: extraQuota, TotalQuota: basedQuota + extraQuota}, nil
}

func baseQuotaFromAccount(account *gen.DyAccount) int64 {
	return levelingQuotaFromAccount(account, config.LevelingQuotaConfig{}) + perkQuotaFromAccount(account)
}

func levelingQuotaFromAccount(account *gen.DyAccount, cfg config.LevelingQuotaConfig) int64 {
	level := int64(0)
	if account != nil && account.GetProfile() != nil {
		level = int64(account.GetProfile().GetLevel())
	}
	if level < 0 {
		level = 0
	}
	progressLevel := level
	if progressLevel > 100 {
		progressLevel = 100
	}
	level1 := cfg.Level1
	if level1 <= 0 {
		level1 = 512
	}
	level10 := cfg.Level10
	if level10 <= 0 {
		level10 = 1024
	}
	level60 := cfg.Level60
	if level60 <= 0 {
		level60 = 5 * 1024
	}
	level120 := cfg.Level120
	if level120 <= 0 {
		level120 = 10 * 1024
	}
	progressQuota := progressLevel * level120 / 100
	milestoneQuota := level1
	switch {
	case level >= 120:
		milestoneQuota = level120
	case level >= 60:
		milestoneQuota = level60
	case level >= 10:
		milestoneQuota = level10
	}
	if progressQuota < milestoneQuota {
		return milestoneQuota
	}
	return progressQuota
}

func perkQuotaFromAccount(account *gen.DyAccount) int64 {
	perkLevel := int32(0)
	if account != nil {
		if sub := account.GetPerkSubscription(); sub != nil {
			perkLevel = sub.GetPerkLevel()
		} else {
			perkLevel = account.GetPerkLevel()
		}
	}
	switch perkLevel {
	case 1:
		return 10 * 1024
	case 2:
		return 25 * 1024
	case 3:
		return 50 * 1024
	default:
		return 0
	}
}

func (s *QuotaService) GetUsage(account *gen.DyAccount) (UsageSummary, error) {
	if account == nil {
		return UsageSummary{}, fmt.Errorf("account is required")
	}
	account, err := s.enrichedAccount(context.Background(), account)
	if err != nil {
		return UsageSummary{}, err
	}
	usedMB, fileCount, usageBytes, err := s.usageStats(account.GetId())
	if err != nil {
		return UsageSummary{}, err
	}
	summary, err := s.GetSummary(account)
	if err != nil {
		return UsageSummary{}, err
	}
	return UsageSummary{UsedQuota: usedMB, TotalQuota: summary.TotalQuota, TotalFileCount: fileCount, TotalUsageBytes: usageBytes}, nil
}

func (s *QuotaService) enrichedAccount(ctx context.Context, account *gen.DyAccount) (*gen.DyAccount, error) {
	if account == nil {
		return nil, fmt.Errorf("account is required")
	}
	if s.profileClient == nil {
		return account, nil
	}
	key := fmt.Sprintf("quota:account:%s", account.GetId())
	if s.cache != nil {
		var cached gen.DyAccount
		if ok, err := s.cache.GetData(ctx, key, &cached, "DyAccount"); err == nil && ok {
			return &cached, nil
		}
	}
	resolved, err := s.profileClient.GetAccount(ctx, &gen.DyGetAccountRequest{Id: account.GetId()})
	if err != nil {
		return nil, err
	}
	if s.cache != nil {
		_ = s.cache.SetData(ctx, key, resolved, "DyAccount", 5*time.Minute)
	}
	return resolved, nil
}

func (s *QuotaService) usageStats(accountID string) (int64, int64, int64, error) {
	var files []database.CloudFile
	if err := s.db.Preload("Object").Where("account_id = ? AND deleted_at IS NULL", accountID).Find(&files).Error; err != nil {
		return 0, 0, 0, err
	}
	poolMultipliers := map[string]float64{}
	var total int64
	var fileCount int64
	var usageBytes int64
	for _, file := range files {
		if file.Object == nil || file.Object.Size <= 0 {
			continue
		}
		fileCount++
		usageBytes += file.Object.Size
		multiplier := 1.0
		if file.PoolID != nil && strings.TrimSpace(*file.PoolID) != "" {
			if cached, ok := poolMultipliers[*file.PoolID]; ok {
				multiplier = cached
			} else {
				multiplier = 1.0
				var pool database.FilePool
				if err := s.db.First(&pool, "id = ?", *file.PoolID).Error; err == nil {
					var billing PoolBillingConfig
					_ = json.Unmarshal(pool.BillingConfig, &billing)
					if billing.CostMultiplier != nil && *billing.CostMultiplier > 0 {
						multiplier = *billing.CostMultiplier
					}
				}
				poolMultipliers[*file.PoolID] = multiplier
			}
		}
		total += int64(math.Ceil(float64(file.Object.Size) * multiplier / float64(quotaUnitBytes)))
	}
	return total, fileCount, usageBytes, nil
}

func (s *QuotaService) billableUsage(accountID string) (int64, error) {
	total, _, _, err := s.usageStats(accountID)
	return total, err
}

func (s *QuotaService) GetPoolUsage(accountID uuid.UUID, poolID string) (map[string]any, error) {
	usage, err := s.billableUsageForPool(accountID, poolID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"pool_id": poolID, "total_quota": usage}, nil
}

func (s *QuotaService) billableUsageForPool(accountID uuid.UUID, poolID string) (int64, error) {
	var files []database.CloudFile
	if err := s.db.Preload("Object").Where("account_id = ? AND pool_id = ? AND deleted_at IS NULL", accountID, poolID).Find(&files).Error; err != nil {
		return 0, err
	}
	var multiplier float64 = 1
	var pool database.FilePool
	if err := s.db.First(&pool, "id = ?", poolID).Error; err == nil {
		var billing PoolBillingConfig
		_ = json.Unmarshal(pool.BillingConfig, &billing)
		if billing.CostMultiplier != nil && *billing.CostMultiplier > 0 {
			multiplier = *billing.CostMultiplier
		}
	}
	var total int64
	for _, file := range files {
		if file.Object == nil || file.Object.Size <= 0 {
			continue
		}
		total += int64(math.Ceil(float64(file.Object.Size) * multiplier / float64(quotaUnitBytes)))
	}
	return total, nil
}

func ErrNotImplemented() error { return errors.New("not implemented") }

func Check(err error) error {
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	return nil
}

func DetectMimeType(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "application/octet-stream", nil
	}
	return mimetype.Detect(data).String(), nil
}

func CopyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func CopyStreamToChunk(tempDir, taskID string, idx int, reader io.Reader) (string, error) {
	chunkDir := filepath.Join(tempDir, taskID)
	if err := os.MkdirAll(chunkDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(chunkDir, fmt.Sprintf("%d.chunk", idx))
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, reader); err != nil {
		return "", err
	}
	return path, nil
}

func IsImageMime(mimeType string) bool { return strings.HasPrefix(mimeType, "image/") }
func IsVideoMime(mimeType string) bool { return strings.HasPrefix(mimeType, "video/") }

func DerivativeKind(mimeType, suffix string) string {
	if suffix == "thumbnail" {
		return "system.thumbnail"
	}
	if strings.HasPrefix(suffix, "compressed") {
		return "system.compression"
	}
	return "system.generated"
}

func PublishFileUploaded(ctx context.Context, bus *eventbus.Bus, evt eventbus.FileUploadedEvent) error {
	if bus == nil || bus.Conn == nil {
		return nil
	}
	_, err := bus.Conn.JetStream()
	_ = ctx
	return err
}
