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

func (d *DB) AutoMigrate() error {
	if err := d.DB.AutoMigrate(
		&FilePool{}, &FileObject{}, &CloudFile{}, &FilePermission{}, &PoolPermission{}, &PersistentTask{}, &QuotaRecord{}, &FileLock{}, &WebDAVToken{},
	); err != nil {
		return err
	}
	return nil
}
