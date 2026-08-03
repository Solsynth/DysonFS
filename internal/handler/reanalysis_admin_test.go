package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

// allowFilesManage authenticates the caller as an account with the
// files.manage permission, the same check the other admin endpoints use.
func allowFilesManage(t *testing.T, files *service.FileService, accountID uuid.UUID) {
	t.Helper()
	files.SetPermissionChecker(permissionCheckerFunc(func(_ context.Context, gotAccountID, key string) (bool, error) {
		if gotAccountID != accountID.String() || key != service.PermissionFilesManage {
			t.Fatalf("permission check = (%q, %q), want (%q, %q)", gotAccountID, key, accountID, service.PermissionFilesManage)
		}
		return true, nil
	}))
}

// seedReanalysisImage creates an image file on the local backend that is
// missing its source metadata and compression derivative, i.e. a file the
// reanalysis scan must flag and the repair pass must fix.
func seedReanalysisImage(t *testing.T, db *gorm.DB, stor storage.Backend) (fileID, objectID string) {
	t.Helper()
	jpeg := generateTestJPEG(t)
	objectID = database.NewID()
	if err := db.Create(&database.FileObject{ID: objectID, Size: int64(len(jpeg)), MimeType: "image/jpeg", Hash: service.ComputeHash(jpeg), StorageKey: &objectID, Meta: datatypes.JSON([]byte(`{}`))}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	fileID = database.NewID()
	if err := db.Create(&database.CloudFile{ID: fileID, Name: "photo.jpg", AccountID: uuid.New(), ObjectID: &objectID, StorageKey: &objectID, Indexed: true}).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := stor.Put(context.Background(), objectID, bytes.NewReader(jpeg), int64(len(jpeg)), "image/jpeg"); err != nil {
		t.Fatalf("put object: %v", err)
	}
	return fileID, objectID
}

func newReanalysisTestRouter(t *testing.T, db *gorm.DB, files *service.FileService, accountID uuid.UUID) *gin.Engine {
	t.Helper()
	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)
	return r
}

func TestAdminReanalysisRequiresFilesManagePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	accountID := uuid.New()
	files.SetPermissionChecker(permissionCheckerFunc(func(_ context.Context, gotAccountID, key string) (bool, error) {
		if gotAccountID != accountID.String() || key != service.PermissionFilesManage {
			t.Fatalf("permission check = (%q, %q), want (%q, %q)", gotAccountID, key, accountID, service.PermissionFilesManage)
		}
		return false, nil
	}))
	r := newReanalysisTestRouter(t, db, files, accountID)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/reanalysis/candidates", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/reanalysis/run", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("run status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}

func TestAdminReanalysisCandidatesListsBrokenFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	accountID := uuid.New()
	allowFilesManage(t, files, accountID)
	fileID, _ := seedReanalysisImage(t, db, stor)
	r := newReanalysisTestRouter(t, db, files, accountID)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/reanalysis/candidates?limit=50", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var candidates []service.ReanalysisCandidate
	if err := json.Unmarshal(w.Body.Bytes(), &candidates); err != nil {
		t.Fatalf("decode candidates: %v", err)
	}
	found := false
	for _, candidate := range candidates {
		if candidate.FileID != fileID {
			continue
		}
		found = true
		if candidate.Reason == "" {
			t.Fatalf("candidate %s has empty reason", fileID)
		}
		if candidate.Kind != "image" {
			t.Fatalf("candidate kind = %q, want image", candidate.Kind)
		}
	}
	if !found {
		t.Fatalf("seeded file %s missing from candidates: %+v", fileID, candidates)
	}

	// kind=video must exclude the image-only seed.
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/reanalysis/candidates?kind=video", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("video candidates status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var video []service.ReanalysisCandidate
	if err := json.Unmarshal(w.Body.Bytes(), &video); err != nil {
		t.Fatalf("decode video candidates: %v", err)
	}
	if len(video) != 0 {
		t.Fatalf("kind=video returned %d candidates, want 0 for image-only seed", len(video))
	}
}

func TestAdminReanalysisRunRepairsCandidates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	accountID := uuid.New()
	allowFilesManage(t, files, accountID)
	fileID, objectID := seedReanalysisImage(t, db, stor)
	r := newReanalysisTestRouter(t, db, files, accountID)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/reanalysis/run", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("run status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var result service.ReanalysisResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode run result: %v", err)
	}
	if result.Scanned != 1 || result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("run result = %+v, want scanned=1 updated=1 failed=0", result)
	}

	// The compression derivative and its flags must exist after the repair.
	var child database.CloudFile
	if err := db.Preload("Object").First(&child, "parent_id = ? AND application_type = ?", fileID, "system.compression.low").Error; err != nil {
		t.Fatalf("compression derivative not created: %v", err)
	}
	if child.Object == nil || child.Object.Size == 0 {
		t.Fatalf("compression derivative object is empty: %+v", child.Object)
	}
	var parent database.CloudFile
	if err := db.Preload("Object").First(&parent, "id = ?", fileID).Error; err != nil {
		t.Fatalf("reload parent: %v", err)
	}
	if parent.Object == nil || !parent.Object.HasCompression {
		t.Fatalf("parent object has_compression = %+v, want true after repair", parent.Object)
	}
	if _, _, err := stor.Get(context.Background(), objectID); err != nil {
		t.Fatalf("source object disappeared: %v", err)
	}
}

func TestAdminReanalysisFilesRepairsSpecificIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	accountID := uuid.New()
	allowFilesManage(t, files, accountID)
	fileID, _ := seedReanalysisImage(t, db, stor)
	r := newReanalysisTestRouter(t, db, files, accountID)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/reanalysis/files", strings.NewReader(`{"file_ids":["`+fileID+`"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("files status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var result service.ReanalysisResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode files result: %v", err)
	}
	if result.Scanned != 1 || result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("files result = %+v, want scanned=1 updated=1 failed=0", result)
	}
	var child database.CloudFile
	if err := db.First(&child, "parent_id = ? AND application_type = ?", fileID, "system.compression.low").Error; err != nil {
		t.Fatalf("compression derivative not created: %v", err)
	}

	// Unknown IDs are counted as failed, not fatal.
	req = httptest.NewRequest(http.MethodPost, "/api/admin/reanalysis/files", strings.NewReader(`{"file_ids":["does-not-exist"]}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("unknown id status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode unknown id result: %v", err)
	}
	if result.Scanned != 1 || result.Failed != 1 {
		t.Fatalf("unknown id result = %+v, want scanned=1 failed=1", result)
	}

	// Empty file_ids is a client error.
	req = httptest.NewRequest(http.MethodPost, "/api/admin/reanalysis/files", strings.NewReader(`{"file_ids":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty file_ids status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}
