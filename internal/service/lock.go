package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"src.solsynth.dev/sosys/filesystem/internal/database"
)

var (
	ErrLockConflict      = errors.New("file is already locked by another holder")
	ErrLockNotFound      = errors.New("lock not found or expired")
	ErrLockTokenMismatch = errors.New("lock token does not match current lock")
)

func (s *FileService) CleanExpiredLocks(ctx context.Context) error {
	return s.db.WithContext(ctx).Where("expires_at <= ?", time.Now()).Delete(&database.FileLock{}).Error
}

func (s *FileService) GetLock(ctx context.Context, fileID string) (*database.FileLock, error) {
	if err := s.CleanExpiredLocks(ctx); err != nil {
		return nil, err
	}
	var lock database.FileLock
	if err := s.db.WithContext(ctx).Where("file_id = ?", fileID).First(&lock).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &lock, nil
}

func (s *FileService) AcquireLock(ctx context.Context, fileID, protocol, lockToken string, accountID *uuid.UUID, timeout int) (*database.FileLock, error) {
	if strings.TrimSpace(lockToken) == "" {
		return nil, fmt.Errorf("lock token is required")
	}
	if timeout <= 0 {
		timeout = 1800
	}

	var result *database.FileLock
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_ = tx.Where("expires_at <= ?", time.Now()).Delete(&database.FileLock{}).Error

		var current database.FileLock
		err := tx.Where("file_id = ?", fileID).First(&current).Error
		hasCurrent := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if hasCurrent && current.LockToken != lockToken {
			return ErrLockConflict
		}

		if hasCurrent {
			updates := map[string]any{
				"lock_token": lockToken,
				"protocol":   protocol,
				"account_id": accountID,
				"timeout":    timeout,
				"expires_at": time.Now().Add(time.Duration(timeout) * time.Second),
			}
			if err := tx.Model(&database.FileLock{}).Where("id = ?", current.ID).Updates(updates).Error; err != nil {
				return err
			}
			result = &current
			result.LockToken = lockToken
			result.Protocol = protocol
			result.AccountID = accountID
			result.Timeout = timeout
			result.ExpiresAt = time.Now().Add(time.Duration(timeout) * time.Second)
			return nil
		}

		lock := &database.FileLock{
			FileID:    fileID,
			LockToken: lockToken,
			Protocol:  protocol,
			AccountID: accountID,
			Timeout:   timeout,
			ExpiresAt: time.Now().Add(time.Duration(timeout) * time.Second),
		}
		if err := tx.Create(lock).Error; err != nil {
			return err
		}
		result = lock
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *FileService) RefreshLock(ctx context.Context, fileID, lockToken string, timeout int) error {
	if timeout <= 0 {
		timeout = 1800
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_ = tx.Where("expires_at <= ?", time.Now()).Delete(&database.FileLock{}).Error

		var current database.FileLock
		if err := tx.Where("file_id = ?", fileID).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrLockNotFound
			}
			return err
		}

		if current.LockToken != lockToken {
			return ErrLockTokenMismatch
		}

		return tx.Model(&database.FileLock{}).Where("id = ?", current.ID).Updates(map[string]any{
			"timeout":    timeout,
			"expires_at": time.Now().Add(time.Duration(timeout) * time.Second),
		}).Error
	})
}

func (s *FileService) ReleaseLock(ctx context.Context, fileID, lockToken string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_ = tx.Where("expires_at <= ?", time.Now()).Delete(&database.FileLock{}).Error

		var current database.FileLock
		if err := tx.Where("file_id = ?", fileID).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrLockNotFound
			}
			return err
		}

		if current.LockToken != lockToken {
			return ErrLockTokenMismatch
		}

		return tx.Delete(&database.FileLock{}, "id = ?", current.ID).Error
	})
}

func (s *FileService) IsFileLocked(ctx context.Context, fileID string) (bool, error) {
	lock, err := s.GetLock(ctx, fileID)
	if err != nil {
		return false, err
	}
	return lock != nil, nil
}
