package module

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// MigrateSchema applies the schema owned by the resolved modules, in
// dependency order. Only enabled modules contribute models or hooks, so a
// fresh database never grows tables for a module that is not compiled in or
// not enabled.
func MigrateSchema(ctx context.Context, db *gorm.DB, factories []Factory) error {
	for _, factory := range factories {
		if len(factory.Models) > 0 {
			if err := db.WithContext(ctx).AutoMigrate(factory.Models...); err != nil {
				return fmt.Errorf("module %s AutoMigrate: %w", factory.ID, err)
			}
		}
		for _, hook := range factory.SQL {
			if hook == nil {
				continue
			}
			if err := hook(ctx, db); err != nil {
				return fmt.Errorf("module %s schema hook: %w", factory.ID, err)
			}
		}
	}
	return nil
}
