package service

import (
	"errors"
	"fmt"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/storage"

	"github.com/google/uuid"
)

type FileService struct {
	db   *database.DB
	stor storage.Backend
}

func NewFileService(db *database.DB, stor storage.Backend) *FileService {
	return &FileService{db: db, stor: stor}
}

func (s *FileService) GetFile(id string) (*database.CloudFile, error) {
	var file database.CloudFile
	if err := s.db.Preload("Object").First(&file, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &file, nil
}

func (s *FileService) GetChildren(parentID string) ([]database.CloudFile, error) {
	var files []database.CloudFile
	if err := s.db.Preload("Object").Where("parent_id = ?", parentID).Find(&files).Error; err != nil {
		return nil, err
	}
	return files, nil
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
	file := &database.CloudFile{ID: database.NewID(), Name: name, AccountID: accountID, ObjectID: &objectID, ParentID: parentID, Indexed: true, ApplicationType: appType}
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
		return s.db.Model(&database.FileObject{}).Where("id = ?", parent.ObjectID).Update("has_thumbnail", count > 0).Error
	}
	if kind == "system.compression" {
		return s.db.Model(&database.FileObject{}).Where("id = ?", parent.ObjectID).Update("has_compression", count > 0).Error
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
	if err := s.db.Model(&database.CloudFile{}).Where("parent_id = ? and application_type = ?", fileID, "system.compression").Count(&comp).Error; err != nil {
		return err
	}
	if err := s.db.Model(&database.FileObject{}).Where("id = ?", *file.ObjectID).Updates(map[string]any{"has_thumbnail": thumb > 0, "has_compression": comp > 0}).Error; err != nil {
		return err
	}
	return nil
}

type TaskService struct{ db *database.DB }

func NewTaskService(db *database.DB) *TaskService { return &TaskService{db: db} }

func (s *TaskService) CreateUploadTask(accountID uuid.UUID, name string, size int64, poolID *string, fileName string, contentType string) (*database.PersistentTask, error) {
	task := &database.PersistentTask{ID: database.NewID(), TaskID: database.NewID(), Name: name, Type: "file.upload", Status: "pending", AccountID: accountID, Progress: 0, LastActivity: time.Now(), FileName: &fileName, FileSize: &size, PoolID: poolID}
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

func (s *TaskService) ListTasks(accountID uuid.UUID) ([]database.PersistentTask, error) {
	var tasks []database.PersistentTask
	if err := s.db.Where("account_id = ?", accountID).Find(&tasks).Error; err != nil {
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
