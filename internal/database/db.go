package database

import (
	"fmt"

	"src.solsynth.dev/sosys/filesystem/internal/config"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type DB struct {
	*gorm.DB
}

func Open(cfg *config.Config) (*DB, error) {
	if cfg.Database.DSN == "" {
		return nil, fmt.Errorf("database dsn is required")
	}

	gormLogger := logger.Default.LogMode(logger.Warn)

	db, err := gorm.Open(postgres.Open(cfg.Database.DSN), &gorm.Config{Logger: gormLogger})
	if err != nil {
		return nil, err
	}

	return &DB{DB: db}, nil
}

// Models lists every model the package defines. Schema ownership belongs to
// the modules that declare which of these they use; this list exists so tests
// and tooling can enumerate the domain in one place.
func Models() []any {
	return []any{
		&FilePool{}, &FileObject{}, &CloudFile{}, &FilePermission{}, &PoolPermission{},
		&PersistentTask{}, &UploadChunk{}, &FileLock{}, &WebDAVToken{}, &StorageNode{}, &S3Token{},
	}
}
