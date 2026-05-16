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
		&FilePool{}, &FileObject{}, &CloudFile{}, &FileReplica{}, &FilePermission{}, &PoolPermission{}, &PersistentTask{}, &QuotaRecord{},
	); err != nil {
		return err
	}
	return d.DB.Exec(`
		alter table if exists file_pools alter column id type varchar(36);
		alter table if exists file_replicas alter column pool_id type varchar(36);
		alter table if exists file_permissions alter column file_id type varchar(36);
		alter table if exists pool_permissions alter column id type varchar(36);
		alter table if exists pool_permissions alter column pool_id type varchar(36);
		alter table if exists persistent_tasks alter column pool_id type varchar(36);
		alter table if exists persistent_tasks alter column parent_id type varchar(36);
		alter table if exists cloud_files alter column object_id type varchar(36);
		alter table if exists cloud_files alter column parent_id type varchar(36);
	`).Error
}
