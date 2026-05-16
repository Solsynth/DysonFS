package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	gen "src.solsynth.dev/sosys/go/proto"

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

func NewFileService(db *database.DB, stor storage.Backend) *FileService {
	return &FileService{db: db, stor: stor}
}

func (s *FileService) DB() *database.DB { return s.db }

func (s *FileService) Storage() storage.Backend { return s.stor }

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
	object := &database.FileObject{ID: database.NewID(), MimeType: mimeType, Hash: "", Meta: datatypes.JSON([]byte(`{}`)), HasCompression: false, HasThumbnail: false}
	if err := s.db.Create(object).Error; err != nil {
		return nil, err
	}
	return object, nil
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
