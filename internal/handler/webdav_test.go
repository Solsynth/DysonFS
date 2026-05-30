package handler

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

func TestWebDAVReadDirTreatsDotAsRoot(t *testing.T) {
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()

	rootFile := database.CloudFile{ID: database.NewID(), Name: "Story", AccountID: accountID, Indexed: true}
	rootFolder := database.CloudFile{ID: database.NewID(), Name: "Solsynth", AccountID: accountID, Indexed: true, IsFolder: true}
	child := database.CloudFile{ID: database.NewID(), Name: "chapter-1.txt", AccountID: accountID, ParentID: &rootFolder.ID, Indexed: true}
	unindexed := database.CloudFile{ID: database.NewID(), Name: "hidden", AccountID: accountID, Indexed: false}
	if err := db.Create(&rootFile).Error; err != nil {
		t.Fatalf("create root file: %v", err)
	}
	if err := db.Create(&rootFolder).Error; err != nil {
		t.Fatalf("create root folder: %v", err)
	}
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := db.Create(&unindexed).Error; err != nil {
		t.Fatalf("create unindexed file: %v", err)
	}

	fs := &webdavFS{files: files, accountID: accountID.String()}
	items, err := fs.ReadDir(context.Background(), ".", -1)
	if err != nil {
		t.Fatalf("ReadDir(.) error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(ReadDir(.)) = %d, want 2", len(items))
	}
	names := map[string]os.FileMode{}
	for _, item := range items {
		names[item.Name()] = item.Mode()
	}
	if _, ok := names["Story"]; !ok {
		t.Fatal("ReadDir(.) missing root file Story")
	}
	if _, ok := names["Solsynth"]; !ok {
		t.Fatal("ReadDir(.) missing root folder Solsynth")
	}
	if !names["Solsynth"].IsDir() {
		t.Fatalf("Solsynth mode = %v, want directory", names["Solsynth"])
	}
}

func TestWebDAVResolvePathNormalizesRelativePaths(t *testing.T) {
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()

	folder := database.CloudFile{ID: database.NewID(), Name: "Solsynth", AccountID: accountID, Indexed: true, IsFolder: true}
	objectID := database.NewID()
	object := database.FileObject{ID: objectID, Size: 4, MimeType: "text/plain", Hash: "hash", Meta: datatypes.JSON([]byte(`{}`))}
	file := database.CloudFile{ID: database.NewID(), Name: "track.txt", AccountID: accountID, ParentID: &folder.ID, ObjectID: &objectID, Indexed: true}
	if err := db.Create(&folder).Error; err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if err := db.Create(&object).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	if err := db.Create(&file).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}

	fs := &webdavFS{files: files, accountID: accountID.String()}
	got, err := fs.resolvePath(context.Background(), "Solsynth/track.txt")
	if err != nil {
		t.Fatalf("resolvePath(relative) error = %v", err)
	}
	if got.ID != file.ID {
		t.Fatalf("resolvePath(relative) ID = %q, want %q", got.ID, file.ID)
	}
}

func TestWebDAVFileInfoModeMarksDirectories(t *testing.T) {
	dirInfo := &webdavFileInfo{name: "folder", isDir: true}
	if !dirInfo.Mode().IsDir() {
		t.Fatalf("directory mode = %v, want dir bit set", dirInfo.Mode())
	}

	fileInfo := &webdavFileInfo{name: "file.txt", isDir: false}
	if fileInfo.Mode().IsDir() {
		t.Fatalf("file mode = %v, want regular file", fileInfo.Mode())
	}
}
