package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/webdav"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

type webdavLockSystem struct {
	files     *service.FileService
	accountID string
}

func (l *webdavLockSystem) Confirm(now time.Time, name0, name1 string, conditions ...webdav.Condition) (func(), error) {
	ctx := context.Background()
	for _, cond := range conditions {
		if cond.Not {
			lock, err := l.files.GetLock(ctx, name0)
			if err != nil {
				return func() {}, err
			}
			if lock != nil && lock.LockToken == cond.Token {
				return func() {}, webdav.ErrConfirmationFailed
			}
			continue
		}

		lock, err := l.files.GetLock(ctx, name0)
		if err != nil {
			return func() {}, err
		}
		if lock == nil {
			return func() {}, webdav.ErrConfirmationFailed
		}
		if lock.LockToken != cond.Token {
			return func() {}, webdav.ErrConfirmationFailed
		}

		release := func() {
			_ = l.files.ReleaseLock(context.Background(), name0, cond.Token)
		}
		return release, nil
	}
	return func() {}, webdav.ErrConfirmationFailed
}

func (l *webdavLockSystem) Create(now time.Time, details webdav.LockDetails) (string, error) {
	ctx := context.Background()
	token := fmt.Sprintf("urn:uuid:%s", uuid.New().String())
	timeout := int(details.Duration.Seconds())
	if timeout <= 0 {
		timeout = 1800
	}

	accountUUID := parseUUID(l.accountID)

	file, err := l.files.GetLock(ctx, details.Root)
	if err != nil {
		return "", err
	}
	if file != nil {
		return "", webdav.ErrLocked
	}

	lockFile, err := l.resolveToFile(ctx, details.Root)
	if err == nil && lockFile != nil {
		if _, err := l.files.AcquireLock(ctx, lockFile.ID, "webdav", token, &accountUUID, timeout); err != nil {
			if err == service.ErrLockConflict {
				return "", webdav.ErrLocked
			}
			return "", err
		}
		return token, nil
	}

	if _, err := l.files.AcquireLock(ctx, details.Root, "webdav", token, &accountUUID, timeout); err != nil {
		if err == service.ErrLockConflict {
			return "", webdav.ErrLocked
		}
		return "", err
	}
	return token, nil
}

func (l *webdavLockSystem) Refresh(now time.Time, token string, duration time.Duration) (webdav.LockDetails, error) {
	ctx := context.Background()
	lock, err := l.findLockByToken(ctx, token)
	if err != nil {
		return webdav.LockDetails{}, webdav.ErrNoSuchLock
	}

	timeout := int(duration.Seconds())
	if timeout <= 0 {
		timeout = 1800
	}

	if err := l.files.RefreshLock(ctx, lock.FileID, token, timeout); err != nil {
		return webdav.LockDetails{}, webdav.ErrNoSuchLock
	}

	return webdav.LockDetails{
		Root:      lock.FileID,
		Duration:  duration,
		ZeroDepth: true,
	}, nil
}

func (l *webdavLockSystem) Unlock(now time.Time, token string) error {
	ctx := context.Background()
	lock, err := l.findLockByToken(ctx, token)
	if err != nil {
		return webdav.ErrNoSuchLock
	}
	return l.files.ReleaseLock(ctx, lock.FileID, token)
}

func (l *webdavLockSystem) findLockByToken(ctx context.Context, token string) (*database.FileLock, error) {
	var lock database.FileLock
	if err := l.files.DB().WithContext(ctx).Where("lock_token = ?", token).First(&lock).Error; err != nil {
		return nil, err
	}
	return &lock, nil
}

func (l *webdavLockSystem) resolveToFile(ctx context.Context, path string) (*database.CloudFile, error) {
	fs := &webdavFS{files: l.files, accountID: l.accountID}
	return fs.resolvePath(ctx, path)
}

var _ webdav.LockSystem = (*webdavLockSystem)(nil)
