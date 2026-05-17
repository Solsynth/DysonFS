package service

import (
	"context"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	blurhash "github.com/bbrks/go-blurhash"
	"github.com/davidbyttow/govips/v2/vips"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	gen "src.solsynth.dev/sosys/go/proto"
	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/tiff"

	"github.com/gabriel-vasile/mimetype"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
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
	EnableSigned bool   `json:"enable_signed"`
	EnableSsl    bool   `json:"enable_ssl"`
	Endpoint     string `json:"endpoint"`
	AccessEndpoint *string `json:"access_endpoint"`
	Bucket       string `json:"bucket"`
	ImageProxy   *string `json:"image_proxy"`
	AccessProxy  *string `json:"access_proxy"`
	SecretId     string `json:"secret_id"`
	SecretKey    string `json:"secret_key"`
}

type Pool struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	AccountID     uuid.UUID         `json:"account_id"`
	StorageConfig PoolStorageConfig  `json:"storage_config"`
	BillingConfig PoolBillingConfig  `json:"billing_config"`
	PolicyConfig  PoolConfig        `json:"policy_config"`
	IsHidden      bool              `json:"is_hidden"`
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
	db   *database.DB
	stor storage.Backend
}

const systemPoolName = "system"

func NewFileService(db *database.DB, stor storage.Backend) *FileService {
	return &FileService{db: db, stor: stor}
}

func (s *FileService) DB() *database.DB { return s.db }

func (s *FileService) Storage() storage.Backend { return s.stor }

func (s *FileService) SetStorage(stor storage.Backend) { s.stor = stor }

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
	return defaultPoolID, nil
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
	return &file, nil
}

func (s *FileService) GetChildren(parentID string) ([]database.CloudFile, error) {
	var files []database.CloudFile
	if err := s.db.Preload("Object").Where("parent_id = ?", parentID).Where("deleted_at IS NULL").Find(&files).Error; err != nil {
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
	return s.db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("file_id = ?", fileID).Delete(&database.FilePermission{}).Error; err != nil {
			return err
		}
		if len(perms) == 0 {
			return nil
		}
		return tx.Create(&perms).Error
	})
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
	var perms []database.FilePermission
	if err := s.db.Where("file_id = ? AND permission = ?", file.ID, permission).Find(&perms).Error; err != nil {
		return false
	}
	if len(perms) == 0 {
		return true
	}
	for _, perm := range perms {
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
	return files, nil
}

func (s *FileService) ListOwned(accountID uuid.UUID) ([]database.CloudFile, error) {
	var files []database.CloudFile
	if err := s.db.Preload("Object").Where("account_id = ?", accountID).Find(&files).Error; err != nil {
		return nil, err
	}
	return files, nil
}

func (s *FileService) ListUnindexed(accountID uuid.UUID) ([]database.CloudFile, error) {
	var files []database.CloudFile
	if err := s.db.Preload("Object").Where("account_id = ? AND indexed = false", accountID).Find(&files).Error; err != nil {
		return nil, err
	}
	return files, nil
}

func (s *FileService) CreateFolder(accountID uuid.UUID, name string, parentID *string) (*database.CloudFile, error) {
	file := &database.CloudFile{ID: database.NewID(), Name: name, AccountID: accountID, Indexed: true, IsFolder: true, ParentID: parentID}
	if err := s.db.Create(file).Error; err != nil {
		return nil, err
	}
	return file, nil
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
	return s.db.Delete(&database.CloudFile{}, "id = ?", id).Error
}

func (s *FileService) RecycleFile(id string) error {
	return s.db.Model(&database.CloudFile{}).Where("id = ?", id).Update("is_marked_recycle", true).Error
}

func (s *FileService) RecycleBatch(ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx := s.db.Model(&database.CloudFile{}).Where("id IN ?", ids).Update("is_marked_recycle", true)
	return tx.RowsAffected, tx.Error
}

func (s *FileService) RestoreFile(id string) error {
	return s.db.Model(&database.CloudFile{}).Where("id = ?", id).Update("is_marked_recycle", false).Error
}

func (s *FileService) PurgeFile(id string) error {
	if err := s.db.Unscoped().Delete(&database.CloudFile{}, "id = ?", id).Error; err != nil {
		return err
	}
	if err := s.db.Where("file_id = ?", id).Delete(&database.FilePermission{}).Error; err != nil {
		return err
	}
	return nil
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

func (s *FileService) UpdateUploadedAt(fileID string) error {
	return s.db.Model(&database.CloudFile{}).Where("id = ?", fileID).Update("uploaded_at", time.Now()).Error
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
	object := &database.FileObject{ID: database.NewID(), Size: int64(len(data)), MimeType: mimeType, Hash: "", Meta: datatypes.JSON([]byte(`{}`)), HasCompression: false, HasThumbnail: false}
	if err := s.db.Create(object).Error; err != nil {
		return nil, err
	}
	return object, nil
}

type ImageAnalysis struct {
	Width    int
	Height   int
	Blurhash string
	Exif     map[string]any
}

type SourceAnalysis struct {
	Image *ImageAnalysis
	Media map[string]any
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
		analysis.Image = img
		return analysis, nil
	}
	if strings.HasPrefix(mimeType, "video/") || strings.HasPrefix(mimeType, "audio/") {
		media, err := probeMedia(ctx, path)
		if err != nil {
			return nil, err
		}
		analysis.Media = media
		return analysis, nil
	}
	return analysis, nil
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
		updates := map[string]any{
		}
		if analysis.Image != nil {
			updates["width"] = analysis.Image.Width
			updates["height"] = analysis.Image.Height
			updates["blurhash"] = analysis.Image.Blurhash
			if len(analysis.Image.Exif) > 0 {
				updates["exif"] = analysis.Image.Exif
			}
		}
		if len(analysis.Media) > 0 {
			updates["media"] = analysis.Media
		}
		mergedFileMeta, err := mergeJSONMeta(file.FileMeta, updates)
		if err != nil {
			return err
		}
		if err := tx.Model(&database.CloudFile{}).Where("id = ?", fileID).Update("file_meta", mergedFileMeta).Error; err != nil {
			return err
		}
		if file.ObjectID != nil {
			var object database.FileObject
			if err := tx.First(&object, "id = ?", *file.ObjectID).Error; err != nil {
				return err
			}
			mergedObjectMeta, err := mergeJSONMeta(object.Meta, updates)
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
	StorageKey string    `json:"storage_key,omitempty"`
	ObjectID   string    `json:"object_id,omitempty"`
	Reason     string    `json:"reason"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s *FileService) ListImageReanalysisCandidates(ctx context.Context, limit int) ([]ReanalysisCandidate, error) {
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
		if file.Object == nil || !strings.HasPrefix(file.Object.MimeType, "image/") {
			continue
		}
		reason := repairReason(file)
		if reason == "" {
			continue
		}
		candidate := ReanalysisCandidate{FileID: file.ID, Name: file.Name, MimeType: file.Object.MimeType, Size: file.Object.Size, UpdatedAt: file.UpdatedAt, Reason: reason}
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

func (s *FileService) RepairImageMetadataCandidate(ctx context.Context, fileID string) error {
	if s.stor == nil {
		return fmt.Errorf("storage backend not configured")
	}
	var file database.CloudFile
	if err := s.db.Preload("Object").First(&file, "id = ?", fileID).Error; err != nil {
		return err
	}
	if file.Object == nil || !strings.HasPrefix(file.Object.MimeType, "image/") {
		return nil
	}
	storageKey := file.StorageKey
	if storageKey == nil && file.Object.StorageKey != nil {
		storageKey = file.Object.StorageKey
	}
	if storageKey == nil && file.ObjectID != nil {
		storageKey = file.ObjectID
	}
	if storageKey == nil || strings.TrimSpace(*storageKey) == "" {
		return fmt.Errorf("storage key missing")
	}
	info, err := s.stor.Stat(ctx, *storageKey)
	if err != nil {
		return err
	}
	rc, _, err := s.stor.Get(ctx, *storageKey)
	if err != nil {
		return err
	}
	path, cleanup, err := writeTempObject(rc)
	_ = rc.Close()
	if err != nil {
		return err
	}
	defer cleanup()
	analysis, err := s.AnalyzeImage(path)
	if err != nil {
		return err
	}
	if _, err := s.StoreImageAnalysis(file.ID, analysis); err != nil {
		return err
	}
	if err := s.db.Model(&database.FileObject{}).Where("id = ?", file.Object.ID).Update("size", info.Size).Error; err != nil {
		return err
	}
	if file.StorageKey == nil && storageKey != nil {
		_ = s.db.Model(&database.CloudFile{}).Where("id = ?", file.ID).Update("storage_key", *storageKey).Error
	}
	if file.Object.StorageKey == nil && storageKey != nil {
		_ = s.db.Model(&database.FileObject{}).Where("id = ?", file.Object.ID).Update("storage_key", *storageKey).Error
	}
	return nil
}

func (s *FileService) ReanalyzeMissingImageMetadata(ctx context.Context, limit int) (ReanalysisResult, error) {
	if s.stor == nil {
		return ReanalysisResult{}, fmt.Errorf("storage backend not configured")
	}
	if limit <= 0 {
		limit = 100
	}
	candidates, err := s.ListImageReanalysisCandidates(ctx, limit)
	if err != nil {
		return ReanalysisResult{}, err
	}
	result := ReanalysisResult{Scanned: len(candidates)}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := s.RepairImageMetadataCandidate(ctx, candidate.FileID); err != nil {
			result.Failed++
			continue
		}
		result.Updated++
	}
	return result, nil
}

func repairReason(file *database.CloudFile) string {
	if file == nil {
		return ""
	}
	reasons := make([]string, 0, 4)
	if len(bytes.TrimSpace(file.FileMeta)) == 0 || string(bytes.TrimSpace(file.FileMeta)) == "null" || string(bytes.TrimSpace(file.FileMeta)) == "{}" {
		reasons = append(reasons, "missing file_meta")
	}
	var meta map[string]any
	if err := json.Unmarshal(file.FileMeta, &meta); err != nil {
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

func (s *FileService) CreateUploadedFile(accountID uuid.UUID, name string, objectID string, poolID *string, appType *string, storageKey *string) (*database.CloudFile, error) {
	_ = poolID
	file := &database.CloudFile{ID: database.NewID(), Name: name, AccountID: accountID, ObjectID: &objectID, Indexed: true, ApplicationType: appType, StorageKey: storageKey, FileMeta: datatypes.JSON([]byte(`{}`)), UserMeta: datatypes.JSON([]byte(`{}`))}
	if err := s.db.Create(file).Error; err != nil {
		return nil, err
	}
	return file, nil
}

func (s *FileService) CreateDerivedFile(accountID uuid.UUID, parentID string, name string, objectID string, appType string, storageKey *string) (*database.CloudFile, error) {
	pt := parentID
	typeName := appType
	file := &database.CloudFile{ID: database.NewID(), Name: name, AccountID: accountID, ObjectID: &objectID, ParentID: &pt, Indexed: false, ApplicationType: &typeName, StorageKey: storageKey, FileMeta: datatypes.JSON([]byte(`{}`)), UserMeta: datatypes.JSON([]byte(`{}`))}
	if err := s.db.Create(file).Error; err != nil {
		return nil, err
	}
	return file, nil
}

type TaskService struct{ db *database.DB }

func NewTaskService(db *database.DB) *TaskService { return &TaskService{db: db} }

func (s *TaskService) DB() *database.DB { return s.db }

func (s *TaskService) CreateUploadTask(accountID uuid.UUID, name string, size int64, poolID *string, fileName string, contentType string, chunkSize int64, chunksCount int) (*database.PersistentTask, error) {
	task := &database.PersistentTask{ID: database.NewID(), TaskID: database.NewID(), Name: name, Type: "file.upload", Status: "pending", AccountID: accountID, Progress: 0, LastActivity: time.Now(), FileName: &fileName, FileSize: &size, PoolID: poolID, ChunkSize: chunkSize, ChunksCount: chunksCount, UploadedChunks: datatypes.JSON([]byte(`[]`))}
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

type QuotaService struct{ db *database.DB }

func NewQuotaService(db *database.DB) *QuotaService { return &QuotaService{db: db} }

type QuotaSummary struct {
	BasedQuota int64 `json:"based_quota"`
	ExtraQuota  int64 `json:"extra_quota"`
	TotalQuota  int64 `json:"total_quota"`
}

func (s *QuotaService) ListRecords(accountID uuid.UUID) ([]database.QuotaRecord, error) {
	var records []database.QuotaRecord
	if err := s.db.Where("account_id = ?", accountID).Order("created_at desc").Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

func (s *QuotaService) GetSummary(accountID uuid.UUID) (QuotaSummary, error) {
	var records []database.QuotaRecord
	if err := s.db.Where("account_id = ?", accountID).Find(&records).Error; err != nil {
		return QuotaSummary{}, err
	}
	var total int64
	for _, record := range records {
		total += record.Quota
	}
	return QuotaSummary{BasedQuota: total, ExtraQuota: 0, TotalQuota: total}, nil
}

func (s *QuotaService) GetUsage(accountID uuid.UUID) (QuotaSummary, error) {
	return s.GetSummary(accountID)
}

func (s *QuotaService) GetPoolUsage(accountID uuid.UUID, poolID string) (map[string]any, error) {
	_ = accountID
	var total int64
	if err := s.db.Model(&database.CloudFile{}).Where("pool_id = ?", poolID).Count(&total).Error; err != nil {
		return nil, err
	}
	return map[string]any{"pool_id": poolID, "total_quota": total}, nil
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
