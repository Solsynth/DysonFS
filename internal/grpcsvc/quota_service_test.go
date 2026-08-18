package grpcsvc

import (
	"context"
	"testing"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	gen "src.solsynth.dev/sosys/go/proto"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetUsedQuotaCountsActiveWorkspaceObjects(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+database.NewID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&database.FileObject{}, &database.CloudFile{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	workspaceID := uuid.New().String()
	accountID := uuid.New()
	activeObjectID := database.NewID()
	deletedObjectID := database.NewID()
	if err := db.Create(&database.FileObject{ID: activeObjectID, Size: 123}).Error; err != nil {
		t.Fatalf("create active object: %v", err)
	}
	if err := db.Create(&database.FileObject{ID: deletedObjectID, Size: 456, DeletedAt: gorm.DeletedAt{Valid: true}}).Error; err != nil {
		t.Fatalf("create deleted object: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), AccountID: accountID, WorkspaceID: &workspaceID, ObjectID: &activeObjectID}).Error; err != nil {
		t.Fatalf("create active file: %v", err)
	}
	if err := db.Create(&database.CloudFile{ID: database.NewID(), AccountID: accountID, WorkspaceID: &workspaceID, ObjectID: &deletedObjectID}).Error; err != nil {
		t.Fatalf("create deleted-object file: %v", err)
	}

	server := &quotaServiceServer{files: service.NewFileService(&database.DB{DB: db}, nil)}
	response, err := server.GetUsedQuota(context.Background(), &gen.DyGetUsedQuotaRequest{WorkspaceId: workspaceID})
	if err != nil {
		t.Fatalf("GetUsedQuota() error = %v", err)
	}
	if response.GetUsedBytes() != 123 {
		t.Fatalf("used_bytes = %d, want 123", response.GetUsedBytes())
	}
}
